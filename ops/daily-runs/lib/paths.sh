#!/usr/bin/env bash

marginlab_eval_workspace_root() {
  local script_dir="$1"
  local configured_root="${MARGINLAB_EVAL_WORKSPACE_ROOT:-}"
  if [ -n "${configured_root}" ]; then
    (cd "${configured_root}" && pwd)
  else
    # Production layout:
    #   <workspace>/evals/ops/daily-runs
    (cd "${script_dir}/../../.." && pwd)
  fi
}
