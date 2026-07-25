#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/paths.sh"
DEPLOY=0
EVAL_WORKSPACE_ROOT="$(marginlab_eval_workspace_root "${SCRIPT_DIR}")"
DAILY_STATE_DIR="${MARGINLAB_DAILY_STATE_DIR:-${EVAL_WORKSPACE_ROOT}/daily-runs}"
PROJECTS_ROOT="${MARGINLAB_PROJECTS_ROOT:-$(cd "${EVAL_WORKSPACE_ROOT}/.." && pwd)}"
LOCK_FILE="${MARGINLAB_DAILY_LOCK_FILE:-/tmp/marginlab-daily-runs.lock}"
AUTOMATION_ROOT="${MARGINLAB_AUTOMATION_ROOT:-${PROJECTS_ROOT}/marginlab-site-data-automation}"
RAW_REPOSITORY="${MARGINLAB_RAW_REPOSITORY:-${PROJECTS_ROOT}/marginlab}"
EVALUATION_ROOT="${MARGINLAB_EVALUATION_ROOT:-${EVAL_WORKSPACE_ROOT}/evals}"
EXPECTED_INSTANCES="${MARGINLAB_EXPECTED_INSTANCES:-50}"
MINIMUM_VALID_INSTANCES="${MARGINLAB_MINIMUM_VALID_INSTANCES:-${EXPECTED_INSTANCES}}"
NON_TEST_FAILURE_POLICY="${MARGINLAB_NON_TEST_FAILURE_POLICY:-exclude}"

usage() {
  echo "Usage: $0 [--deploy]" >&2
}

while (($# > 0)); do
  case "$1" in
    --deploy)
      DEPLOY=1
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

if ! [[ "${EXPECTED_INSTANCES}" =~ ^[1-9][0-9]*$ ]]; then
  echo "MARGINLAB_EXPECTED_INSTANCES must be a positive integer" >&2
  exit 2
fi
if ! [[ "${MINIMUM_VALID_INSTANCES}" =~ ^[1-9][0-9]*$ ]] \
  || ((MINIMUM_VALID_INSTANCES > EXPECTED_INSTANCES)); then
  echo "MARGINLAB_MINIMUM_VALID_INSTANCES must be positive and no greater than MARGINLAB_EXPECTED_INSTANCES" >&2
  exit 2
fi
case "${NON_TEST_FAILURE_POLICY}" in
  reject|count-as-failure|exclude)
    ;;
  *)
    echo "MARGINLAB_NON_TEST_FAILURE_POLICY must be reject, count-as-failure, or exclude" >&2
    exit 2
    ;;
esac

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

bash "${SCRIPT_DIR}/run-codex.sh"
bash "${SCRIPT_DIR}/run-claude-code.sh"

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

if ((DEPLOY)); then
  MARGINLAB_SITE_DATA_PUBLISH=1 \
    npm --prefix "${AUTOMATION_ROOT}/hosting" run publish:site-data -- \
    --publish "${PUBLISH_ARGUMENTS[@]}"
else
  npm --prefix "${AUTOMATION_ROOT}/hosting" run publish:site-data -- \
    "${PUBLISH_ARGUMENTS[@]}"
fi
