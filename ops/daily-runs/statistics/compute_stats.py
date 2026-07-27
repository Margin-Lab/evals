#!/usr/bin/env python3
"""Compute statistical summaries for margin daily runs from native results.json files."""

from __future__ import annotations

import argparse
import json
import math
import re
from dataclasses import dataclass, field
from datetime import UTC, date, datetime
from pathlib import Path
from typing import Dict, List, Optional, Tuple


TIMESCALES = {
    "daily": 1,
    "weekly": 7,
    "monthly": 30,
}

BASELINE_P0 = 0.56
NON_TEST_FAILURE_POLICIES = ("reject", "count-as-failure", "exclude")


class NonCompletedRunError(ValueError):
    """A structurally identified run that did not complete."""


class NonReusableRunError(ValueError):
    """A structurally valid completed run that is unsafe to aggregate."""


@dataclass
class BenchmarkRun:
    run_id: str
    timestamp: str
    run_date: date
    total: int
    passed: int
    status: Dict
    runtime_ms: Optional[int]
    input_tokens: int
    output_tokens: int
    tool_calls: int
    cost_usd: Optional[float]
    run_dir: Path

    @property
    def accuracy(self) -> float:
        return self.passed / self.total if self.total > 0 else 0.0


@dataclass
class DayStats:
    run_date: date
    total_trials: int = 0
    total_successes: int = 0
    runs: List[BenchmarkRun] = field(default_factory=list)

    @property
    def accuracy(self) -> float:
        return self.total_successes / self.total_trials if self.total_trials > 0 else 0.0


@dataclass
class WindowStats:
    window_size: int
    successes: int
    trials: int
    accuracy: float
    delta: float
    ci_lower: float
    ci_upper: float
    p_value_degradation: float
    p_value_improvement: float
    significance_threshold: float


def _ensure_int(value: object, field_name: str) -> int:
    if isinstance(value, bool):
        raise ValueError(f"{field_name} must be a number, got boolean")
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        if not value.is_integer():
            raise ValueError(f"{field_name} must be an integer, got non-integral float {value}")
        return int(value)
    raise ValueError(f"{field_name} must be an integer, got {type(value).__name__}")


def _ensure_nonnegative_int(value: object, field_name: str) -> int:
    parsed = _ensure_int(value, field_name)
    if parsed < 0:
        raise ValueError(f"{field_name} must be non-negative, got {parsed}")
    return parsed


def _ensure_float_or_none(value: object, field_name: str) -> Optional[float]:
    if value is None:
        return None
    if isinstance(value, bool):
        raise ValueError(f"{field_name} must be a number or null, got boolean")
    if isinstance(value, (int, float)):
        return float(value)
    raise ValueError(f"{field_name} must be a number or null, got {type(value).__name__}")


def _parse_run_timestamp(run_dir_name: str) -> Tuple[str, date]:
    match = re.search(r"(\d{8}-\d{6})", run_dir_name or "")
    if match:
        timestamp = match.group(1)
        run_date = datetime.strptime(timestamp[:8], "%Y%m%d").date()
        return timestamp, run_date
    raise ValueError(f"run directory {run_dir_name!r} is missing a timestamp pattern YYYYMMDD-HHMMSS")


