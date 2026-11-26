#!/usr/bin/env bash
# The Antigravity CLI tracker's nightly sample: Gemini 4 Neon at high effort on
# 6 of the 50 daily-suite tasks, the next 6 in suite order each night (see
# lib/antigravity_suite.py). The tracker runs on a Google AI Pro sign-in, whose
# weekly Antigravity limit covers about 47 such tasks (measured 2026-11-26), so
# a full 50 a night doesn't fit; a 7-day window holds 42 different tasks.
#
# Exits 0 after syncing the night's run, 75 when it skips the night because the
# account's limits are too low for the sample (nothing runs or syncs), and
# non-zero otherwise.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/paths.sh"
source "${SCRIPT_DIR}/lib/policy.sh"
ROOT_DIR="$(marginlab_eval_workspace_root "${SCRIPT_DIR}")"
DAILY_STATE_DIR="${MARGINLAB_DAILY_STATE_DIR:-${ROOT_DIR}/daily-runs}"
SWE_SUITES_ROOT="${MARGINLAB_SWE_SUITES_ROOT:-${ROOT_DIR}/swe-suites}"
PROJECTS_ROOT="${MARGINLAB_PROJECTS_ROOT:-$(cd "${ROOT_DIR}/.." && pwd)}"
RAW_REPOSITORY="${MARGINLAB_RAW_REPOSITORY:-${PROJECTS_ROOT}/marginlab}"
MODEL_SERIES="gemini-4-neon-high"
RUN_TIMESTAMP="${RUN_TIMESTAMP:-$(date -u +%Y%m%d-%H%M%S)}"
ANTIGRAVITY_RUNS_DIR="${DAILY_STATE_DIR}/runs/antigravity"
TARGET_DIR="${ANTIGRAVITY_RUNS_DIR}/${MODEL_SERIES}/${RUN_TIMESTAMP}"
RUNS_DIR="$(dirname "${TARGET_DIR}")"
RUN_DATE="${TARGET_DIR##*/}"
RUN_DATE_FORMATTED="${RUN_DATE:0:4}-${RUN_DATE:4:2}-${RUN_DATE:6:2}"
TASKS_PER_RUN="${MARGINLAB_ANTIGRAVITY_TASKS_PER_RUN:-6}"
# Provisional: the site shows this tracker as collecting its baseline and tests
# nothing against this rate. Replace it with the pooled rate of a fixed set of
# days, as for the other trackers, when the baseline is set.
BASELINE_P0="${ANTIGRAVITY_BASELINE_P0:-0.70}"
EVAL_CONFIG="${MARGINLAB_ANTIGRAVITY_EVAL_CONFIG:-${DAILY_STATE_DIR}/eval-config-codex.toml}"
AGY="${MARGINLAB_AGY:-$HOME/.local/bin/agy}"
# A night of 6 tasks used 13 points of the weekly limit and 48 of the 5-hour
# limit on average (2026-11-26), with the largest task using three times the
# smallest's; these leave room for a heavy night.
MIN_WEEKLY_PERCENT="${MARGINLAB_ANTIGRAVITY_MIN_WEEKLY_PERCENT:-20}"
MIN_FIVE_HOUR_PERCENT="${MARGINLAB_ANTIGRAVITY_MIN_FIVE_HOUR_PERCENT:-70}"
SUITE_SOURCE="${SWE_SUITES_ROOT}/swe-bench-pro-curated-50"
SYNC_DESTINATION="${RAW_REPOSITORY}/data/benchmarks/degradation_trackers/antigravity/swe-bench-pro/data"

# A night has TASKS_PER_RUN tasks; at most one may fail for infrastructure reasons.
marginlab_load_daily_run_policy
EXPECTED_INSTANCES="${TASKS_PER_RUN}"
MINIMUM_VALID_INSTANCES="${MARGINLAB_ANTIGRAVITY_MINIMUM_VALID_INSTANCES:-$((TASKS_PER_RUN - 1))}"
if ! [[ "${TASKS_PER_RUN}" =~ ^[1-9][0-9]*$ ]] || ! [[ "${MINIMUM_VALID_INSTANCES}" =~ ^[1-9][0-9]*$ ]] \
  || ((MINIMUM_VALID_INSTANCES > EXPECTED_INSTANCES)); then
  echo "MARGINLAB_ANTIGRAVITY_TASKS_PER_RUN and MARGINLAB_ANTIGRAVITY_MINIMUM_VALID_INSTANCES must be positive, the second no greater" >&2
  exit 2
