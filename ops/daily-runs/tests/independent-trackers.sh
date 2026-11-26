#!/usr/bin/env bash
set -euo pipefail

# When one tracker fails, the other still runs and is published on its own, and
# the run still exits non-zero so the alert hook reports the failure.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_ROOT="$(mktemp -d /tmp/marginlab-daily-independent-test.XXXXXX)"
trap 'rm -rf -- "${FIXTURE_ROOT}"' EXIT

RUNNER_ROOT="${FIXTURE_ROOT}/daily-runs"
BIN_ROOT="${FIXTURE_ROOT}/bin"
CALLS="${FIXTURE_ROOT}/calls.log"
OUTPUT="${FIXTURE_ROOT}/output.log"
mkdir -p \
  "${RUNNER_ROOT}/lib" \
  "${BIN_ROOT}" \
  "${FIXTURE_ROOT}/workspace" \
  "${FIXTURE_ROOT}/state" \
  "${FIXTURE_ROOT}/raw" \
  "${FIXTURE_ROOT}/automation/hosting" \
  "${FIXTURE_ROOT}/evals"

# The real parent runner, with stand-in trackers that exit as each case says.
cp "${SCRIPT_DIR}/run.sh" "${RUNNER_ROOT}/run.sh"
cp "${SCRIPT_DIR}/lib/paths.sh" "${SCRIPT_DIR}/lib/policy.sh" "${RUNNER_ROOT}/lib/"
cat >"${RUNNER_ROOT}/run-codex.sh" <<'TRACKER'
printf 'tracker codex\n' >>"${CALLS}"
exit "${CODEX_EXIT}"
TRACKER
cat >"${RUNNER_ROOT}/run-claude-code.sh" <<'TRACKER'
printf 'tracker claude-code\n' >>"${CALLS}"
exit "${CLAUDE_EXIT}"
TRACKER

cat >"${BIN_ROOT}/git" <<'GIT'
#!/usr/bin/env bash
printf '1111111111111111111111111111111111111111\n'
GIT
# The publisher exits with PUBLISH_EXIT, or with SECOND_PUBLISH_EXIT on a second call.
cat >"${BIN_ROOT}/npm" <<'NPM'
#!/usr/bin/env bash
printf 'publish %s\n' "$*" >>"${CALLS}"
if [ -n "${SECOND_PUBLISH_EXIT:-}" ] && [ "$(grep -c '^publish ' "${CALLS}")" -ge 2 ]; then
  exit "${SECOND_PUBLISH_EXIT}"
fi
exit "${PUBLISH_EXIT}"
NPM
chmod +x "${BIN_ROOT}/git" "${BIN_ROOT}/npm"

# run_case <codex exit> <claude exit> <publisher exit> <expected run exit>
run_case() {
  : >"${CALLS}"
  local status=0
  env \
    "PATH=${BIN_ROOT}:${PATH}" \
    "CALLS=${CALLS}" \
    "CODEX_EXIT=$1" \
    "CLAUDE_EXIT=$2" \
    "PUBLISH_EXIT=$3" \
    "MARGINLAB_DAILY_RUN_TIMESTAMP=20261117-060000" \
    "MARGINLAB_EVAL_WORKSPACE_ROOT=${FIXTURE_ROOT}/workspace" \
    "MARGINLAB_DAILY_STATE_DIR=${FIXTURE_ROOT}/state" \
    "MARGINLAB_RAW_REPOSITORY=${FIXTURE_ROOT}/raw" \
    "MARGINLAB_AUTOMATION_ROOT=${FIXTURE_ROOT}/automation" \
    "MARGINLAB_EVALUATION_ROOT=${FIXTURE_ROOT}/evals" \
    "MARGINLAB_DAILY_LOCK_FILE=${FIXTURE_ROOT}/daily.lock" \
    bash "${RUNNER_ROOT}/run.sh" --publish >"${OUTPUT}" 2>&1 || status=$?
  if [ "${status}" != "$4" ]; then
    echo "codex=$1 claude=$2 publisher=$3: expected exit $4, got ${status}" >&2
    cat "${OUTPUT}" >&2
    exit 1
  fi
  # Both trackers always run, Codex first.
  test "$(grep '^tracker ' "${CALLS}" | tr '\n' ' ')" = "tracker codex tracker claude-code "
}

publications() {
  grep -c '^publish ' "${CALLS}" || true
}

# Both finish: one publication that requires both trackers to be current.
run_case 0 0 0 0
test "$(publications)" -eq 1
if grep -q -- '--current-tracker' "${CALLS}"; then
  echo "Expected a full publication when both trackers finish" >&2
  exit 1
fi

# Codex fails, as on 2026-11-17: Claude Code still runs and is published alone.
run_case 1 0 0 1
test "$(publications)" -eq 1
grep -q -- '--publish .*--current-tracker claude-code-current$' "${CALLS}"
grep -Fq 'FAIL: Codex did not finish for 2026-11-17 (status 1); running Claude Code anyway' "${OUTPUT}"
grep -Fq 'FAIL: only Claude Code went to the site-data publisher for 2026-11-17; Codex did not finish' "${OUTPUT}"

# Claude Code fails: Codex is published alone and Claude Code's status is kept.
run_case 0 3 0 3
test "$(publications)" -eq 1
grep -q -- '--current-tracker codex-current$' "${CALLS}"
grep -Fq 'FAIL: Claude Code did not finish for 2026-11-17 (status 3)' "${OUTPUT}"