def load_benchmark_run(
    results_json: Path,
    *,
    non_test_failure_policy: str,
    expected_instances: int,
    minimum_valid_instances: int,
) -> BenchmarkRun:
    if non_test_failure_policy not in NON_TEST_FAILURE_POLICIES:
        raise ValueError(
            "non_test_failure_policy must be one of "
            + ", ".join(NON_TEST_FAILURE_POLICIES)
        )
    if expected_instances <= 0:
        raise ValueError("expected_instances must be positive")
    if (
        minimum_valid_instances <= 0
        or minimum_valid_instances > expected_instances
    ):
        raise ValueError(
            "minimum_valid_instances must be positive and no greater than "
            "expected_instances"
        )
    try:
        with open(results_json) as f:
            results = json.load(f)
    except (json.JSONDecodeError, OSError):
        raise ValueError(f"Failed to load JSON from {results_json}")
    if not isinstance(results, dict):
        raise ValueError(f"results.json must be an object in {results_json}")

    state = results.get("state")
    if not isinstance(state, str):
        raise ValueError("missing required string field state")
    if state not in ("completed", "failed"):
        raise NonCompletedRunError(f"run state is {state!r}, not 'completed'")
    if state == "failed" and non_test_failure_policy != "exclude":
        raise NonCompletedRunError(
            "run state is 'failed'; only the exclude policy can reuse an "
            "infrastructure-failed run"
        )

    if "total_instances" not in results:
        raise ValueError("missing required field total_instances")
    total_instances = _ensure_nonnegative_int(
        results.get("total_instances"),
        "total_instances",
    )

    run_id = results.get("run_id")
    if run_id is None:
        raise ValueError("missing required field run_id")
    run_id_str = str(run_id)
    timestamp, run_date = _parse_run_timestamp(results_json.parent.name)

    status = results.get("status")
    if not isinstance(status, dict):
        raise ValueError("missing required object status")

    succeeded = status.get("succeeded")
    if not isinstance(succeeded, dict) or "count" not in succeeded:
        raise ValueError("missing required field status.succeeded.count")
    passed = _ensure_nonnegative_int(
        succeeded.get("count"),
        "status.succeeded.count",
    )

    test_failed = status.get("test_failed")
    if not isinstance(test_failed, dict) or "count" not in test_failed:
        raise ValueError("missing required field status.test_failed.count")
    failed_tests = _ensure_nonnegative_int(
        test_failed.get("count"),
        "status.test_failed.count",
    )

    infra_failed = status.get("infra_failed")
    if not isinstance(infra_failed, dict) or "count" not in infra_failed:
        raise ValueError("missing required field status.infra_failed.count")
    failed_infra = _ensure_nonnegative_int(
        infra_failed.get("count"),
        "status.infra_failed.count",
    )

    canceled = status.get("canceled")
    if not isinstance(canceled, dict) or "count" not in canceled:
        raise ValueError("missing required field status.canceled.count")
    canceled_count = _ensure_nonnegative_int(
        canceled.get("count"),
        "status.canceled.count",
    )

    if state == "failed" and (failed_infra == 0 or canceled_count > 0):
        raise NonCompletedRunError(
            "run state is 'failed' without an eligible infrastructure-only "
            "failure "
            f"(infra_failed={failed_infra}, canceled={canceled_count})"
        )

    total = passed + failed_tests
    if non_test_failure_policy == "count-as-failure":
        total += failed_infra + canceled_count

    usage = results.get("usage")
    if not isinstance(usage, dict):
        raise ValueError("missing required object usage")
    if "input_tokens" not in usage:
        raise ValueError("missing required field usage.input_tokens")
    if "output_tokens" not in usage:
        raise ValueError("missing required field usage.output_tokens")
    if "tool_calls" not in usage:
        raise ValueError("missing required field usage.tool_calls")
    input_tokens = _ensure_int(usage.get("input_tokens"), "usage.input_tokens")
    output_tokens = _ensure_int(usage.get("output_tokens"), "usage.output_tokens")
    tool_calls = _ensure_int(usage.get("tool_calls"), "usage.tool_calls")
    cost_usd = _ensure_float_or_none(usage.get("cost_usd"), "usage.cost_usd")

    runtime = results.get("runtime")
    if not isinstance(runtime, dict):
        raise ValueError("missing required object runtime")
    if "run_ms" not in runtime:
        raise ValueError("missing required field runtime.run_ms")
    runtime_ms = _ensure_int(runtime.get("run_ms"), "runtime.run_ms")

    status_total = passed + failed_tests + failed_infra + canceled_count
    if total_instances != expected_instances:
        raise NonReusableRunError(
            f"total_instances must be {expected_instances}; found {total_instances}"
        )
    if status_total != total_instances:
        raise NonReusableRunError(
            f"status counts total {status_total}; expected {total_instances}"
        )
    if non_test_failure_policy == "reject" and (failed_infra or canceled_count):
        raise NonReusableRunError(
            "non-test failures are not allowed by the reject policy "
            f"(infra_failed={failed_infra}, canceled={canceled_count})"
        )
    if total < minimum_valid_instances:
        raise NonReusableRunError(
            f"policy denominator is {total}; at least "
            f"{minimum_valid_instances} valid instances are required"
        )

    return BenchmarkRun(
        run_id=run_id_str,
        timestamp=timestamp,
        run_date=run_date,
        total=total,
        passed=passed,
        status=status,
        runtime_ms=runtime_ms,
        input_tokens=input_tokens,
        output_tokens=output_tokens,
        tool_calls=tool_calls,
        cost_usd=cost_usd,
        run_dir=results_json.parent,
    )


