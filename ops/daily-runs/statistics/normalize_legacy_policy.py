#!/usr/bin/env python3
"""Safely annotate a hash-pinned legacy statistics file with the exclude policy."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import re
import stat
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from validate_reusable_run import validate_reusable_run


POLICY = "exclude"
SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")


@dataclass(frozen=True)
class Migration:
    run_directory: str
    target_date: str
    expected_instances: int
    minimum_valid_instances: int
    results_sha256: str
    legacy_statistics_sha256: str
    normalized_statistics_sha256: str


MIGRATIONS = {
    "codex-20260725-040001": Migration(
        run_directory="20260725-040001",
        target_date="2026-07-25",
        expected_instances=50,
        minimum_valid_instances=50,
        results_sha256=(
            "69a0d7d456366bf910471d8ae52421736738ac5558f96f6c5b4c3fdbebd0384f"
        ),
        legacy_statistics_sha256=(
            "1d905281e15358342887a7e234e9fad2c68bc3cf7add7cd09aa5109cd008eafb"
        ),
        normalized_statistics_sha256=(
            "5ab0ad56e84aea827a0b8070266b8471cd82f014642196ee620186506d563762"
        ),
    ),
    "claude-code-20260725-045635": Migration(
        run_directory="20260725-045635",
        target_date="2026-07-25",
        expected_instances=50,
        minimum_valid_instances=50,
        results_sha256=(
            "8ad5aaf89c10dde5e8d1da366ef5930949d66fa2e930c2d6e6acac32cda11a62"
        ),
        legacy_statistics_sha256=(
            "e472a0074171bbb63e2647d2eea7608d99008ebd41281252bfdfd2dccf109d02"
        ),
        normalized_statistics_sha256=(
            "6d08c25b51a04a632c9c8540a5ab1d7b7c64247424ae1d0b7b0ace83fc3fc3ee"
        ),
    ),
}


def _sha256(contents: bytes) -> str:
    return hashlib.sha256(contents).hexdigest()


def _require_sha256(value: str, option: str) -> str:
    normalized = value.lower()
    if not SHA256_PATTERN.fullmatch(normalized):
        raise ValueError(f"{option} must be a 64-character SHA-256 digest")
    return normalized


def _load_object(contents: bytes, path: Path) -> dict[str, Any]:
    try:
        value = json.loads(contents)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"{path}: invalid JSON: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{path}: expected a JSON object")
    return value


def _require_nonnegative_int(value: Any, context: str) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
        raise ValueError(f"{context} must be a non-negative integer")
    return value


def _require_ratio(
    value: Any,
    *,
    successes: int,
    trials: int,
    context: str,
) -> None:
    if not isinstance(value, (int, float)) or isinstance(value, bool):
        raise ValueError(f"{context} must be a finite number")
    number = float(value)
    if not math.isfinite(number):
        raise ValueError(f"{context} must be a finite number")
    expected = successes / trials if trials else 0.0
    if not math.isclose(number, expected, rel_tol=0.0, abs_tol=0.0001):
        raise ValueError(
            f"{context} must equal {successes}/{trials} within four-decimal precision"
        )


def _validate_daily_statistics(
    results: dict[str, Any],
    statistics: dict[str, Any],
    *,
    target_date: str,
) -> None:
    status = results.get("status")
    if not isinstance(status, dict):
        raise ValueError("results.status must be an object")
    succeeded = status.get("succeeded")
    test_failed = status.get("test_failed")
    if not isinstance(succeeded, dict) or not isinstance(test_failed, dict):
        raise ValueError(
            "results.status.succeeded and results.status.test_failed must be objects"
        )
    passed = _require_nonnegative_int(
        succeeded.get("count"),
        "results.status.succeeded.count",
    )
    failed_tests = _require_nonnegative_int(
        test_failed.get("count"),
        "results.status.test_failed.count",
    )
    denominator = passed + failed_tests

    daily_history = statistics.get("daily_history")
    if not isinstance(daily_history, list):
        raise ValueError("statistics.daily_history must be an array")
    target_entries = [
        entry
        for entry in daily_history
        if isinstance(entry, dict) and entry.get("date") == target_date
    ]
    if len(target_entries) != 1:
        raise ValueError(
            "statistics.daily_history must contain exactly one entry for "
            f"{target_date}; found {len(target_entries)}"
        )
    daily_entry = target_entries[0]
    if daily_entry.get("passed") != passed or daily_entry.get("total") != denominator:
        raise ValueError(
            "statistics.daily_history target entry must use "
            f"{passed}/{denominator} under the {POLICY} policy"
        )
    _require_ratio(
        daily_entry.get("accuracy"),
        successes=passed,
        trials=denominator,
        context="statistics.daily_history target accuracy",
    )

    timescales = statistics.get("timescales")
    daily_timescale = timescales.get("daily") if isinstance(timescales, dict) else None
    if not isinstance(daily_timescale, dict):
        raise ValueError("statistics.timescales.daily must be an object")
    if (
        daily_timescale.get("successes") != passed
        or daily_timescale.get("trials") != denominator
    ):
        raise ValueError(
            "statistics.timescales.daily must use "
            f"{passed}/{denominator} under the {POLICY} policy"
        )
    _require_ratio(
        daily_timescale.get("accuracy"),
        successes=passed,
        trials=denominator,
        context="statistics.timescales.daily.accuracy",
    )


def _normalized_contents(statistics: dict[str, Any]) -> bytes:
    normalized: dict[str, Any] = {}
    inserted = False
    for key, value in statistics.items():
        normalized[key] = value
        if key == "target_date":
            normalized["non_test_failure_policy"] = POLICY
            inserted = True
    if not inserted:
        raise ValueError("statistics.target_date is required")
    return (json.dumps(normalized, indent=2) + "\n").encode()


def _write_backup(path: Path, contents: bytes, mode: int) -> None:
    if path.exists():
        if not path.is_file() or path.read_bytes() != contents:
            raise ValueError(f"backup already exists with different contents: {path}")
        return
    if not path.parent.is_dir():
        raise ValueError(f"backup parent directory does not exist: {path.parent}")
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(contents)
            output.flush()
            os.fsync(output.fileno())
    except BaseException:
        path.unlink(missing_ok=True)
        raise


def _fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _migration(migration_id: str) -> Migration:
    migration = MIGRATIONS.get(migration_id)
    if migration is None:
        raise ValueError(
            f"unsupported migration {migration_id!r}; expected one of "
            + ", ".join(sorted(MIGRATIONS))
        )
    if migration.expected_instances <= 0:
        raise ValueError("migration expected_instances must be positive")
    if (
        migration.minimum_valid_instances <= 0
        or migration.minimum_valid_instances > migration.expected_instances
    ):
        raise ValueError(
            "migration minimum_valid_instances must be positive and no greater "
            "than expected_instances"
        )
    for value, context in (
        (migration.results_sha256, "migration results_sha256"),
        (
            migration.legacy_statistics_sha256,
            "migration legacy_statistics_sha256",
        ),
        (
            migration.normalized_statistics_sha256,
            "migration normalized_statistics_sha256",
        ),
    ):
        _require_sha256(value, context)
    return migration


def normalize_legacy_policy(
    results_path: Path,
    statistics_path: Path,
    backup_path: Path,
    *,
    migration_id: str,
) -> bool:
    """Normalize a verified legacy file; return True only when it was changed."""

    migration = _migration(migration_id)
    for input_path, context in (
        (results_path, "results"),
        (statistics_path, "statistics"),
    ):
        if input_path.is_symlink() or not input_path.is_file():
            raise ValueError(f"{context} input must be a regular file: {input_path}")
    if backup_path.is_symlink():
        raise ValueError(f"backup must not be a symbolic link: {backup_path}")
    resolved_results = results_path.resolve()
    resolved_statistics = statistics_path.resolve()
    resolved_backup = backup_path.resolve(strict=False)
    if resolved_results.parent != resolved_statistics.parent:
        raise ValueError("results and statistics must belong to the same run directory")
    if resolved_results.parent.name != migration.run_directory:
        raise ValueError(
            f"{migration_id}: run directory must be {migration.run_directory}; "
            f"found {resolved_results.parent.name}"
        )
    if resolved_backup in (
        resolved_results,
        resolved_statistics,
    ):
        raise ValueError("backup path must be distinct from both input files")

    try:
        results_contents = results_path.read_bytes()
        statistics_contents = statistics_path.read_bytes()
    except OSError as exc:
        raise ValueError(f"unable to read migration input: {exc}") from exc
    if _sha256(results_contents) != migration.results_sha256:
        raise ValueError(f"{results_path}: SHA-256 does not match the expected input")

    results = _load_object(results_contents, results_path)
    statistics = _load_object(statistics_contents, statistics_path)
    existing_policy = statistics.get("non_test_failure_policy")
    has_policy = "non_test_failure_policy" in statistics

    if has_policy:
        if existing_policy != POLICY:
            raise ValueError(
                f"{statistics_path}: existing non_test_failure_policy must be "
                f"{POLICY}; found {existing_policy!r}"
            )
        if _sha256(statistics_contents) != migration.normalized_statistics_sha256:
            raise ValueError(
                f"{statistics_path}: normalized SHA-256 does not match the expected input"
            )
        if not backup_path.is_file():
            raise ValueError(
                f"normalized file requires its verified legacy backup: {backup_path}"
            )
        backup_contents = backup_path.read_bytes()
        if _sha256(backup_contents) != migration.legacy_statistics_sha256:
            raise ValueError(f"{backup_path}: SHA-256 does not match the legacy input")
        validate_reusable_run(
            results_path,
            statistics_path,
            target_date=migration.target_date,
            expected_instances=migration.expected_instances,
            minimum_valid_instances=migration.minimum_valid_instances,
            policy=POLICY,
        )
        _validate_daily_statistics(
            results,
            statistics,
            target_date=migration.target_date,
        )
        return False

    if _sha256(statistics_contents) != migration.legacy_statistics_sha256:
        raise ValueError(
            f"{statistics_path}: legacy SHA-256 does not match the expected input"
        )
    if statistics.get("target_date") != migration.target_date:
        raise ValueError(
            f"{statistics_path}: target_date must be {migration.target_date}; "
            f"found {statistics.get('target_date')!r}"
        )

    normalized_contents = _normalized_contents(statistics)
    if _sha256(normalized_contents) != migration.normalized_statistics_sha256:
        raise ValueError(
            f"{statistics_path}: generated normalized SHA-256 does not match "
            "the expected output"
        )
    normalized_statistics = _load_object(normalized_contents, statistics_path)
    _validate_daily_statistics(
        results,
        normalized_statistics,
        target_date=migration.target_date,
    )

    source_mode = stat.S_IMODE(statistics_path.stat().st_mode)
    temporary_name: str | None = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="wb",
            prefix=f".{statistics_path.name}.",
            suffix=".tmp",
            dir=statistics_path.parent,
            delete=False,
        ) as output:
            temporary_name = output.name
            output.write(normalized_contents)
            os.fchmod(output.fileno(), source_mode)
            output.flush()
            os.fsync(output.fileno())
        temporary_path = Path(temporary_name)
        validate_reusable_run(
            results_path,
            temporary_path,
            target_date=migration.target_date,
            expected_instances=migration.expected_instances,
            minimum_valid_instances=migration.minimum_valid_instances,
            policy=POLICY,
        )
        if results_path.read_bytes() != results_contents:
            raise ValueError(f"{results_path}: changed concurrently during normalization")
        if statistics_path.read_bytes() != statistics_contents:
            raise ValueError(
                f"{statistics_path}: changed concurrently during normalization"
            )
        _write_backup(backup_path, statistics_contents, source_mode)
        _fsync_directory(backup_path.parent)
        if backup_path.read_bytes() != statistics_contents:
            raise ValueError(f"{backup_path}: backup verification failed")
        if results_path.read_bytes() != results_contents:
            raise ValueError(f"{results_path}: changed concurrently during backup")
        if statistics_path.read_bytes() != statistics_contents:
            raise ValueError(
                f"{statistics_path}: changed concurrently during backup"
            )
        os.replace(temporary_path, statistics_path)
        temporary_name = None
        _fsync_directory(statistics_path.parent)
    finally:
        if temporary_name is not None:
            Path(temporary_name).unlink(missing_ok=True)

    return True


def main() -> None:
    parser = argparse.ArgumentParser(
        description=(
            "Hash-pin, validate, back up, and annotate a legacy statistics.json "
            "with the historically equivalent exclude policy."
        )
    )
    parser.add_argument("--results", type=Path, required=True)
    parser.add_argument("--statistics", type=Path, required=True)
    parser.add_argument("--backup", type=Path, required=True)
    parser.add_argument(
        "--migration",
        choices=sorted(MIGRATIONS),
        required=True,
        help="Reviewed legacy artifact pair to normalize.",
    )
    args = parser.parse_args()

    try:
        changed = normalize_legacy_policy(
            args.results,
            args.statistics,
            args.backup,
            migration_id=args.migration,
        )
    except ValueError as exc:
        raise SystemExit(f"Legacy statistics normalization failed: {exc}") from exc

    status = "Normalized" if changed else "Already normalized"
    print(f"{status}: {args.statistics}")


if __name__ == "__main__":
    main()
