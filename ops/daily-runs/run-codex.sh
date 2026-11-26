#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/paths.sh"
source "${SCRIPT_DIR}/lib/policy.sh"
ROOT_DIR="$(marginlab_eval_workspace_root "${SCRIPT_DIR}")"
DAILY_STATE_DIR="${MARGINLAB_DAILY_STATE_DIR:-${ROOT_DIR}/daily-runs}"
SWE_SUITES_ROOT="${MARGINLAB_SWE_SUITES_ROOT:-${ROOT_DIR}/swe-suites}"
PROJECTS_ROOT="${MARGINLAB_PROJECTS_ROOT:-$(cd "${ROOT_DIR}/.." && pwd)}"
RAW_REPOSITORY="${MARGINLAB_RAW_REPOSITORY:-${PROJECTS_ROOT}/marginlab}"
MODEL_SERIES="gpt-6-1-sol-high"
RUN_TIMESTAMP="${RUN_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"
CODEX_RUNS_DIR="${DAILY_STATE_DIR}/runs/codex"
TARGET_DIR="${CODEX_RUNS_DIR}/${MODEL_SERIES}/${RUN_TIMESTAMP}"
RUNS_DIR="$(dirname "${TARGET_DIR}")"
RUN_DATE="${TARGET_DIR##*/}"
RUN_DATE_FORMATTED="${RUN_DATE:0:4}-${RUN_DATE:4:2}-${RUN_DATE:6:2}"
# GPT-6.1 Sol/high's baseline: the pooled pass rate of its daily runs from
# 2026-11-08 to 2026-11-26 (540 of 889 graded tasks, 18 runs). It must match
# CODEX_BASELINE_WINDOW in the site's hosting/src/lib/tracker-mode.ts; the site
# build stops if they differ.
BASELINE_P0="${CODEX_BASELINE_P0:-0.6074}"
marginlab_load_daily_run_policy
SYNC_DESTINATION="${RAW_REPOSITORY}/data/benchmarks/degradation_trackers/codex/swe-bench-pro/data"
BASELINE_SERIES=(
  "${MODEL_SERIES}"
  "gpt-5-6-sol-high"
  "gpt-5-6-sol-xhigh"
)

validate_reusable_run() {
  local run_root="$1"
  python3 "${SCRIPT_DIR}/statistics/validate_reusable_run.py" \
    --results "${run_root}/results.json" \
    --statistics "${run_root}/statistics.json" \
    --target-date "${RUN_DATE_FORMATTED}" \
    --expected-instances "${EXPECTED_INSTANCES}" \
    --minimum-valid-instances "${MINIMUM_VALID_INSTANCES}" \
    --policy "${NON_TEST_FAILURE_POLICY}"
}

sync_run() {
  "${SCRIPT_DIR}/sync-run.sh" \
    --source-run "$1" \
    --destination-root "${SYNC_DESTINATION}"
}

# Keep the daily tracker at one completed sample per model series and date.
# Set FORCE_RUN=1 only when an intentional same-day rerun is required.
if [ "${FORCE_RUN:-0}" != "1" ]; then
  completed_runs=()
  for existing_run in "${RUNS_DIR}/${RUN_DATE:0:8}-"*; do
    if [ -f "${existing_run}/results.json" ] && [ -f "${existing_run}/statistics.json" ]; then
      if validate_reusable_run "${existing_run}"; then
        completed_runs+=("${existing_run}")
      else
        echo "Ignoring non-reusable paired Codex run: ${existing_run}" >&2
      fi
    fi
  done
  if ((${#completed_runs[@]} > 1)); then
    echo "Multiple completed Codex runs exist for ${RUN_DATE_FORMATTED}; refusing ambiguous sync" >&2
    exit 1
  fi
  if ((${#completed_runs[@]} == 1)); then
    echo "Reusing completed Codex run for ${RUN_DATE_FORMATTED}: ${completed_runs[0]}"
    sync_run "${completed_runs[0]}"
    exit 0
  fi
fi

# Every task installs Codex in its own container. Pin one version for the whole
# run, checked to install here first, so a release during the run can neither
# split the run across versions nor fail tasks while its builds are uploading.
PINNED_CONFIG_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/marginlab-codex-config.XXXXXX")"
trap 'rm -rf -- "${PINNED_CONFIG_ROOT}"' EXIT
AGENT_CONFIG="${PINNED_CONFIG_ROOT}/codex-gpt-6.1-sol-high"
CODEX_VERSION="$(python3 "${SCRIPT_DIR}/lib/pin_agent_config.py" \
  --config "${SCRIPT_DIR}/../../configs/example-agent-configs/codex-gpt-6.1-sol-high" \
  --version-input codex_version \
  --package @openai/codex \
  --binary codex \
  --output "${AGENT_CONFIG}")"
echo "Every Codex task in this run uses @openai/codex ${CODEX_VERSION}"

if ! env -u OPENAI_API_KEY margin run \
  --output "${TARGET_DIR}" \
  --suite "${SWE_SUITES_ROOT}/swe-bench-pro-curated-50" \
  --agent-config "${AGENT_CONFIG}" \
  --eval "${DAILY_STATE_DIR}/eval-config-codex.toml" \
  --non-interactive; then
  echo "margin run returned non-zero for Codex; continuing to stats + sync" >&2
fi

STATS_ARGS=(
  --output "${TARGET_DIR}/statistics.json"
  --baseline "${BASELINE_P0}"
  --date "${RUN_DATE_FORMATTED}"
  --non-test-failure-policy "${NON_TEST_FAILURE_POLICY}"
  --expected-instances "${EXPECTED_INSTANCES}"
  --minimum-valid-instances "${MINIMUM_VALID_INSTANCES}"
  --required-results "${TARGET_DIR}/results.json"
)
for series in "${BASELINE_SERIES[@]}"; do
  STATS_ARGS+=(--runs_dir "${CODEX_RUNS_DIR}/${series}")
done

python3 "${SCRIPT_DIR}/statistics/compute_stats.py" "${STATS_ARGS[@]}"

validate_reusable_run "${TARGET_DIR}"
sync_run "${TARGET_DIR}"
