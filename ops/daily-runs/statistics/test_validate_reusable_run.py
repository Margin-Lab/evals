from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("validate_reusable_run.py")
SPEC = importlib.util.spec_from_file_location("validate_reusable_run", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Unable to load {MODULE_PATH}")
VALIDATOR = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = VALIDATOR
SPEC.loader.exec_module(VALIDATOR)


class ReusableRunTests(unittest.TestCase):
    def make_pair(
        self,
        root: Path,
        *,
        state: str = "completed",
        instances: int = 50,
        succeeded: int = 40,
        test_failed: int = 10,
        infra_failed: int = 0,
        canceled: int = 0,
        target_date: str = "2026-07-25",
        policy: str | None = "exclude",
    ) -> tuple[Path, Path]:
        results_path = root / "results.json"
        statistics_path = root / "statistics.json"
        results_path.write_text(
            json.dumps(
                {
                    "state": state,
                    "total_instances": instances,
                    "status": {
                        "succeeded": {"count": succeeded},
                        "test_failed": {"count": test_failed},
                        "infra_failed": {"count": infra_failed},
                        "canceled": {"count": canceled},
                    },
                }
            )
        )
        statistics = {"target_date": target_date}
        if policy is not None:
            statistics["non_test_failure_policy"] = policy
        statistics_path.write_text(json.dumps(statistics))
        return results_path, statistics_path

    def validate(self, results: Path, statistics: Path) -> None:
        VALIDATOR.validate_reusable_run(
            results,
            statistics,
            target_date="2026-07-25",
            expected_instances=50,
            minimum_valid_instances=50,
            policy="exclude",
        )

    def test_complete_matching_pair_is_reusable(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            pair = self.make_pair(Path(temp_dir))
            self.validate(*pair)

    def test_failed_or_incomplete_results_are_not_reusable(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            pair = self.make_pair(Path(temp_dir), state="failed")
            with self.assertRaisesRegex(ValueError, "state must be completed"):
                self.validate(*pair)

    def test_suite_size_date_and_policy_must_match(self) -> None:
        cases = (
            ({"instances": 49}, "total_instances"),
            ({"target_date": "2026-07-24"}, "target_date"),
            ({"policy": None}, "non_test_failure_policy"),
            ({"policy": "count-as-failure"}, "non_test_failure_policy"),
            (
                {"succeeded": 39, "test_failed": 10, "infra_failed": 1},
                "at least 50 valid instances",
            ),
        )
        for values, message in cases:
            with self.subTest(values=values):
                with tempfile.TemporaryDirectory() as temp_dir:
                    pair = self.make_pair(Path(temp_dir), **values)
                    with self.assertRaisesRegex(ValueError, message):
                        self.validate(*pair)


if __name__ == "__main__":
    unittest.main()