fi

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

# One completed sample per date. Set FORCE_RUN=1 only for an intentional rerun.
if [ "${FORCE_RUN:-0}" != "1" ]; then
  completed_runs=()
  for existing_run in "${RUNS_DIR}/${RUN_DATE:0:8}-"*; do
    if [ -f "${existing_run}/results.json" ] && [ -f "${existing_run}/statistics.json" ]; then
      if validate_reusable_run "${existing_run}"; then
        completed_runs+=("${existing_run}")
      else
        echo "Ignoring non-reusable Antigravity run: ${existing_run}" >&2
      fi
    fi
  done
  if ((${#completed_runs[@]} > 1)); then
    echo "Multiple completed Antigravity runs exist for ${RUN_DATE_FORMATTED}; refusing ambiguous sync" >&2
    exit 1
  fi
  if ((${#completed_runs[@]} == 1)); then
    echo "Reusing completed Antigravity run for ${RUN_DATE_FORMATTED}: ${completed_runs[0]}"
    sync_run "${completed_runs[0]}"
    exit 0
  fi
fi

for needed in "${SUITE_SOURCE}/suite.toml" "${EVAL_CONFIG}" "${AGY}"; do
  if [ ! -e "${needed}" ]; then
    echo "Antigravity can't run: ${needed} doesn't exist" >&2
    exit 1
  fi
done

# Skip the night, rather than fail tasks, when the account's limits are short.
limits_status=0
python3 "${SCRIPT_DIR}/lib/antigravity_limits.py" \
  --agy "${AGY}" \
  --min-weekly "${MIN_WEEKLY_PERCENT}" \
  --min-five-hour "${MIN_FIVE_HOUR_PERCENT}" || limits_status=$?
if ((limits_status == 75)); then
  echo "SKIP: Antigravity skipped ${RUN_DATE_FORMATTED}: the account's limits are too low for tonight's ${TASKS_PER_RUN} tasks" >&2
  exit 75
elif ((limits_status != 0)); then
  exit "${limits_status}"
fi

WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/marginlab-antigravity.XXXXXX")"
trap 'rm -rf -- "${WORK_ROOT}"' EXIT
NIGHT_SUITE="${WORK_ROOT}/suite"
echo "Tonight's ${TASKS_PER_RUN} Antigravity tasks:"
python3 "${SCRIPT_DIR}/lib/antigravity_suite.py" \
  --source "${SUITE_SOURCE}" \
  --target "${NIGHT_SUITE}" \
  --date "${RUN_DATE_FORMATTED}" \
  --count "${TASKS_PER_RUN}" | sed 's/^/  /'

# Every task installs agy in its own container: pin one release for the night.
AGENT_CONFIG="${WORK_ROOT}/antigravity-cli-gemini-4-neon-high"
AGY_RELEASE="$(python3 "${SCRIPT_DIR}/lib/pin_agy_config.py" \
  --config "${SCRIPT_DIR}/../../configs/example-agent-configs/antigravity-cli-gemini-4-neon-high" \
  --output "${AGENT_CONFIG}")"
echo "Every Antigravity task in this run uses agy release ${AGY_RELEASE}"

if ! env -u GEMINI_API_KEY -u DBUS_SESSION_BUS_ADDRESS margin run \
  --output "${TARGET_DIR}" \
  --suite "${NIGHT_SUITE}" \
  --agent-config "${AGENT_CONFIG}" \
  --eval "${EVAL_CONFIG}" \
  --non-interactive; then
  echo "margin run returned non-zero for Antigravity; continuing to checks + stats + sync" >&2
fi

python3 "${SCRIPT_DIR}/lib/antigravity_run_check.py" --run "${TARGET_DIR}"

STATS_ARGS=(
  --output "${TARGET_DIR}/statistics.json"
  --baseline "${BASELINE_P0}"
  --date "${RUN_DATE_FORMATTED}"
  --non-test-failure-policy "${NON_TEST_FAILURE_POLICY}"
  --expected-instances "${EXPECTED_INSTANCES}"
  --minimum-valid-instances "${MINIMUM_VALID_INSTANCES}"
  --required-results "${TARGET_DIR}/results.json"
  --runs_dir "${RUNS_DIR}"
)
python3 "${SCRIPT_DIR}/statistics/compute_stats.py" "${STATS_ARGS[@]}"

validate_reusable_run "${TARGET_DIR}"
sync_run "${TARGET_DIR}"
