from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from datetime import date
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("compute_stats.py")
SPEC = importlib.util.spec_from_file_location("daily_run_compute_stats", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Unable to load {MODULE_PATH}")
STATS = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = STATS
SPEC.loader.exec_module(STATS)


def write_run(
    series_dir: Path,
    timestamp: str,
    run_id: str,
    *,
    succeeded: int,
    test_failed: int,
    infra_failed: int = 0,
    canceled: int = 0,
    state: str = "completed",
    total_instances: int | None = None,
) -> None:
    run_dir = series_dir / timestamp
    run_dir.mkdir(parents=True)
    total = succeeded + test_failed + infra_failed + canceled

    def bucket(count: int) -> dict:
        return {
            "count": count,
            "percentage": (count / total) * 100 if total else 0,
        }

    (run_dir / "results.json").write_text(
        json.dumps(
            {
                "run_id": run_id,
                "state": state,
                "total_instances": (
                    total if total_instances is None else total_instances
                ),
                "status": {
                    "succeeded": bucket(succeeded),
                    "test_failed": bucket(test_failed),
                    "infra_failed": bucket(infra_failed),
                    "canceled": bucket(canceled),
                },
                "usage": {
                    "input_tokens": 100,
                    "output_tokens": 20,
                    "tool_calls": 5,
                    "cost_usd": None,
                },
                "runtime": {"run_ms": 1_000},
            }
        )
    )


def load_runs(
    base_dirs: list[Path],
    *,
    minimum_valid_instances: int = 1,
    required_results_json: Path | None = None,
) -> list:
    return STATS.load_runs_from_directories(
        base_dirs,
        non_test_failure_policy="exclude",
        expected_instances=50,
        minimum_valid_instances=minimum_valid_instances,
        required_results_json=required_results_json,
    )


class CombinedRunsTests(unittest.TestCase):
    def test_failed_same_day_run_is_excluded_from_statistics(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            series = Path(temp_dir) / "series"
            write_run(
                series,
                "20260712-040001",
                "failed-partial",
                succeeded=50,
                test_failed=0,
                state="failed",
            )
            write_run(
                series,
                "20260712-050001",
                "completed",
                succeeded=40,
                test_failed=10,
            )

            runs = load_runs([series])
            output = STATS.compute_dashboard_stats(
                runs,
                date(2026, 7, 12),
                baseline_p0=0.8,
            )

            self.assertEqual([run.run_id for run in runs], ["completed"])
            self.assertEqual(output["total_runs_analyzed"], 1)
            self.assertEqual(output["timescales"]["daily"]["successes"], 40)
            self.assertEqual(output["timescales"]["daily"]["trials"], 50)

    def test_malformed_completed_same_day_run_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            series = Path(temp_dir) / "series"
            write_run(
                series,
                "20260712-040001",
                "valid",
                succeeded=40,
                test_failed=10,
            )
            write_run(
                series,
                "20260712-050001",
                "malformed",
                succeeded=40,
                test_failed=10,
            )
            malformed_path = series / "20260712-050001" / "results.json"
            malformed = json.loads(malformed_path.read_text())
            del malformed["status"]["succeeded"]
            malformed_path.write_text(json.dumps(malformed))

            with self.assertRaisesRegex(
                ValueError,
                r"20260712-050001/results\.json: missing required field "
                r"status\.succeeded\.count",
            ):
                load_runs([series])

    def test_prior_completed_run_below_minimum_does_not_poison_retry(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            series = Path(temp_dir) / "series"
            write_run(
                series,
                "20260712-040001",
                "non-reusable-prior",
                succeeded=49,
                test_failed=0,
                infra_failed=1,
            )
            write_run(
                series,
                "20260712-050001",
                "valid-current",
                succeeded=40,
                test_failed=10,
            )
            current_results = series / "20260712-050001" / "results.json"

            runs = load_runs(
                [series],
                minimum_valid_instances=50,
                required_results_json=current_results,
            )
            output = STATS.compute_dashboard_stats(
                runs,
                date(2026, 7, 12),
                baseline_p0=0.8,
            )

            self.assertEqual([run.run_id for run in runs], ["valid-current"])
            self.assertEqual(output["total_runs_analyzed"], 1)
            self.assertEqual(output["timescales"]["daily"]["successes"], 40)
            self.assertEqual(output["timescales"]["daily"]["trials"], 50)

    def test_prior_completed_runs_with_invalid_totals_are_excluded(self) -> None:
        cases = (
            {
                "name": "reported total",
                "succeeded": 40,
                "test_failed": 10,
                "total_instances": 49,
            },
            {
                "name": "status total",
                "succeeded": 40,
                "test_failed": 9,
                "total_instances": 50,
            },
        )
        for case in cases:
            with self.subTest(case["name"]), tempfile.TemporaryDirectory() as temp_dir:
                series = Path(temp_dir) / "series"
                write_run(
                    series,
                    "20260712-040001",
                    "non-reusable-prior",
                    succeeded=case["succeeded"],
                    test_failed=case["test_failed"],
                    total_instances=case["total_instances"],
                )
                write_run(
                    series,
                    "20260712-050001",
                    "valid-current",
                    succeeded=40,
                    test_failed=10,
                )
                current_results = series / "20260712-050001" / "results.json"

                runs = load_runs(
                    [series],
                    minimum_valid_instances=50,
                    required_results_json=current_results,
                )

                self.assertEqual([run.run_id for run in runs], ["valid-current"])
                self.assertEqual(sum(run.total for run in runs), 50)

    def test_non_reusable_current_run_fails_instead_of_being_omitted(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            series = Path(temp_dir) / "series"
            write_run(
                series,
                "20260712-040001",
                "valid-prior",
                succeeded=40,
                test_failed=10,
            )
            write_run(
                series,
                "20260712-050001",
                "invalid-current",
                succeeded=49,
                test_failed=0,
                infra_failed=1,
            )
            current_results = series / "20260712-050001" / "results.json"

            with self.assertRaisesRegex(
                ValueError,
                "required current run is not reusable: policy denominator is 49",
            ):
                load_runs(
                    [series],
                    minimum_valid_instances=50,
                    required_results_json=current_results,
                )

    def test_five_infrastructure_failures_are_reusable_when_explicitly_bounded(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            series = Path(temp_dir) / "series"
            write_run(
                series,
                "20260726-080001",
                "bounded-infra-failure",
                succeeded=39,
                test_failed=6,
                infra_failed=5,
                state="failed",
            )
            current_results = series / "20260726-080001" / "results.json"

            runs = load_runs(
                [series],
                minimum_valid_instances=45,
                required_results_json=current_results,
            )
            output = STATS.compute_dashboard_stats(
                runs,
                date(2026, 7, 26),
                baseline_p0=0.73,
            )

            self.assertEqual([run.run_id for run in runs], ["bounded-infra-failure"])
            self.assertEqual(output["timescales"]["daily"]["successes"], 39)
            self.assertEqual(output["timescales"]["daily"]["trials"], 45)

    def test_infrastructure_failures_beyond_minimum_bound_are_rejected(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            series = Path(temp_dir) / "series"
            write_run(
                series,
                "20260726-080001",
                "excessive-infra-failures",
                succeeded=39,
                test_failed=5,
                infra_failed=6,
                state="failed",
            )
            current_results = series / "20260726-080001" / "results.json"

            with self.assertRaisesRegex(
                ValueError,
                "required current run is not reusable: policy denominator is 44",
            ):
                load_runs(
                    [series],
                    minimum_valid_instances=45,
                    required_results_json=current_results,
                )

    def test_non_test_failure_policy_is_explicit_and_deterministic(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            series = Path(temp_dir) / "series"
            write_run(
                series,
                "20260712-040002",
                "policy-test",
                succeeded=38,
                test_failed=10,
                infra_failed=1,
                canceled=1,
            )
            results_json = series / "20260712-040002" / "results.json"

            excluded = STATS.load_benchmark_run(
                results_json,
                non_test_failure_policy="exclude",
                expected_instances=50,
                minimum_valid_instances=1,
            )
            counted = STATS.load_benchmark_run(
                results_json,
                non_test_failure_policy="count-as-failure",
                expected_instances=50,
                minimum_valid_instances=1,
            )

            self.assertEqual(excluded.total, 48)
            self.assertEqual(counted.total, 50)
            with self.assertRaisesRegex(ValueError, "reject policy"):
                STATS.load_benchmark_run(
                    results_json,
                    non_test_failure_policy="reject",
                    expected_instances=50,
                    minimum_valid_instances=1,
                )

    def test_explicit_series_are_combined_and_future_runs_are_excluded(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            high = root / "gpt-5-6-sol-high"
            xhigh = root / "gpt-5-6-sol-xhigh"
            older = root / "gpt-5-5-xhigh"

            write_run(
                high,
                "20260711-105627",
                "high-0711",
                succeeded=40,
                test_failed=9,
                infra_failed=1,
            )
            write_run(
                xhigh,
                "20260711-001500",
                "xhigh-0711",
                succeeded=39,
                test_failed=7,
                infra_failed=4,
            )
            write_run(
                high,
                "20260712-040002",
                "high-0712",
                succeeded=38,
                test_failed=11,
                infra_failed=1,
            )
            write_run(
                high,
                "20260713-040001",
                "future-high",
                succeeded=50,
                test_failed=0,
            )
            write_run(
                older,
                "20260710-040001",
                "older-model",
                succeeded=1,
                test_failed=49,
            )

            runs = load_runs([high, xhigh])
            output = STATS.compute_dashboard_stats(runs, date(2026, 7, 12), baseline_p0=0.8340)

            self.assertEqual(output["total_runs_analyzed"], 3)
            self.assertEqual(
                output["daily_history"],
                [
                    {"date": "2026-07-11", "passed": 79, "total": 95, "accuracy": 0.8316},
                    {"date": "2026-07-12", "passed": 38, "total": 49, "accuracy": 0.7755},
                ],
            )
            self.assertEqual(output["timescales"]["daily"]["successes"], 38)
            self.assertEqual(output["timescales"]["daily"]["trials"], 49)
            self.assertEqual(output["timescales"]["monthly"]["successes"], 117)
            self.assertEqual(output["timescales"]["monthly"]["trials"], 144)
            self.assertEqual(output["baseline_p0"], 0.8340)

    def test_duplicate_run_ids_across_series_are_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            high = root / "gpt-5-6-sol-high"
            xhigh = root / "gpt-5-6-sol-xhigh"

            write_run(
                high,
                "20260711-105627",
                "duplicate",
                succeeded=40,
                test_failed=10,
            )
            write_run(
                xhigh,
                "20260711-001500",
                "duplicate",
                succeeded=39,
                test_failed=11,
            )

            with self.assertRaisesRegex(ValueError, "duplicate run_id"):
                load_runs([high, xhigh])

    def test_every_explicit_series_directory_must_exist_and_have_results(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            high = root / "gpt-5-6-sol-high"
            empty_xhigh = root / "gpt-5-6-sol-xhigh"
            empty_xhigh.mkdir()

            write_run(
                high,
                "20260711-105627",
                "high-0711",
                succeeded=40,
                test_failed=10,
            )

            with self.assertRaisesRegex(ValueError, "no results.json files"):
                load_runs([high, empty_xhigh])

            with self.assertRaisesRegex(ValueError, "runs directory not found"):
                load_runs([high, root / "missing-series"])

    def test_series_with_only_failed_runs_does_not_block_completed_series(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            high = root / "gpt-5-6-sol-high"
            xhigh = root / "gpt-5-6-sol-xhigh"

            write_run(
                high,
                "20260712-040001",
                "completed-high",
                succeeded=40,
                test_failed=10,
            )
            write_run(
                xhigh,
                "20260712-050001",
                "failed-xhigh",
                succeeded=50,
                test_failed=0,
                state="failed",
            )

            runs = load_runs([high, xhigh])

            self.assertEqual([run.run_id for run in runs], ["completed-high"])
            self.assertEqual(sum(run.total for run in runs), 50)

    def test_all_configured_series_with_only_failed_runs_are_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            high = root / "gpt-5-6-sol-high"
            xhigh = root / "gpt-5-6-sol-xhigh"

            write_run(
                high,
                "20260712-040001",
                "failed-high",
                succeeded=50,
                test_failed=0,
                state="failed",
            )
            write_run(
                xhigh,
                "20260712-050001",
                "failed-xhigh",
                succeeded=50,
                test_failed=0,
                state="failed",
            )

            with self.assertRaisesRegex(
                ValueError,
                "no completed results.json files found in configured runs directories",
            ):
                load_runs([high, xhigh])


if __name__ == "__main__":
    unittest.main()