def _iter_run_results(base_dir: Path, recursive: bool) -> List[Path]:
    if recursive:
        return sorted(base_dir.glob("**/results.json"))

    results: List[Path] = []
    if not base_dir.exists():
        return results

    direct_results = base_dir / "results.json"
    if direct_results.exists():
        return [direct_results]

    for item in sorted(base_dir.iterdir(), key=lambda p: p.name):
        if item.is_dir():
            candidate = item / "results.json"
            if candidate.exists():
                results.append(candidate)
    return results


def load_all_runs(
    base_dir: Path,
    *,
    recursive: bool,
    non_test_failure_policy: str,
    expected_instances: int,
    minimum_valid_instances: int,
    required_results_json: Optional[Path] = None,
) -> List[BenchmarkRun]:
    runs: List[BenchmarkRun] = []
    required_path = (
        required_results_json.resolve()
        if required_results_json is not None
        else None
    )
    for results_json in _iter_run_results(base_dir, recursive):
        try:
            run = load_benchmark_run(
                results_json,
                non_test_failure_policy=non_test_failure_policy,
                expected_instances=expected_instances,
                minimum_valid_instances=minimum_valid_instances,
            )
        except (NonCompletedRunError, NonReusableRunError) as exc:
            if required_path is not None and results_json.resolve() == required_path:
                raise ValueError(
                    f"required current run is not reusable: {exc}"
                ) from exc
            # Margin may persist a partial result before returning nonzero. It
            # is useful for diagnosis but must never enter accuracy statistics.
            # A failed run is eligible only for the explicitly bounded
            # infrastructure-failure exception above. Likewise, a prior run
            # that fails today's sample contract cannot poison a retry.
            continue
        except ValueError as exc:
            raise ValueError(f"{results_json}: {exc}") from exc
        runs.append(run)

    return sorted(runs, key=lambda run: (run.run_date, run.timestamp))


