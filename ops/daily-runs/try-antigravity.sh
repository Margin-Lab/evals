#!/usr/bin/env bash
# Tries the Antigravity CLI tracker's agent (agy with Gemini 4 Neon, high) on tasks
# from the daily suite, outside the daily schedule, and prints what setting up its
# tracker needs: each task's result and time, the agent's events, the tokens it
# used and the quota used. Touches no daily run: results go under ~/antigravity-trials/.
#
# Usage:
#   bash try-antigravity.sh               one quick task (about 10 minutes)
#   bash try-antigravity.sh --tasks 5     the suite's first 5 tasks
#   bash try-antigravity.sh --all         all 50 tasks, like a daily run (hours)
#   bash try-antigravity.sh --report      the latest trial's report again
#
# Sign in first and run check-antigravity-sign-in.sh.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/paths.sh"
ROOT_DIR="$(marginlab_eval_workspace_root "${SCRIPT_DIR}")"
DAILY_STATE_DIR="${MARGINLAB_DAILY_STATE_DIR:-${ROOT_DIR}/daily-runs}"
SWE_SUITES_ROOT="${MARGINLAB_SWE_SUITES_ROOT:-${ROOT_DIR}/swe-suites}"
SUITE_SOURCE="${SWE_SUITES_ROOT}/swe-bench-pro-curated-50"
EVAL_CONFIG="${MARGINLAB_ANTIGRAVITY_EVAL_CONFIG:-${DAILY_STATE_DIR}/eval-config-codex.toml}"
AGENT_CONFIG="$(cd "${SCRIPT_DIR}/../../configs/example-agent-configs/antigravity-cli-gemini-4-neon-high" && pwd)"
AGY="${MARGINLAB_AGY:-$HOME/.local/bin/agy}"
TRIALS_DIR="${MARGINLAB_ANTIGRAVITY_TRIALS_DIR:-$HOME/antigravity-trials}"
# Passed by Codex and Claude Code on every recent day, in about 3.5 minutes.
QUICK_TASK="instance_ansible__ansible-395e5e20fab9cad517243372fa3c3c5d9e09ab2a-v7eee2454f617569fd6889f2211f75bc02a35f9f8"

usage() {
  echo "Usage: $0 [--tasks N | --all | --report]" >&2
  exit 2
}

