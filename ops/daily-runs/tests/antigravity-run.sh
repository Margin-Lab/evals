#!/usr/bin/env bash
set -euo pipefail

# The Antigravity tracker's nightly run, end to end with stand-in margin and agy:
# the night's tasks, statistics and sync; a rerun reuses the night; low limits
# skip it; a task stopped by a limit keeps the night out of the data.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPOSITORY="$(cd "${SCRIPT_DIR}/../.." && pwd)"
FIXTURE_ROOT="$(mktemp -d /tmp/marginlab-antigravity-run-test.XXXXXX)"
trap 'rm -rf -- "${FIXTURE_ROOT}"' EXIT

WORKSPACE_ROOT="${FIXTURE_ROOT}/workspace"
RUNNER_ROOT="${WORKSPACE_ROOT}/evals/ops/daily-runs"
STATE_ROOT="${FIXTURE_ROOT}/state"
RAW_ROOT="${FIXTURE_ROOT}/raw"
BIN_ROOT="${FIXTURE_ROOT}/bin"
HOME_ROOT="${FIXTURE_ROOT}/home"
MARGIN_LOG="${FIXTURE_ROOT}/margin.log"
SUITE_ROOT="${WORKSPACE_ROOT}/swe-suites/swe-bench-pro-curated-50"
SYNCED="${RAW_ROOT}/data/benchmarks/degradation_trackers/antigravity/swe-bench-pro/data"
mkdir -p "${RUNNER_ROOT}" "${STATE_ROOT}" "${RAW_ROOT}" "${BIN_ROOT}" "${SUITE_ROOT}/cases" \
  "${HOME_ROOT}/.gemini/antigravity-cli" "${WORKSPACE_ROOT}/evals/configs/example-agent-configs"

# The real scripts, but pinning agy to a fixed release without the network.
cp -R "${SCRIPT_DIR}/run-antigravity.sh" "${SCRIPT_DIR}/sync-run.sh" "${SCRIPT_DIR}/lib" "${SCRIPT_DIR}/statistics" "${RUNNER_ROOT}/"
cp -R "${REPOSITORY}/configs/example-agent-configs/antigravity-cli-gemini-4-neon-high" \
  "${WORKSPACE_ROOT}/evals/configs/example-agent-configs/"
cat >"${RUNNER_ROOT}/lib/pin_agy_config.py" <<'PIN'
import shutil, sys
shutil.copytree(sys.argv[sys.argv.index("--config") + 1], sys.argv[sys.argv.index("--output") + 1])
print("1.5.18-1")
PIN

python3 - "${SUITE_ROOT}" <<'PY'
import json, sys
from pathlib import Path
root = Path(sys.argv[1])
cases = [f"case-{index:02d}" for index in range(50)]
for case in cases:
    (root / "cases" / case).mkdir()
    (root / "cases" / case / "case.toml").write_text(f'kind = "test_case"\nname = "{case}"\n')
(root / "suite.toml").write_text('kind = "test_suite"\nname = "curated-50"\ncases = [' + ", ".join(json.dumps(c) for c in cases) + "]\n")
PY
printf 'max_concurrency = 2\n' >"${STATE_ROOT}/eval-config-codex.toml"
printf '{"token": {}}\n' >"${HOME_ROOT}/.gemini/antigravity-cli/antigravity-oauth-token"

# agy answers /usage from LIMITS: "<weekly %> <5-hour %> <exit status>". It runs in a
# clean environment, so it reads the file rather than variables.
LIMITS="${FIXTURE_ROOT}/limits"
cat >"${BIN_ROOT}/agy" <<AGY
#!/usr/bin/env bash
read -r weekly five_hour status <"${LIMITS}"
[ "\${status}" = 0 ] || exit "\${status}"
printf '{"status":"SUCCESS","response":"Gemini Models\\\\tWeekly Limit Remaining\\\\t%s%%\\\\t2026-12-03T17:26:06Z\\\\nGemini Models\\\\tFive Hour Limit Remaining\\\\t%s%%\\\\t2026-11-27T11:00:00Z\\\\n"}\\n' "\${weekly}" "\${five_hour}"
AGY
# margin passes the first four tasks and fails the rest; QUOTA_STOP stops the last on a limit.
cat >"${BIN_ROOT}/margin" <<'MARGIN'
#!/usr/bin/env bash
set -euo pipefail
output="" suite=""
while (($# > 0)); do
  case "$1" in --output) output="$2"; shift 2 ;; --suite) suite="$2"; shift 2 ;; *) shift ;; esac
done
printf 'margin %s\n' "$(python3 -c 'import sys, tomllib; print(" ".join(tomllib.load(open(sys.argv[1], "rb"))["cases"]))' "${suite}/suite.toml")" >>"${MARGIN_LOG}"
python3 - "${output}" "${suite}" "${QUOTA_STOP:-0}" <<'PY'
import json, sys, tomllib
from pathlib import Path
output, suite, quota_stop = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3] == "1"
cases = tomllib.loads((suite / "suite.toml").read_text())["cases"]
instances = []
for index, case in enumerate(cases):
    instance = f"run-inst-{index + 1:04d}"
    extra = {"status": "SUCCESS"}
    if quota_stop and index == len(cases) - 1:
        extra = {"status": "ERROR", "error": "You have exhausted your quota on this model."}
    (output / "instances" / instance).mkdir(parents=True)
    (output / "instances" / instance / "trajectory.json").write_text(json.dumps({"final_metrics": {"extra": extra}}))
    instances.append({"instance_id": instance, "case_id": case, "final_state": "succeeded" if index < 4 else "test_failed"})
