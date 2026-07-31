#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_ROOT="$(mktemp -d /tmp/marginlab-daily-publication-test.XXXXXX)"
trap 'rm -rf -- "${FIXTURE_ROOT}"' EXIT

WORKSPACE_ROOT="${FIXTURE_ROOT}/workspace"
STATE_ROOT="${FIXTURE_ROOT}/state"
RAW_ROOT="${FIXTURE_ROOT}/raw"
AUTOMATION_ROOT="${FIXTURE_ROOT}/automation"
EVALUATION_ROOT="${FIXTURE_ROOT}/evals"
BIN_ROOT="${FIXTURE_ROOT}/bin"
PUBLISH_LOG="${FIXTURE_ROOT}/publish.log"
MARGIN_LOG="${FIXTURE_ROOT}/margin.log"
WARMUP_LOG="${FIXTURE_ROOT}/warmup.log"
TARGET_TIMESTAMP="20260725-080000"
TEST_COMMIT="1111111111111111111111111111111111111111"

mkdir -p \
  "${WORKSPACE_ROOT}/swe-suites" \
  "${STATE_ROOT}/runs/codex/gpt-5-6-sol-high/20260725-040001" \
  "${STATE_ROOT}/runs/claude-code/20260725-045635" \
  "${RAW_ROOT}" \
  "${AUTOMATION_ROOT}/hosting" \
  "${EVALUATION_ROOT}" \
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

cat >"${BIN_ROOT}/git" <<'GIT'
#!/usr/bin/env bash
set -euo pipefail
if [ "$#" -eq 4 ] \
  && [ "$1" = "-C" ] \
  && [ "$3" = "rev-parse" ] \
  && [ "$4" = "HEAD" ]; then
  printf '%s\n' "${TEST_COMMIT}"
  exit 0
fi
printf 'unexpected git invocation:' >&2
printf ' %q' "$@" >&2
printf '\n' >&2
exit 99
GIT
chmod +x "${BIN_ROOT}/git"

cat >"${BIN_ROOT}/npm" <<'NPM'
#!/usr/bin/env bash
set -euo pipefail
{
  printf 'confirmation=%s\n' "${MARGINLAB_SITE_DATA_PUBLISH:-}"
  printf 'arg=%s\n' "$@"
} >>"${PUBLISH_LOG}"
NPM
chmod +x "${BIN_ROOT}/npm"

COMMON_ENV=(
  env
  "PATH=${BIN_ROOT}:${PATH}"
  "TEST_COMMIT=${TEST_COMMIT}"
  "PUBLISH_LOG=${PUBLISH_LOG}"
  "MARGINLAB_DAILY_RUN_TIMESTAMP=${TARGET_TIMESTAMP}"
  "MARGINLAB_EVAL_WORKSPACE_ROOT=${WORKSPACE_ROOT}"
  "MARGINLAB_DAILY_STATE_DIR=${STATE_ROOT}"
  "MARGINLAB_RAW_REPOSITORY=${RAW_ROOT}"
  "MARGINLAB_AUTOMATION_ROOT=${AUTOMATION_ROOT}"
  "MARGINLAB_EVALUATION_ROOT=${EVALUATION_ROOT}"
  "MARGINLAB_DAILY_LOCK_FILE=${FIXTURE_ROOT}/daily.lock"
  "MARGINLAB_SITE_DATA_PUBLISH=1"
  "MARGIN_LOG=${MARGIN_LOG}"
  "WARMUP_LOG=${WARMUP_LOG}"
)

# Default mode reaches the publisher only after tracker validation and strips
# even an inherited write-confirmation variable. It cannot commit or push.
"${COMMON_ENV[@]}" "${SCRIPT_DIR}/run.sh"
test "$(grep -Fc 'confirmation=' "${PUBLISH_LOG}")" -eq 1
grep -Fx 'confirmation=' "${PUBLISH_LOG}"
if grep -Fxq 'arg=--publish' "${PUBLISH_LOG}"; then
  echo "Dry run unexpectedly supplied --publish" >&2
  exit 1
fi
grep -Fx 'arg=publish:site-data' "${PUBLISH_LOG}"
grep -Fx 'arg=--target-date' "${PUBLISH_LOG}"
grep -Fx 'arg=2026-07-25' "${PUBLISH_LOG}"

# Explicit publication supplies both independent confirmations. The
# site-data publisher owns the validated site-data-only commit and leased
# direct push to main.
: >"${PUBLISH_LOG}"
"${COMMON_ENV[@]}" "${SCRIPT_DIR}/run.sh" --publish
test "$(grep -Fc 'confirmation=' "${PUBLISH_LOG}")" -eq 1
grep -Fx 'confirmation=1' "${PUBLISH_LOG}"
grep -Fx 'arg=--publish' "${PUBLISH_LOG}"
grep -Fx 'arg=--expected-instances' "${PUBLISH_LOG}"
grep -Fx 'arg=--minimum-valid-instances' "${PUBLISH_LOG}"
grep -Fx 'arg=45' "${PUBLISH_LOG}"
grep -Fx 'arg=--non-test-failure-policy' "${PUBLISH_LOG}"

test ! -e "${MARGIN_LOG}"
test ! -e "${WARMUP_LOG}"

# The pre-activation name was misleading: this command publishes data to Git
# and never directly deploys Firebase.
if "${COMMON_ENV[@]}" "${SCRIPT_DIR}/run.sh" --deploy; then
  echo "Expected deprecated --deploy mode to be rejected" >&2
  exit 1
fi