# report <trial dir> <margin run status> <seconds>
report() {
  python3 - "$1" "$2" "$3" "${EVAL_CONFIG}" <<'PY'
import collections, json, os, re, sys
from pathlib import Path

trial, status, seconds, eval_config = Path(sys.argv[1]), sys.argv[2], sys.argv[3], Path(sys.argv[4])
run = trial / "run"
print()
took = f" after {int(seconds) // 60} min {int(seconds) % 60} s" if seconds.isdigit() else ""
print(f"=== Antigravity trial {trial.name}: margin run exited {status}{took}")
if eval_config.is_file():
    settings = [line.strip() for line in eval_config.read_text(encoding="utf-8").splitlines()
                if re.search(r"concurren|parallel|timeout|retr", line)]
    print("Eval settings: " + ("; ".join(settings) or "(none matched)"))

results_path = run / "results.json"
if results_path.is_file():
    instances = json.loads(results_path.read_text(encoding="utf-8")).get("instances", [])
    states = collections.Counter(instance.get("final_state") for instance in instances)
    print("Results: " + ", ".join(f"{state} {n}" for state, n in states.most_common()))
    for instance in instances:
        usage = instance.get("usage") or {}
        reason = instance.get("infra_failure_reason")
        print(f"  {instance.get('final_state', '?'):<13} {(instance.get('runtime_ms') or 0) / 60000:5.1f} min"
              f"  in {usage.get('input_tokens', '-')} out {usage.get('output_tokens', '-')}"
              f" tools {usage.get('tool_calls', '-')}  {instance.get('case_id', '?')[:70]}"
              + (f"  [{reason}]" if reason else ""))
else:
    print(f"No results.json in {run}")

# Whole-run token totals per task come from each trajectory's final metrics.
totals = collections.Counter()
step_types = collections.Counter()
outcomes = collections.Counter()
tool_calls = 0
trajectories = sorted(run.rglob("trajectory.json")) if run.is_dir() else []
for path in trajectories:
    trajectory = json.loads(path.read_text(encoding="utf-8"))
    extra = ((trajectory or {}).get("final_metrics") or {}).get("extra") or {}
    for key, value in (extra.get("usage_totals") or {}).items():
        if isinstance(value, (int, float)):
            totals[key] += value
    step_types.update(extra.get("step_types") or {})
    tool_calls += extra.get("tool_calls") or 0
    outcomes[f"{extra.get('status', '?')}/{extra.get('finished_reason', '-')}"] += 1
print(f"Trajectories: {len(trajectories)}; agy outcomes: " + (", ".join(f"{k} {v}" for k, v in outcomes.most_common()) or "none"))
print("Tokens, all tasks: " + (", ".join(f"{key} {value:,}" for key, value in totals.most_common()) or "none reported"))
# What the same tokens would cost through the Gemini API. input_tokens is the
# uncached part of each prompt, and output_tokens already counts thinking.
prices = [float(p) for p in os.environ.get("MARGINLAB_AGY_PRICES", "3,0.15,15").split(",")]
if trajectories and totals:
    cost = (totals["input_tokens"] * prices[0] + totals["cache_read_tokens"] * prices[1]
            + totals["output_tokens"] * prices[2]) / 1e6
    per_task = cost / len(trajectories)
    print(f"At API prices (${prices[0]:g} input, ${prices[1]:g} cached, ${prices[2]:g} output per 1M tokens): "
          f"${cost:.2f} in all, ${per_task:.2f} per task, ${per_task * 50:.0f} for 50 tasks")
print(f"Tool calls: {tool_calls}; steps: " + ", ".join(f"{key} {value}" for key, value in step_types.most_common(10)))

# The Gemini limits before and after, from /usage: "Gemini Models<TAB><limit><TAB>98%<TAB><reset>".
def gemini_limits(name):
    try:
        response = json.loads((trial / name).read_text(encoding="utf-8")).get("response", "")
    except (OSError, ValueError, AttributeError):
        return {}
    found = {}
    for line in response.splitlines():
        parts = line.split("\t")
        if len(parts) >= 3 and parts[0] == "Gemini Models" and parts[2].endswith("%"):
            found[parts[1]] = (float(parts[2][:-1]), parts[3] if len(parts) > 3 else "")
    return found

before, after = gemini_limits("quota-before.json"), gemini_limits("quota-after.json")
for limit, label in (("Weekly Limit Remaining", "Weekly"), ("Five Hour Limit Remaining", "5-hour")):
    if limit not in after:
        print(f"{label} limit: not read")
        continue
    left, resets = after[limit]
    line = f"{label} limit: {left:g}% left"
    if limit in before:
        used = before[limit][0] - left
        line = f"{label} limit: {before[limit][0]:g}% -> {left:g}% left, {used:g} points for {len(trajectories)} task(s)"
        if used > 0 and trajectories:
            line += f", so a full limit covers about {100 * len(trajectories) / used:.0f} tasks like these"
    print(line + (f" (resets {resets})" if resets else ""))

# The agent's stdout, including the stream-json events, is in its terminal log. Only
# event counts are printed: the log also holds terminal control characters.
logs = sorted(run.rglob("agent_server_pty.log")) if run.is_dir() else []
kinds = collections.Counter()
for log in logs[:1]:
    for line in log.read_text(encoding="utf-8", errors="replace").splitlines():
        start = line.find('{"event"')
        if start < 0:
            continue
        try:
            event = json.loads(line[start:])
        except ValueError:
            kinds["unparsed"] += 1
            continue
        payload = event.get(event.get("event")) if isinstance(event.get(event.get("event")), dict) else event
        kinds[f"{event.get('event')}:{payload.get('step_type', '')}:{payload.get('state', '')}"] += 1
if kinds:
    print(f"Events in the first task's log: {sum(kinds.values())}; " + ", ".join(f"{k} {v}" for k, v in kinds.most_common(10)))
elif logs:
    print("The first task's terminal log has no stream-json events")
PY
}

selection="quick"
count=1
case "${1:-}" in
  "") ;;
  --tasks)
    selection="first"
    count="${2:-}"
    [[ "${count}" =~ ^[1-9][0-9]*$ ]] || usage
    ;;
  --all) selection="all" ;;
  --report)
    latest="$(ls -1d "${TRIALS_DIR}"/*/ 2> /dev/null | tail -n 1)"
    if [ -z "${latest}" ]; then
      echo "Error: no trials in ${TRIALS_DIR}" >&2
      exit 1
    fi
    report "${latest%/}" "(not rerun)" ""
    exit 0
    ;;
  -h|--help)
    sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
  *) usage ;;
