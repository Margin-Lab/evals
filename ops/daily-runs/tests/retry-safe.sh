#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_ROOT="$(mktemp -d /tmp/marginlab-daily-retry-test.XXXXXX)"
trap 'rm -rf -- "${FIXTURE_ROOT}"' EXIT

WORKSPACE_ROOT="${FIXTURE_ROOT}/workspace"
STATE_ROOT="${FIXTURE_ROOT}/state"
RAW_ROOT="${FIXTURE_ROOT}/raw"
BIN_ROOT="${FIXTURE_ROOT}/bin"
MARGIN_LOG="${FIXTURE_ROOT}/margin.log"
WARMUP_LOG="${FIXTURE_ROOT}/warmup.log"
TARGET_TIMESTAMP="20260725-080000"

mkdir -p \
  "${WORKSPACE_ROOT}/swe-suites" \
  "${STATE_ROOT}/runs/codex/gpt-5-6-sol-high/20260725-040001" \
  "${STATE_ROOT}/runs/claude-code/20260725-045635" \
  "${RAW_ROOT}/data/benchmarks/degradation_trackers/codex/swe-bench-pro/data/20260725_040001" \
  "${RAW_ROOT}/data/benchmarks/degradation_trackers/claude_code/swe-bench-pro/data/20260725_045635" \
  "${BIN_ROOT}"

printf '{"state":"completed","total_instances":50,"status":{"succeeded":{"count":40},"test_failed":{"count":10},"infra_failed":{"count":0},"canceled":{"count":0}}}\n' \
  >"${STATE_ROOT}/runs/codex/gpt-5-6-sol-high/20260725-040001/results.json"
printf '{"state":"failed","total_instances":50,"status":{"succeeded":{"count":42},"test_failed":{"count":7},"infra_failed":{"count":1},"canceled":{"count":0}}}\n' \
  >"${STATE_ROOT}/runs/claude-code/20260725-045635/results.json"
for run_root in \
  "${STATE_ROOT}/runs/codex/gpt-5-6-sol-high/20260725-040001" \
  "${STATE_ROOT}/runs/claude-code/20260725-045635"; do
  printf '{"target_date":"2026-07-25","non_test_failure_policy":"exclude"}\n' \
    >"${run_root}/statistics.json"
done

# Model an interrupted prior sync: results exists in each raw destination but
# statistics is absent and the results content is incomplete.
printf 'partial\n' \
  >"${RAW_ROOT}/data/benchmarks/degradation_trackers/codex/swe-bench-pro/data/20260725_040001/results.json"
printf 'partial\n' \
  >"${RAW_ROOT}/data/benchmarks/degradation_trackers/claude_code/swe-bench-pro/data/20260725_045635/results.json"

cat >"${BIN_ROOT}/margin" <<'MARGIN'
#!/usr/bin/env bash
set -euo pipefail
printf 'unexpected margin invocation\n' >>"${MARGIN_LOG}"
exit 99
MARGIN
chmod +x "${BIN_ROOT}/margin"

cat >"${STATE_ROOT}/warmup-claude.sh" <<'WARMUP'
#!/usr/bin/env bash
set -euo pipefail
printf 'unexpected warmup invocation\n' >>"${WARMUP_LOG}"
WARMUP
chmod +x "${STATE_ROOT}/warmup-claude.sh"

COMMON_ENV=(
  env
  "PATH=${BIN_ROOT}:${PATH}"
  "RUN_TIMESTAMP=${TARGET_TIMESTAMP}"
  "MARGINLAB_EVAL_WORKSPACE_ROOT=${WORKSPACE_ROOT}"
  "MARGINLAB_DAILY_STATE_DIR=${STATE_ROOT}"
  "MARGINLAB_RAW_REPOSITORY=${RAW_ROOT}"
  "MARGIN_LOG=${MARGIN_LOG}"
  "WARMUP_LOG=${WARMUP_LOG}"
)

"${COMMON_ENV[@]}" "${SCRIPT_DIR}/run-codex.sh"
"${COMMON_ENV[@]}" "${SCRIPT_DIR}/run-claude-code.sh"
# A second retry is an idempotent sync, not another paid evaluation.
"${COMMON_ENV[@]}" "${SCRIPT_DIR}/run-codex.sh"
"${COMMON_ENV[@]}" "${SCRIPT_DIR}/run-claude-code.sh"

cmp -s \
  "${STATE_ROOT}/runs/codex/gpt-5-6-sol-high/20260725-040001/results.json" \
  "${RAW_ROOT}/data/benchmarks/degradation_trackers/codex/swe-bench-pro/data/20260725_040001/results.json"
cmp -s \
  "${STATE_ROOT}/runs/codex/gpt-5-6-sol-high/20260725-040001/statistics.json" \
  "${RAW_ROOT}/data/benchmarks/degradation_trackers/codex/swe-bench-pro/data/20260725_040001/statistics.json"
cmp -s \
  "${STATE_ROOT}/runs/claude-code/20260725-045635/results.json" \
  "${RAW_ROOT}/data/benchmarks/degradation_trackers/claude_code/swe-bench-pro/data/20260725_045635/results.json"
cmp -s \
  "${STATE_ROOT}/runs/claude-code/20260725-045635/statistics.json" \
  "${RAW_ROOT}/data/benchmarks/degradation_trackers/claude_code/swe-bench-pro/data/20260725_045635/statistics.json"
test ! -e "${MARGIN_LOG}"
test ! -e "${WARMUP_LOG}"

# More than one completed run for the target day is ambiguous and must not
# sync or start a new evaluation.
second_codex="${STATE_ROOT}/runs/codex/gpt-5-6-sol-high/20260725-050001"
mkdir -p "${second_codex}"
printf '{"state":"completed","total_instances":50,"status":{"succeeded":{"count":40},"test_failed":{"count":10},"infra_failed":{"count":0},"canceled":{"count":0}}}\n' \
  >"${second_codex}/results.json"
printf '{"target_date":"2026-07-25","non_test_failure_policy":"exclude"}\n' \
  >"${second_codex}/statistics.json"

if "${COMMON_ENV[@]}" "${SCRIPT_DIR}/run-codex.sh"; then
  echo "Expected duplicate completed Codex runs to fail" >&2
  exit 1
fi
test ! -e "${MARGIN_LOG}"
