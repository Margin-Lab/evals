#!/usr/bin/env python3
"""Validate whether a daily run is safe to reuse and sync."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


POLICIES = ("reject", "count-as-failure", "exclude")


def _load_object(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"{path}: invalid JSON: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"{path}: expected a JSON object")
    return value


def validate_reusable_run(
    results_path: Path,
    statistics_path: Path,
    *,
    target_date: str,
    expected_instances: int,
    minimum_valid_instances: int,
    policy: str,
) -> None:
    results = _load_object(results_path)
    statistics = _load_object(statistics_path)

    if results.get("state") != "completed":
        raise ValueError(
            f"{results_path}: state must be completed; found {results.get('state')!r}"
        )
    if results.get("total_instances") != expected_instances:
        raise ValueError(
            f"{results_path}: total_instances must be {expected_instances}; "
            f"found {results.get('total_instances')!r}"
        )
    status = results.get("status")
    if not isinstance(status, dict):
        raise ValueError(f"{results_path}: status must be an object")
    counts: dict[str, int] = {}
    for name in ("succeeded", "test_failed", "infra_failed", "canceled"):
        bucket = status.get(name)
        count = bucket.get("count") if isinstance(bucket, dict) else None
        if not isinstance(count, int) or isinstance(count, bool) or count < 0:
            raise ValueError(
                f"{results_path}: status.{name}.count must be a non-negative integer"
            )
        counts[name] = count
    if sum(counts.values()) != expected_instances:
        raise ValueError(
            f"{results_path}: status counts must total {expected_instances}"
        )
    if policy == "reject" and (counts["infra_failed"] or counts["canceled"]):
        raise ValueError(
            f"{results_path}: reject policy does not allow infra_failed or canceled"
        )
    valid_instances = counts["succeeded"] + counts["test_failed"]
    denominator = (
        valid_instances
        if policy == "exclude"
        else valid_instances + counts["infra_failed"] + counts["canceled"]
    )
    if denominator < minimum_valid_instances:
        raise ValueError(
            f"{results_path}: policy denominator is {denominator}; "
            f"at least {minimum_valid_instances} valid instances are required"
        )
    if statistics.get("target_date") != target_date:
        raise ValueError(
            f"{statistics_path}: target_date must be {target_date}; "
            f"found {statistics.get('target_date')!r}"
        )
    if statistics.get("non_test_failure_policy") != policy:
        raise ValueError(
            f"{statistics_path}: non_test_failure_policy must be {policy}; "
            f"found {statistics.get('non_test_failure_policy')!r}"
        )


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Validate a paired daily result/statistics run before reuse."
    )
    parser.add_argument("--results", type=Path, required=True)
    parser.add_argument("--statistics", type=Path, required=True)
    parser.add_argument("--target-date", required=True)
    parser.add_argument("--expected-instances", type=int, required=True)
    parser.add_argument("--minimum-valid-instances", type=int, required=True)
    parser.add_argument("--policy", choices=POLICIES, required=True)
    args = parser.parse_args()

    if args.expected_instances <= 0:
        raise SystemExit("--expected-instances must be positive")
    if (
        args.minimum_valid_instances <= 0
        or args.minimum_valid_instances > args.expected_instances
    ):
        raise SystemExit(
            "--minimum-valid-instances must be positive and no greater than "
            "--expected-instances"
        )
    try:
        validate_reusable_run(
            args.results,
            args.statistics,
            target_date=args.target_date,
            expected_instances=args.expected_instances,
            minimum_valid_instances=args.minimum_valid_instances,
            policy=args.policy,
        )
    except ValueError as exc:
        raise SystemExit(f"Not reusable: {exc}") from exc


if __name__ == "__main__":
    main()