def load_runs_from_directories(
    base_dirs: List[Path],
    *,
    recursive: bool = False,
    non_test_failure_policy: str,
    expected_instances: int,
    minimum_valid_instances: int,
    required_results_json: Optional[Path] = None,
) -> List[BenchmarkRun]:
    runs: List[BenchmarkRun] = []
    runs_by_id: Dict[str, Path] = {}
    required_path = (
        required_results_json.resolve()
        if required_results_json is not None
        else None
    )
    required_path_was_scanned = False

    if required_results_json is not None and not required_results_json.is_file():
        raise ValueError(
            f"required current run results not found: {required_results_json}"
        )

    for base_dir in base_dirs:
        if not base_dir.is_dir():
            raise ValueError(f"runs directory not found: {base_dir}")

        result_files = _iter_run_results(base_dir, recursive)
        if not result_files:
            raise ValueError(
                f"no results.json files found in runs directory: {base_dir}"
            )
        if required_path is not None and any(
            path.resolve() == required_path for path in result_files
        ):
            required_path_was_scanned = True

        directory_runs = load_all_runs(
            base_dir,
            recursive=recursive,
            non_test_failure_policy=non_test_failure_policy,
            expected_instances=expected_instances,
            minimum_valid_instances=minimum_valid_instances,
            required_results_json=required_results_json,
        )

        for run in directory_runs:
            previous_dir = runs_by_id.get(run.run_id)
            if previous_dir is not None:
                raise ValueError(
                    f"duplicate run_id {run.run_id!r} found in {previous_dir} and {run.run_dir}"
                )
            runs_by_id[run.run_id] = run.run_dir
            runs.append(run)

    if required_path is not None and not required_path_was_scanned:
        raise ValueError(
            "required current run results are outside the configured runs "
            f"directories: {required_results_json}"
        )
    if not runs:
        raise ValueError(
            "no completed results.json files found in configured runs directories"
        )

    return sorted(runs, key=lambda run: (run.run_date, run.timestamp))


def aggregate_by_day(runs: List[BenchmarkRun]) -> Dict[date, DayStats]:
    day_stats: Dict[date, DayStats] = {}
    for run in runs:
        day = day_stats.setdefault(run.run_date, DayStats(run_date=run.run_date))
        day.total_trials += run.total
        day.total_successes += run.passed
        day.runs.append(run)
    return day_stats


def _log_factorial_binomial(n: int, k: int) -> float:
    return math.lgamma(n + 1) - math.lgamma(k + 1) - math.lgamma(n - k + 1)


def _log_binomial_pmf(n: int, k: int, p: float) -> float:
    if k < 0 or k > n:
        return float("-inf")
    if p <= 0:
        return 0.0 if k == 0 else float("-inf")
    if p >= 1:
        return 0.0 if k == n else float("-inf")

    return _log_factorial_binomial(n, k) + (k * math.log(p)) + ((n - k) * math.log1p(-p))


def _logsumexp(values: List[float]) -> float:
    finite_values = [value for value in values if math.isfinite(value)]
    if not finite_values:
        return float("-inf")

    max_value = max(finite_values)
    total = sum(math.exp(value - max_value) for value in finite_values)
    return max_value + math.log(total)


def _binomial_cdf(n: int, k: int, p: float) -> float:
    if k < 0:
        return 0.0
    if k >= n:
        return 1.0
    if p <= 0:
        return 1.0 if k >= 0 else 0.0
    if p >= 1:
        return 0.0 if k < n else 1.0

    log_cdf = _logsumexp([_log_binomial_pmf(n, i, p) for i in range(0, k + 1)])
    return max(0.0, min(1.0, math.exp(log_cdf)))


def _binomial_sf(n: int, k: int, p: float) -> float:
    if k <= 0:
        return 1.0
    if k > n:
        return 0.0
    if p <= 0:
        return 0.0
    if p >= 1:
        return 1.0

    log_sf = _logsumexp([_log_binomial_pmf(n, i, p) for i in range(k, n + 1)])
    return max(0.0, min(1.0, math.exp(log_sf)))


def exact_binomial_test(k: int, n: int, p0: float, alternative: str) -> float:
    if n == 0:
        return 1.0

    alternative = alternative.lower()
    if alternative == "less":
        return _binomial_cdf(n, k, p0)
    if alternative == "greater":
        return _binomial_sf(n, k, p0)
    cdf = _binomial_cdf(n, k, p0)
    sf = 1.0 - _binomial_cdf(n, k - 1, p0)
    return min(1.0, 2.0 * min(cdf, sf))


