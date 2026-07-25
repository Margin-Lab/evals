#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/paths.sh"
EVAL_WORKSPACE_ROOT="$(marginlab_eval_workspace_root "${SCRIPT_DIR}")"
DAILY_STATE_DIR="${MARGINLAB_DAILY_STATE_DIR:-${EVAL_WORKSPACE_ROOT}/daily-runs}"
LOG_DIR="${MARGINLAB_DAILY_LOG_DIR:-${DAILY_STATE_DIR}/logs}"
LOG_FILE="${LOG_DIR}/run-$(date +%Y%m%d).log"
ZSH_BIN="/usr/bin/zsh"
TIMEOUT_SECONDS="${MARGINLAB_DAILY_TIMEOUT_SECONDS:-28800}"
ALERT_HOOK="${MARGINLAB_DAILY_ALERT_HOOK:-}"
PUBLISH=0

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
      exit 2
      ;;
  esac
  shift
done

if ! [[ "${TIMEOUT_SECONDS}" =~ ^[1-9][0-9]*$ ]]; then
  echo "MARGINLAB_DAILY_TIMEOUT_SECONDS must be a positive integer" >&2
  exit 2
fi

mkdir -p "${LOG_DIR}"

{
  echo "[$(date '+%Y-%m-%dT%H:%M:%S%z')] Starting daily run"

  run_command="cd \"${SCRIPT_DIR}\" && ./run.sh"
  if ((PUBLISH)); then
    run_command+=" --publish"
  fi

  if timeout --signal=TERM --kill-after=300 "${TIMEOUT_SECONDS}" \
    "${ZSH_BIN}" -lic "${run_command}"; then
    status=0
  else
    status=$?
  fi

  echo "[$(date '+%Y-%m-%dT%H:%M:%S%z')] Finished daily run with status ${status}"

  if ((status != 0)) && [ -n "${ALERT_HOOK}" ]; then
    if [ ! -x "${ALERT_HOOK}" ]; then
      echo "Alert hook is not executable: ${ALERT_HOOK}" >&2
    elif ! "${ALERT_HOOK}" "daily-run-failed" "${status}" "${LOG_FILE}"; then
      echo "Alert hook failed; preserving daily-run status ${status}" >&2
    fi
  fi

  exit "${status}"
} >> "${LOG_FILE}" 2>&1