esac

for needed in "${SUITE_SOURCE}/suite.toml" "${EVAL_CONFIG}" "${AGY}"; do
  if [ ! -e "${needed}" ]; then
    echo "Error: ${needed} doesn't exist" >&2
    exit 1
  fi
done
if [ ! -s "$HOME/.gemini/antigravity-cli/antigravity-oauth-token" ]; then
  echo "Error: no Antigravity sign-in saved. Sign in with: env -u DBUS_SESSION_BUS_ADDRESS ${AGY}" >&2
  exit 1
fi

TRIAL_DIR="${TRIALS_DIR}/$(date +%Y%m%d-%H%M%S)"
mkdir -p "${TRIAL_DIR}"
SUITE="${SUITE_SOURCE}"
if [ "${selection}" != "all" ]; then
  SUITE="${TRIAL_DIR}/suite"
  python3 - "${SUITE_SOURCE}" "${SUITE}" "${selection}" "${count}" "${QUICK_TASK}" <<'PY' || exit 1
import json, shutil, sys, tomllib
from pathlib import Path

source, target, selection, count, quick = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3], int(sys.argv[4]), sys.argv[5]
suite = tomllib.loads((source / "suite.toml").read_text(encoding="utf-8"))
cases = [quick] if selection == "quick" else suite["cases"][:count]
missing = [case for case in cases if not (source / "cases" / case).is_dir()]
if missing:
    sys.exit(f"Error: the suite has no case {missing[0]}")
(target / "cases").mkdir(parents=True)
for case in cases:
    shutil.copytree(source / "cases" / case, target / "cases" / case, symlinks=False)
for extra in source.iterdir():
    if extra.is_file() and extra.name != "suite.toml":
        shutil.copy2(extra, target / extra.name)
lines = [
    'kind = "test_suite"',
    f'name = {json.dumps(suite.get("name", "suite") + "-antigravity-trial")}',
    f'description = {json.dumps("Antigravity CLI trial: " + str(len(cases)) + " task(s)")}',
    "cases = [",
    *[f"  {json.dumps(case)}," for case in cases],
    "]",
]
(target / "suite.toml").write_text("\n".join(lines) + "\n", encoding="utf-8")
print(f"Trying {len(cases)} task(s): " + ", ".join(cases))
PY
else
  echo "Trying all tasks in ${SUITE_SOURCE}"
fi

# /usage answers from the account without starting a turn, so it spends no quota.
# Like check-antigravity-sign-in.sh, it runs in a new home holding only the
# sign-in and with a clean environment. From this machine's own home and
# terminal settings, agy tried to use the terminal from the background and was
# stopped there (2026-11-26).
quota() {
  local home
  home="$(mktemp -d "${TMPDIR:-/tmp}/agy-quota.XXXXXX")" || return 0
  mkdir -p "${home}/.gemini/antigravity-cli"
  cp "$HOME/.gemini/antigravity-cli/antigravity-oauth-token" "${home}/.gemini/antigravity-cli/"
  (cd "${home}" && env -i HOME="${home}" PATH=/usr/local/bin:/usr/bin:/bin TERM=dumb \
    timeout -k 10 120 "${AGY}" -p "/usage" --output-format json < /dev/null 2> /dev/null)
  rm -rf -- "${home}"
}
echo "Reading the quota before the run"
quota > "${TRIAL_DIR}/quota-before.json"

echo "Results will be in ${TRIAL_DIR}"
started="$(date +%s)"
env -u GEMINI_API_KEY -u DBUS_SESSION_BUS_ADDRESS margin run \
  --output "${TRIAL_DIR}/run" \
  --suite "${SUITE}" \
  --agent-config "${AGENT_CONFIG}" \
  --eval "${EVAL_CONFIG}" \
  --non-interactive 2>&1 | tee "${TRIAL_DIR}/margin.log"
status="${PIPESTATUS[0]}"
finished="$(date +%s)"
quota > "${TRIAL_DIR}/quota-after.json"

report "${TRIAL_DIR}" "${status}" "$((finished - started))"
exit "${status}"