passed = sum(1 for instance in instances if instance["final_state"] == "succeeded")
(output / "results.json").write_text(json.dumps({
    "run_id": output.name, "state": "completed", "total_instances": len(cases),
    "status": {"succeeded": {"count": passed}, "test_failed": {"count": len(cases) - passed},
               "infra_failed": {"count": 0}, "canceled": {"count": 0}},
    "usage": {"input_tokens": 1000, "output_tokens": 100, "tool_calls": 10},
    "runtime": {"run_ms": 600000},
    "instances": instances,
}))
PY
MARGIN
chmod +x "${BIN_ROOT}/agy" "${BIN_ROOT}/margin"

# night <timestamp> <weekly %> <5-hour %> [agy exit] [VAR=value...]; prints the exit status.
night() {
  local timestamp="$1" weekly="$2" five_hour="$3" agy_exit="${4:-0}"
  shift 3
  (($# > 0)) && shift
  printf '%s %s %s\n' "${weekly}" "${five_hour}" "${agy_exit}" >"${LIMITS}"
  local status=0
  env "PATH=${BIN_ROOT}:${PATH}" "HOME=${HOME_ROOT}" "RUN_TIMESTAMP=${timestamp}" \
    "MARGINLAB_EVAL_WORKSPACE_ROOT=${WORKSPACE_ROOT}" "MARGINLAB_DAILY_STATE_DIR=${STATE_ROOT}" \
    "MARGINLAB_RAW_REPOSITORY=${RAW_ROOT}" "MARGINLAB_AGY=${BIN_ROOT}/agy" "MARGIN_LOG=${MARGIN_LOG}" "$@" \
    bash "${RUNNER_ROOT}/run-antigravity.sh" >"${FIXTURE_ROOT}/night.log" 2>&1 || status=$?
  echo "${status}"
}
fail() {
  echo "FAILED: $*" >&2
  cat "${FIXTURE_ROOT}/night.log" >&2
  exit 1
}

# The first night runs the suite's first 6 tasks, computes statistics and syncs.
test "$(night 20261127-060000 82 100)" = 0 || fail "first night"
test "$(cat "${MARGIN_LOG}")" = "margin case-00 case-01 case-02 case-03 case-04 case-05" || fail "first night's tasks"
python3 - "${SYNCED}/20261127_060000/statistics.json" <<'PY' || fail "first night's statistics"
import json, sys
stats = json.load(open(sys.argv[1]))
assert stats["target_date"] == "2026-11-27", stats
assert stats["timescales"]["daily"]["trials"] == 6, stats["timescales"]["daily"]
assert stats["timescales"]["daily"]["successes"] == 4, stats["timescales"]["daily"]
PY
cmp -s "${STATE_ROOT}/runs/antigravity/gemini-4-neon-high/20261127-060000/results.json" \
  "${SYNCED}/20261127_060000/results.json" || fail "first night's sync"

# A rerun of the night syncs again without another evaluation.
test "$(night 20261127-070000 82 100)" = 0 || fail "rerun"
grep -q 'Reusing completed Antigravity run for 2026-11-27' "${FIXTURE_ROOT}/night.log" || fail "rerun reuse"
test "$(wc -l <"${MARGIN_LOG}")" -eq 1 || fail "rerun ran margin"

# The next night takes the next 6 tasks, and its weekly window pools both nights.
test "$(night 20261128-060000 69 100)" = 0 || fail "second night"
test "$(tail -n 1 "${MARGIN_LOG}")" = "margin case-06 case-07 case-08 case-09 case-10 case-11" || fail "second night's tasks"
python3 -c 'import json, sys; assert json.load(open(sys.argv[1]))["timescales"]["weekly"]["trials"] == 12' \
  "${SYNCED}/20261128_060000/statistics.json" || fail "second night's weekly window"

# Low limits skip the night (exit 75) without running anything.
test "$(night 20261129-060000 12 100)" = 75 || fail "low weekly limit"
test "$(night 20261129-060000 80 40)" = 75 || fail "low 5-hour limit"
grep -q 'SKIP: Antigravity skipped 2026-11-29' "${FIXTURE_ROOT}/night.log" || fail "skip message"
test "$(wc -l <"${MARGIN_LOG}")" -eq 2 || fail "a skipped night ran margin"

# Limits that can't be read fail the night.
test "$(night 20261129-060000 80 100 1)" = 1 || fail "unreadable limits"

# A task stopped on a limit keeps the night out of the synced data.
status="$(night 20261130-060000 80 100 0 QUOTA_STOP=1)"
test "${status}" != 0 || fail "a night with a quota stop succeeded"
grep -q 'stopped on the account' "${FIXTURE_ROOT}/night.log" || fail "quota stop message"
test ! -e "${SYNCED}/20261130_060000" || fail "a night with a quota stop was synced"
