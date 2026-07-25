#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

bash -n \
  "${SCRIPT_DIR}/run.sh" \
  "${SCRIPT_DIR}/run-from-cron.sh" \
  "${SCRIPT_DIR}/run-codex.sh" \
  "${SCRIPT_DIR}/run-claude-code.sh" \
  "${SCRIPT_DIR}/sync-run.sh" \
  "${SCRIPT_DIR}/tests/publication-mode.sh" \
  "${SCRIPT_DIR}/tests/retry-safe.sh" \
  "${SCRIPT_DIR}/lib/paths.sh"

source "${SCRIPT_DIR}/lib/paths.sh"
expected_default_root="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
test "$(marginlab_eval_workspace_root "${SCRIPT_DIR}")" = "${expected_default_root}"
test "$(
  MARGINLAB_EVAL_WORKSPACE_ROOT="${SCRIPT_DIR}" \
    marginlab_eval_workspace_root "${SCRIPT_DIR}"
)" = "${SCRIPT_DIR}"
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s "${SCRIPT_DIR}/statistics" \
  -p 'test_*.py'
"${SCRIPT_DIR}/tests/retry-safe.sh"
"${SCRIPT_DIR}/tests/publication-mode.sh"
"${SCRIPT_DIR}/run.sh" --help
"${SCRIPT_DIR}/run-from-cron.sh" --help
"${SCRIPT_DIR}/sync-run.sh" --help
