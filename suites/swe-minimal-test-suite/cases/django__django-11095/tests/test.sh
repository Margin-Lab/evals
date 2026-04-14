#!/bin/bash
set -euo pipefail

cd /testbed
source /opt/miniconda3/bin/activate
conda activate testbed
export PATH="/root/.local/bin:${HOME}/.local/bin:${PATH}"
export SWEBENCH_ORIG_PATH="${PATH}"
export SWEBENCH_CASE_PYTHON="$(command -v python)"

set +e
uv run /tests/verifier.py run /tests/config.json
rc=$?
set -e
case "$rc" in
  0|1|2) exit "$rc" ;;
  *) echo "Verifier launch failed with exit code $rc" >&2; exit 2 ;;
esac
