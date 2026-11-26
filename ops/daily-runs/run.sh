#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/paths.sh"
source "${SCRIPT_DIR}/lib/policy.sh"
PUBLISH=0
EVAL_WORKSPACE_ROOT="$(marginlab_eval_workspace_root "${SCRIPT_DIR}")"
DAILY_STATE_DIR="${MARGINLAB_DAILY_STATE_DIR:-${EVAL_WORKSPACE_ROOT}/daily-runs}"
PROJECTS_ROOT="${MARGINLAB_PROJECTS_ROOT:-$(cd "${EVAL_WORKSPACE_ROOT}/.." && pwd)}"
LOCK_FILE="${MARGINLAB_DAILY_LOCK_FILE:-/tmp/marginlab-daily-runs.lock}"
AUTOMATION_ROOT="${MARGINLAB_AUTOMATION_ROOT:-${PROJECTS_ROOT}/marginlab-site-data-automation}"
RAW_REPOSITORY="${MARGINLAB_RAW_REPOSITORY:-${PROJECTS_ROOT}/marginlab}"
EVALUATION_ROOT="${MARGINLAB_EVALUATION_ROOT:-${EVAL_WORKSPACE_ROOT}/evals}"
marginlab_load_daily_run_policy

usage() {
  echo "Usage: $0 [--publish]" >&2
}

while (($# > 0)); do
  case "$1" in
    --publish)
      PUBLISH=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 1
      ;;
  esac
  shift
done

exec 9>"${LOCK_FILE}"
if ! flock -n 9; then
  echo "Another daily run holds ${LOCK_FILE}; refusing to overlap" >&2
  exit 75
fi

RUN_TIMESTAMP="${MARGINLAB_DAILY_RUN_TIMESTAMP:-$(date -u +%Y%m%d-%H%M%S)}"
TARGET_DATE="${RUN_TIMESTAMP:0:4}-${RUN_TIMESTAMP:4:2}-${RUN_TIMESTAMP:6:2}"
PUBLICATION_RUN_ID="daily-${TARGET_DATE}"
export RUN_TIMESTAMP
export MARGINLAB_EXPECTED_INSTANCES="${EXPECTED_INSTANCES}"
export MARGINLAB_MINIMUM_VALID_INSTANCES="${MINIMUM_VALID_INSTANCES}"
export MARGINLAB_NON_TEST_FAILURE_POLICY="${NON_TEST_FAILURE_POLICY}"
export MARGINLAB_EVAL_WORKSPACE_ROOT="${EVAL_WORKSPACE_ROOT}"
export MARGINLAB_DAILY_STATE_DIR="${DAILY_STATE_DIR}"
export MARGINLAB_RAW_REPOSITORY="${RAW_REPOSITORY}"

# The trackers are independent: when one fails, the other still runs and its
# results are still published, and the run still exits non-zero so the alert
# hook reports the failure.
codex_status=0
bash "${SCRIPT_DIR}/run-codex.sh" || codex_status=$?
if ((codex_status != 0)); then
  echo "FAIL: Codex did not finish for ${TARGET_DATE} (status ${codex_status}); running Claude Code anyway" >&2
fi
claude_status=0
bash "${SCRIPT_DIR}/run-claude-code.sh" || claude_status=$?
if ((claude_status != 0)); then
  echo "FAIL: Claude Code did not finish for ${TARGET_DATE} (status ${claude_status})" >&2
fi

# Publishes the Codex and Claude Code trackers that finished, and returns the
# status the run exits with on their account.
publish_finished_trackers() {
  CURRENT_TRACKERS=()
  if ((codex_status == 0)); then
    CURRENT_TRACKERS+=(codex-current)
  fi
  if ((claude_status == 0)); then
    CURRENT_TRACKERS+=(claude-code-current)
  fi
  if ((${#CURRENT_TRACKERS[@]} == 0)); then
    echo "FAIL: neither tracker finished for ${TARGET_DATE}; nothing was published" >&2
    return "${codex_status}"
  fi

  local source_commit evaluation_commit
  source_commit="$(git -C "${RAW_REPOSITORY}" rev-parse HEAD)" || return
  evaluation_commit="$(git -C "${EVALUATION_ROOT}" rev-parse HEAD)" || return
  PUBLISH_ARGUMENTS=(
    --automation-root "${AUTOMATION_ROOT}"
    --source "${RAW_REPOSITORY}/data"
    --source-commit "${source_commit}"
    --evaluation-root "${EVALUATION_ROOT}"
    --evaluation-commit "${evaluation_commit}"
    --run-id "${PUBLICATION_RUN_ID}"
    --target-date "${TARGET_DATE}"
    --expected-instances "${EXPECTED_INSTANCES}"
    --minimum-valid-instances "${MINIMUM_VALID_INSTANCES}"
    --non-test-failure-policy "${NON_TEST_FAILURE_POLICY}"
  )
  if ((${#CURRENT_TRACKERS[@]} == 1)); then
    # Only the finished tracker must be current; the other keeps its published days.
    PUBLISH_ARGUMENTS+=(--current-tracker "${CURRENT_TRACKERS[0]}")
  fi

  local publish_status=0
  if ((PUBLISH)); then
    MARGINLAB_SITE_DATA_PUBLISH=1 \
      npm --prefix "${AUTOMATION_ROOT}/hosting" run publish:site-data -- \
      --publish "${PUBLISH_ARGUMENTS[@]}" || publish_status=$?
  else
    env -u MARGINLAB_SITE_DATA_PUBLISH \
      npm --prefix "${AUTOMATION_ROOT}/hosting" run publish:site-data -- \
      "${PUBLISH_ARGUMENTS[@]}" || publish_status=$?
  fi
  if ((publish_status != 0)); then
    echo "FAIL: the site-data publisher failed for ${TARGET_DATE} (status ${publish_status})" >&2
    return "${publish_status}"
  fi

  if ((codex_status != 0)); then
    echo "FAIL: only Claude Code went to the site-data publisher for ${TARGET_DATE}; Codex did not finish" >&2
    return "${codex_status}"
  fi
  if ((claude_status != 0)); then
    echo "FAIL: only Codex went to the site-data publisher for ${TARGET_DATE}; Claude Code did not finish" >&2
    return "${claude_status}"
  fi
}

run_status=0
publish_finished_trackers || run_status=$?

# The Antigravity CLI tracker (trial) runs after the others have published, so it
# never delays them, when "${DAILY_STATE_DIR}/antigravity-tracker.enabled" exists.
# Its runs are synced to the raw data but not published yet. A night it skips
# because the account's limits are low is not a failure.
if [ -e "${DAILY_STATE_DIR}/antigravity-tracker.enabled" ]; then
  antigravity_status=0
  bash "${SCRIPT_DIR}/run-antigravity.sh" || antigravity_status=$?
  if ((antigravity_status == 75)); then
    echo "Antigravity skipped ${TARGET_DATE}; the run's status is unaffected" >&2
  elif ((antigravity_status != 0)); then
    echo "FAIL: Antigravity did not finish for ${TARGET_DATE} (status ${antigravity_status})" >&2
    if ((run_status == 0)); then
      run_status="${antigravity_status}"
    fi
  fi
fi
exit "${run_status}"
