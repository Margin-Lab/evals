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
RUN_TIMESTAMP="${RUN_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"
TARGET_DIR="${DAILY_STATE_DIR}/runs/claude-code/${RUN_TIMESTAMP}"
RUNS_DIR="$(dirname "${TARGET_DIR}")"
RUN_DATE="${TARGET_DIR##*/}"
RUN_DATE_FORMATTED="${RUN_DATE:0:4}-${RUN_DATE:4:2}-${RUN_DATE:6:2}"
# Opus 5.5/high's baseline: the pooled pass rate of its daily runs from
# 2026-09-24 to 2026-11-26 (784 of 1006 graded tasks, 21 runs). It must match
# CLAUDE_BASELINE_WINDOW in the site's hosting/src/lib/tracker-mode.ts; the site
# build stops if they differ.
BASELINE_P0="${CLAUDE_CODE_BASELINE_P0:-0.7793}"
marginlab_load_daily_run_policy
SYNC_DESTINATION="${RAW_REPOSITORY}/data/benchmarks/degradation_trackers/claude_code/swe-bench-pro/data"

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

# Keep the daily tracker at one completed sample per date. Set FORCE_RUN=1
# only for an intentional rerun that an operator will reconcile before publish.
if [ "${FORCE_RUN:-0}" != "1" ]; then
  completed_runs=()
  for existing_run in "${RUNS_DIR}/${RUN_DATE:0:8}-"*; do
    if [ -f "${existing_run}/results.json" ] && [ -f "${existing_run}/statistics.json" ]; then
      if validate_reusable_run "${existing_run}"; then
        completed_runs+=("${existing_run}")
      else
        echo "Ignoring non-reusable paired Claude Code run: ${existing_run}" >&2
      fi
    fi
  done
  if ((${#completed_runs[@]} > 1)); then
    echo "Multiple completed Claude Code runs exist for ${RUN_DATE_FORMATTED}; refusing ambiguous sync" >&2
    exit 1
  fi
  if ((${#completed_runs[@]} == 1)); then
    echo "Reusing completed Claude Code run for ${RUN_DATE_FORMATTED}: ${completed_runs[0]}"
    sync_run "${completed_runs[0]}"
    exit 0
  fi
fi

# Every task installs Claude Code in its own container. Pin one version for the
# whole run, checked to install here first, so a release during the run can
# neither split the run across versions nor fail tasks while it is uploading.
PINNED_CONFIG_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/marginlab-claude-code-config.XXXXXX")"
trap 'rm -rf -- "${PINNED_CONFIG_ROOT}"' EXIT
AGENT_CONFIG="${PINNED_CONFIG_ROOT}/claude-code-opus-5.5-high"
CLAUDE_VERSION="$(python3 "${SCRIPT_DIR}/lib/pin_agent_config.py" \
  --config "${SCRIPT_DIR}/../../configs/example-agent-configs/claude-code-opus-5.5-high" \
  --version-input claude_version \
  --package @anthropic-ai/claude-code \
  --binary claude \
  --output "${AGENT_CONFIG}")"
echo "Every Claude Code task in this run uses @anthropic-ai/claude-code ${CLAUDE_VERSION}"

bash "${DAILY_STATE_DIR}/warmup-claude.sh"

if ! margin run --output "${TARGET_DIR}" --suite "${SWE_SUITES_ROOT}/swe-bench-pro-curated-50" --agent-config "${AGENT_CONFIG}" --eval "${DAILY_STATE_DIR}/eval-config.toml" --non-interactive; then
  echo "margin run returned non-zero for Claude Code; continuing to stats + sync" >&2
fi

python3 "${SCRIPT_DIR}/statistics/compute_stats.py" \
  --runs_dir "${RUNS_DIR}" \
  --output "${TARGET_DIR}/statistics.json" \
  --date "${RUN_DATE_FORMATTED}" \
  --baseline "${BASELINE_P0}" \
  --non-test-failure-policy "${NON_TEST_FAILURE_POLICY}" \
  --expected-instances "${EXPECTED_INSTANCES}" \
  --minimum-valid-instances "${MINIMUM_VALID_INSTANCES}" \
  --required-results "${TARGET_DIR}/results.json"

validate_reusable_run "${TARGET_DIR}"
sync_run "${TARGET_DIR}"