def wilson_confidence_interval(k: int, n: int, confidence: float = 0.95) -> Tuple[float, float]:
    if n == 0:
        return 0.0, 1.0

    p_hat = k / n
    z = 1.959963984540054
    if confidence != 0.95:
        # Conservative fallback for non-0.95 inputs.
        from statistics import NormalDist

        z = abs(NormalDist().inv_cdf((1 + confidence) / 2))

    denominator = 1 + z**2 / n
    center = (p_hat + z**2 / (2 * n)) / denominator
    margin = z * math.sqrt(p_hat * (1 - p_hat) / n + z**2 / (4 * n**2)) / denominator

    return max(0.0, center - margin), min(1.0, center + margin)


def compute_significance_threshold(n: int, p0: float, alpha: float = 0.05) -> float:
    if n == 0:
        return 0.10

    degradation_target = int(n * p0)
    k_degrad = None
    for k in range(degradation_target, -1, -1):
        if exact_binomial_test(k, n, p0, "less") < alpha:
            k_degrad = k
            break

    k_improv = None
    for k in range(degradation_target + 1, n + 1):
        if exact_binomial_test(k, n, p0, "greater") < alpha:
            k_improv = k
            break

    delta_degrad = abs(p0 - (k_degrad / n)) if k_degrad is not None else 0.10
    delta_improv = abs((k_improv / n) - p0) if k_improv is not None else 0.10
    return min((delta_degrad + delta_improv) / 2, 0.15)


def compute_window_stats(
    day_stats: Dict[date, DayStats],
    window_size: int,
    target_date: date,
    baseline_p0: float = BASELINE_P0,
) -> Optional[WindowStats]:
    successes = 0
    trials = 0

    for delta_days in range(window_size):
        date_in_window = date.fromordinal(target_date.toordinal() - delta_days)
        found = day_stats.get(date_in_window)
        if found is not None:
            successes += found.total_successes
            trials += found.total_trials

    if trials == 0:
        return None

    accuracy = successes / trials
    delta = accuracy - baseline_p0
    ci_lower, ci_upper = wilson_confidence_interval(successes, trials)
    p_value_degradation = exact_binomial_test(successes, trials, baseline_p0, "less")
    p_value_improvement = exact_binomial_test(successes, trials, baseline_p0, "greater")
    significance_threshold = compute_significance_threshold(trials, baseline_p0)

    return WindowStats(
        window_size=window_size,
        successes=successes,
        trials=trials,
        accuracy=accuracy,
        delta=delta,
        ci_lower=ci_lower,
        ci_upper=ci_upper,
        p_value_degradation=p_value_degradation,
        p_value_improvement=p_value_improvement,
        significance_threshold=significance_threshold,
    )


