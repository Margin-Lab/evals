from __future__ import annotations

import copy
import hashlib
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any, Callable
from unittest import mock


MODULE_PATH = Path(__file__).with_name("normalize_legacy_policy.py")
SPEC = importlib.util.spec_from_file_location("normalize_legacy_policy", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Unable to load {MODULE_PATH}")
NORMALIZER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = NORMALIZER
sys.path.insert(0, str(MODULE_PATH.parent))
try:
    SPEC.loader.exec_module(NORMALIZER)
finally:
    sys.path.remove(str(MODULE_PATH.parent))


def sha256(contents: bytes) -> str:
    return hashlib.sha256(contents).hexdigest()


class NormalizeLegacyPolicyTests(unittest.TestCase):
    TEST_MIGRATION_ID = "test-fixture-20260725-080000"

    def test_reviewed_migrations_use_tracker_run_directory_names(self) -> None:
        self.assertEqual(
            NORMALIZER.MIGRATIONS["codex-20260725-040001"].run_directory,
            "20260725_040001",
        )
        self.assertEqual(
            NORMALIZER.MIGRATIONS[
                "claude-code-20260725-045635"
            ].run_directory,
            "20260725_045635",
        )

    def results(self) -> dict[str, Any]:
        return {
            "state": "completed",
            "total_instances": 50,
            "status": {
                "succeeded": {"count": 40},
                "test_failed": {"count": 10},
                "infra_failed": {"count": 0},
                "canceled": {"count": 0},
            },
        }

    def statistics(self) -> dict[str, Any]:
        return {
            "computed_at": "2026-07-25T09:53:36.415386Z",
            "target_date": "2026-07-25",
            "baseline_p0": 0.73,
            "total_runs_analyzed": 1,
            "timescales": {
                "daily": {
                    "window_size": 1,
                    "successes": 40,
                    "trials": 50,
                    "accuracy": 0.8,
                },
                "weekly": {
                    "window_size": 7,
                    "successes": 40,
                    "trials": 50,
                    "accuracy": 0.8,
                },
                "monthly": {
                    "window_size": 30,
                    "successes": 40,
                    "trials": 50,
                    "accuracy": 0.8,
                },
            },
            "daily_history": [
                {
                    "date": "2026-07-25",
                    "passed": 40,
                    "total": 50,
                    "accuracy": 0.8,
                }
            ],
        }

    def serialize_legacy(self, statistics: dict[str, Any]) -> bytes:
        return json.dumps(statistics, indent=2).encode()

    def serialize_normalized(self, statistics: dict[str, Any]) -> bytes:
        normalized: dict[str, Any] = {}
        for key, value in statistics.items():
            normalized[key] = value
            if key == "target_date":
                normalized["non_test_failure_policy"] = "exclude"
        return (json.dumps(normalized, indent=2) + "\n").encode()

    def write_fixture(
        self,
        root: Path,
        *,
        results: dict[str, Any] | None = None,
        statistics: dict[str, Any] | None = None,
    ) -> tuple[Path, Path, Path, bytes, bytes, bytes]:
        results_value = results if results is not None else self.results()
        statistics_value = statistics if statistics is not None else self.statistics()
        results_contents = (json.dumps(results_value) + "\n").encode()
        legacy_contents = self.serialize_legacy(statistics_value)
        normalized_contents = self.serialize_normalized(statistics_value)
        run_root = root / "20260725-080000"
        run_root.mkdir()
        results_path = run_root / "results.json"
        statistics_path = run_root / "statistics.json"
        backup_path = root / "statistics.legacy.json"
        results_path.write_bytes(results_contents)
        statistics_path.write_bytes(legacy_contents)
        return (
            results_path,
            statistics_path,
            backup_path,
            results_contents,
            legacy_contents,
            normalized_contents,
        )

    def normalize(
        self,
        fixture: tuple[Path, Path, Path, bytes, bytes, bytes],
        **overrides: Any,
    ) -> bool:
        (
            results_path,
            statistics_path,
            backup_path,
            results_contents,
            legacy_contents,
            normalized_contents,
        ) = fixture
        migration_arguments = {
            "run_directory": "20260725-080000",
            "target_date": "2026-07-25",
            "expected_instances": 50,
            "minimum_valid_instances": 50,
            "results_sha256": sha256(results_contents),
            "legacy_statistics_sha256": sha256(legacy_contents),
            "normalized_statistics_sha256": sha256(normalized_contents),
        }
        migration_arguments.update(overrides)
        migration = NORMALIZER.Migration(**migration_arguments)
        with mock.patch.dict(
            NORMALIZER.MIGRATIONS,
            {self.TEST_MIGRATION_ID: migration},
        ):
            return NORMALIZER.normalize_legacy_policy(
                results_path,
                statistics_path,
                backup_path,
                migration_id=self.TEST_MIGRATION_ID,
            )

    def test_normalizes_exact_input_and_is_idempotent(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            fixture = self.write_fixture(Path(temp_dir))
            (
                _,
                statistics_path,
                backup_path,
                _,
                legacy_contents,
                normalized_contents,
            ) = fixture

            self.assertTrue(self.normalize(fixture))
            self.assertEqual(statistics_path.read_bytes(), normalized_contents)
            self.assertEqual(backup_path.read_bytes(), legacy_contents)
            self.assertFalse(self.normalize(fixture))
            self.assertEqual(statistics_path.read_bytes(), normalized_contents)
            self.assertEqual(backup_path.read_bytes(), legacy_contents)

    def test_wrong_hash_fails_without_changing_input(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            fixture = self.write_fixture(Path(temp_dir))
            statistics_path = fixture[1]
            legacy_contents = fixture[4]
            with self.assertRaisesRegex(ValueError, "SHA-256"):
                self.normalize(
                    fixture,
                    results_sha256="0" * 64,
                )
            self.assertEqual(statistics_path.read_bytes(), legacy_contents)
            self.assertFalse(fixture[2].exists())

    def test_backup_must_be_a_distinct_file(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            fixture = list(self.write_fixture(Path(temp_dir)))
            fixture[2] = fixture[1]
            with self.assertRaisesRegex(ValueError, "backup path must be distinct"):
                self.normalize(tuple(fixture))
            self.assertEqual(fixture[1].read_bytes(), fixture[4])

    def assert_semantic_failure(
        self,
        *,
        mutate_results: Callable[[dict[str, Any]], None] | None = None,
        mutate_statistics: Callable[[dict[str, Any]], None] | None = None,
        message: str,
        target_date: str = "2026-07-25",
    ) -> None:
        results = copy.deepcopy(self.results())
        statistics = copy.deepcopy(self.statistics())
        if mutate_results is not None:
            mutate_results(results)
        if mutate_statistics is not None:
            mutate_statistics(statistics)
        with tempfile.TemporaryDirectory() as temp_dir:
            fixture = self.write_fixture(
                Path(temp_dir),
                results=results,
                statistics=statistics,
            )
            with self.assertRaisesRegex(ValueError, message):
                self.normalize(fixture, target_date=target_date)
            self.assertEqual(fixture[1].read_bytes(), fixture[4])
            self.assertFalse(fixture[2].exists())

    def test_invalid_migration_sample_contract_fails(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            fixture = self.write_fixture(Path(temp_dir))
            with self.assertRaisesRegex(ValueError, "expected_instances"):
                self.normalize(fixture, expected_instances=0)
            with self.assertRaisesRegex(ValueError, "minimum_valid_instances"):
                self.normalize(fixture, minimum_valid_instances=51)
            self.assertEqual(fixture[1].read_bytes(), fixture[4])
            self.assertFalse(fixture[2].exists())

    def test_concurrent_results_change_fails_before_replacement(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            fixture = self.write_fixture(Path(temp_dir))
            results_path, statistics_path, backup_path = fixture[:3]
            original_write_backup = NORMALIZER._write_backup

            def write_backup_then_change_results(
                path: Path,
                contents: bytes,
                mode: int,
            ) -> None:
                original_write_backup(path, contents, mode)
                results_path.write_bytes(fixture[3] + b" ")

            with mock.patch.object(
                NORMALIZER,
                "_write_backup",
                side_effect=write_backup_then_change_results,
            ):
                with self.assertRaisesRegex(
                    ValueError,
                    "results.json: changed concurrently during backup",
                ):
                    self.normalize(fixture)

            self.assertEqual(statistics_path.read_bytes(), fixture[4])
            self.assertEqual(backup_path.read_bytes(), fixture[4])

    def test_invalid_semantics_fail_without_changing_input(self) -> None:
        cases = [
            {
                "mutate_results": lambda value: value.update(state="failed"),
                "message": "state must be completed",
            },
            {
                "mutate_results": lambda value: value.update(total_instances=49),
                "message": "total_instances",
            },
            {
                "mutate_statistics": lambda value: value.update(
                    target_date="2026-07-24"
                ),
                "message": "target_date",
            },
            {
                "mutate_statistics": lambda value: value["daily_history"][0].update(
                    total=49
                ),
                "message": "daily_history target entry",
            },
            {
                "mutate_statistics": lambda value: value["timescales"]["daily"].update(
                    successes=39
                ),
                "message": "timescales.daily",
            },
        ]
        for case in cases:
            with self.subTest(message=case["message"]):
                self.assert_semantic_failure(**case)

    def test_existing_mismatched_or_null_policy_fails(self) -> None:
        for policy in ("count-as-failure", None):
            with self.subTest(policy=policy):
                statistics = self.statistics()
                statistics["non_test_failure_policy"] = policy
                with tempfile.TemporaryDirectory() as temp_dir:
                    fixture = self.write_fixture(
                        Path(temp_dir),
                        statistics=statistics,
                    )
                    with self.assertRaisesRegex(
                        ValueError,
                        "existing non_test_failure_policy",
                    ):
                        self.normalize(fixture)
                    self.assertEqual(fixture[1].read_bytes(), fixture[4])
                    self.assertFalse(fixture[2].exists())


if __name__ == "__main__":
    unittest.main()
