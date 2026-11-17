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

CURRENT_TRACKERS=()
if ((codex_status == 0)); then
  CURRENT_TRACKERS+=(codex-current)
fi
if ((claude_status == 0)); then
  CURRENT_TRACKERS+=(claude-code-current)
fi
if ((${#CURRENT_TRACKERS[@]} == 0)); then
  echo "FAIL: neither tracker finished for ${TARGET_DATE}; nothing was published" >&2
  exit "${codex_status}"
fi

SOURCE_COMMIT="$(git -C "${RAW_REPOSITORY}" rev-parse HEAD)"
EVALUATION_COMMIT="$(git -C "${EVALUATION_ROOT}" rev-parse HEAD)"
PUBLISH_ARGUMENTS=(
  --automation-root "${AUTOMATION_ROOT}"
  --source "${RAW_REPOSITORY}/data"
  --source-commit "${SOURCE_COMMIT}"
  --evaluation-root "${EVALUATION_ROOT}"
  --evaluation-commit "${EVALUATION_COMMIT}"
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

publish_status=0
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
  exit "${publish_status}"
fi

if ((codex_status != 0)); then
  echo "FAIL: only Claude Code went to the site-data publisher for ${TARGET_DATE}; Codex did not finish" >&2
  exit "${codex_status}"
fi
if ((claude_status != 0)); then
  echo "FAIL: only Codex went to the site-data publisher for ${TARGET_DATE}; Claude Code did not finish" >&2
  exit "${claude_status}"
fi