def compute_dashboard_stats(
    runs: List[BenchmarkRun],
    target_date: date,
    baseline_p0: float = BASELINE_P0,
) -> dict:
    eligible_runs = [run for run in runs if run.run_date <= target_date]
    day_stats = aggregate_by_day(eligible_runs)

    timescales_output = {}
    for name, window_size in TIMESCALES.items():
        stats = compute_window_stats(day_stats, window_size, target_date, baseline_p0)
        if stats:
            timescales_output[name] = {
                "window_size": stats.window_size,
                "successes": stats.successes,
                "trials": stats.trials,
                "accuracy": round(stats.accuracy, 4),
                "delta": round(stats.delta, 4),
                "ci_lower": round(stats.ci_lower, 4),
                "ci_upper": round(stats.ci_upper, 4),
                "p_value_degradation": round(stats.p_value_degradation, 4),
                "p_value_improvement": round(stats.p_value_improvement, 4),
                "significance_threshold": round(stats.significance_threshold, 4),
            }
        else:
            timescales_output[name] = None

    sorted_dates = sorted(day_stats.keys(), reverse=True)[:30]
    daily_history = []
    for d in reversed(sorted_dates):
        ds = day_stats[d]
        daily_history.append(
            {
                "date": d.isoformat(),
                "passed": ds.total_successes,
                "total": ds.total_trials,
                "accuracy": round(ds.accuracy, 4),
            }
        )

    return {
        "computed_at": datetime.now(UTC).replace(tzinfo=None).isoformat() + "Z",
        "target_date": target_date.isoformat(),
        "baseline_p0": baseline_p0,
        "total_runs_analyzed": len(eligible_runs),
        "timescales": timescales_output,
        "daily_history": daily_history,
    }


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Compute statistics for margin daily runs using results.json files."
    )
    parser.add_argument(
        "--runs_dir",
        dest="runs_dirs",
        type=Path,
        action="append",
        default=None,
        help="Directory containing run outputs. Repeat to combine explicit model series (default: runs).",
    )
    parser.add_argument(
        "--output",
        type=Path,
        required=True,
        help="Where to write the statistics JSON.",
    )
    parser.add_argument(
        "--baseline",
        type=float,
        default=BASELINE_P0,
        help=f"Baseline probability P0 for statistical tests (default: {BASELINE_P0})",
    )
    parser.add_argument(
        "--recursive",
        action="store_true",
        help="Search results.json files recursively (includes nested model run folders).",
    )
    parser.add_argument(
        "--date",
        type=str,
        default=None,
        help="Target date for statistics in YYYY-MM-DD format (default: today).",
    )
    parser.add_argument(
        "--non-test-failure-policy",
        choices=NON_TEST_FAILURE_POLICIES,
        required=True,
        help=(
            "How infra_failed and canceled instances affect accuracy: reject the "
            "run, count them as failures, or exclude them. test_failed instances "
            "always remain in the denominator."
        ),
    )
    parser.add_argument(
        "--expected-instances",
        type=int,
        required=True,
        help="Required total_instances and status-count total for an eligible run.",
    )
    parser.add_argument(
        "--minimum-valid-instances",
        type=int,
        required=True,
        help="Minimum policy denominator for an eligible run.",
    )
    parser.add_argument(
        "--required-results",
        type=Path,
        required=True,
        help=(
            "results.json for the current run. Unlike prior non-reusable runs, "
            "this run must satisfy the aggregation contract."
        ),
    )
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

    if args.date is None:
        target_date = date.today()
    else:
        target_date = datetime.strptime(args.date, "%Y-%m-%d").date()

    runs_dirs = args.runs_dirs or [Path("runs")]

    try:
        runs = load_runs_from_directories(
            runs_dirs,
            recursive=args.recursive,
            non_test_failure_policy=args.non_test_failure_policy,
            expected_instances=args.expected_instances,
            minimum_valid_instances=args.minimum_valid_instances,
            required_results_json=args.required_results,
        )
    except ValueError as exc:
        raise SystemExit(f"Error: {exc}")

    if not runs:
        output = {
            "computed_at": datetime.now(UTC).replace(tzinfo=None).isoformat() + "Z",
            "target_date": target_date.isoformat(),
            "baseline_p0": args.baseline,
            "total_runs_analyzed": 0,
            "timescales": {name: None for name in TIMESCALES},
            "daily_history": [],
        }
    else:
        output = compute_dashboard_stats(runs, target_date, baseline_p0=args.baseline)
    output["non_test_failure_policy"] = args.non_test_failure_policy

    args.output.parent.mkdir(parents=True, exist_ok=True)
    with open(args.output, "w") as f:
        json.dump(output, f, indent=2)

    if output["timescales"].get("daily"):
        daily = output["timescales"]["daily"]
        print(f"\nDaily statistics ({target_date}):")
        print(f"  Accuracy: {daily['accuracy']:.2%} ({daily['successes']}/{daily['trials']})")
        print(f"  Delta from baseline: {daily['delta']:+.2%}")
        print(f"  95% CI: [{daily['ci_lower']:.2%}, {daily['ci_upper']:.2%}]")
        print(f"  p-value (degradation): {daily['p_value_degradation']:.4f}")
        print(f"  p-value (improvement): {daily['p_value_improvement']:.4f}")


if __name__ == "__main__":
    main()