# Both fail: nothing is published.
run_case 1 2 0 1
test "$(publications)" -eq 0
grep -Fq 'FAIL: neither tracker finished for 2026-11-17; nothing was published' "${OUTPUT}"

# A publisher failure is reported, whether or not a tracker failed too.
run_case 0 0 4 4
grep -Fq 'FAIL: the site-data publisher failed for 2026-11-17 (status 4)' "${OUTPUT}"
run_case 1 0 4 4
grep -q -- '--current-tracker claude-code-current$' "${CALLS}"

# The Antigravity tracker runs only when its flag file exists, after the others
# have been published, and its own failure never stops their publication.
cat >"${RUNNER_ROOT}/run-antigravity.sh" <<'TRACKER'
printf 'tracker antigravity\n' >>"${CALLS}"
exit "${ANTIGRAVITY_EXIT}"
TRACKER

# [PUBLISH_EXIT=n] [SECOND_PUBLISH_EXIT=n] antigravity_case <codex exit> <claude exit> <antigravity exit> <expected run exit>
antigravity_case() {
  : >"${CALLS}"
  local status=0
  env \
    "PATH=${BIN_ROOT}:${PATH}" \
    "CALLS=${CALLS}" \
    "CODEX_EXIT=$1" \
    "CLAUDE_EXIT=$2" \
    "ANTIGRAVITY_EXIT=$3" \
    "PUBLISH_EXIT=${PUBLISH_EXIT:-0}" \
    "SECOND_PUBLISH_EXIT=${SECOND_PUBLISH_EXIT:-}" \
    "MARGINLAB_DAILY_RUN_TIMESTAMP=20261117-060000" \
    "MARGINLAB_EVAL_WORKSPACE_ROOT=${FIXTURE_ROOT}/workspace" \
    "MARGINLAB_DAILY_STATE_DIR=${FIXTURE_ROOT}/state" \
    "MARGINLAB_RAW_REPOSITORY=${FIXTURE_ROOT}/raw" \
    "MARGINLAB_AUTOMATION_ROOT=${FIXTURE_ROOT}/automation" \
    "MARGINLAB_EVALUATION_ROOT=${FIXTURE_ROOT}/evals" \
    "MARGINLAB_DAILY_LOCK_FILE=${FIXTURE_ROOT}/daily.lock" \
    bash "${RUNNER_ROOT}/run.sh" --publish >"${OUTPUT}" 2>&1 || status=$?
  if [ "${status}" != "$4" ]; then
    echo "codex=$1 claude=$2 antigravity=$3: expected exit $4, got ${status}" >&2
    cat "${OUTPUT}" >&2
    exit 1
  fi
}

# Without the flag file, it doesn't run.
run_case 0 0 0 0
if grep -q '^tracker antigravity' "${CALLS}"; then
  echo "Antigravity ran without ${FIXTURE_ROOT}/state/antigravity-tracker.enabled" >&2
  exit 1
fi

touch "${FIXTURE_ROOT}/state/antigravity-tracker.enabled"
# Everything finishes: Antigravity runs after the full publication, then the
# same publication runs again so its run goes out tonight.
antigravity_case 0 0 0 0
test "$(cut -d' ' -f1-2 "${CALLS}" | tr '\n' ' ')" = "tracker codex tracker claude-code publish --prefix tracker antigravity publish --prefix "
test "$(grep '^publish ' "${CALLS}" | sort -u | wc -l)" -eq 1
# A night skipped for low limits leaves the run's status alone and isn't published again.
antigravity_case 0 0 75 0
grep -Fq 'Antigravity skipped 2026-11-17; the run' "${OUTPUT}"
test "$(grep -c '^publish ' "${CALLS}")" -eq 1
# Its failure fails the run, after the others were published.
antigravity_case 0 0 5 5
test "$(grep -c '^publish ' "${CALLS}")" -eq 1
grep -Fq 'FAIL: Antigravity did not finish for 2026-11-17 (status 5)' "${OUTPUT}"
# Another tracker's failure keeps its own status, and Antigravity still runs.
antigravity_case 1 0 5 1
grep -q '^tracker antigravity' "${CALLS}"
grep -q -- '--current-tracker claude-code-current$' "${CALLS}"
# When only one tracker was published, the second publication is the same one.
antigravity_case 1 0 0 1
test "$(grep -c -- '^publish .*--current-tracker claude-code-current$' "${CALLS}")" -eq 2
# It runs even when nothing else could be published, and nothing is published.
antigravity_case 1 2 0 1
grep -q '^tracker antigravity' "${CALLS}"
test "$(grep -c '^publish ' "${CALLS}" || true)" -eq 0
# Nor is anything published again when the first publication failed.
PUBLISH_EXIT=4 antigravity_case 0 0 0 4
test "$(grep -c '^publish ' "${CALLS}")" -eq 1
# A failed second publication fails the run, and keeps a status already set.
SECOND_PUBLISH_EXIT=6 antigravity_case 0 0 0 6
grep -Fq 'FAIL: the site-data publisher failed to add Antigravity for 2026-11-17 (status 6)' "${OUTPUT}"
SECOND_PUBLISH_EXIT=6 antigravity_case 0 3 0 3
grep -Fq 'FAIL: the site-data publisher failed to add Antigravity for 2026-11-17 (status 6)' "${OUTPUT}"
