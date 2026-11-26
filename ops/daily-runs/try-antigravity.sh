#!/usr/bin/env bash
# Tries the Antigravity CLI tracker's agent (agy with Gemini 4 Neon, high) on tasks
# from the daily suite, outside the daily schedule, and prints what setting up its
# tracker needs: each task's result and time, the agent's events, and the quota used.
# Touches no daily run: results go under ~/antigravity-trials/.
#
# Usage:
#   bash try-antigravity.sh               one quick task (about 5 minutes)
#   bash try-antigravity.sh --tasks 5     the suite's first 5 tasks
#   bash try-antigravity.sh --all         all 50 tasks, like a daily run (hours)
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

selection="quick"
count=1
case "${1:-}" in
  "") ;;
  --tasks)
    selection="first"
    count="${2:-}"
    if ! [[ "${count}" =~ ^[1-9][0-9]*$ ]]; then
      echo "Usage: $0 [--tasks N | --all]" >&2
      exit 2
    fi
    ;;
  --all) selection="all" ;;
  -h|--help)
    sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
  *)
    echo "Usage: $0 [--tasks N | --all]" >&2
    exit 2
    ;;
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
quota() {
  env -u DBUS_SESSION_BUS_ADDRESS timeout 150 "${AGY}" -p "/usage" --output-format json < /dev/null 2> /dev/null
}
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

python3 - "${TRIAL_DIR}" "${status}" "$((finished - started))" "${EVAL_CONFIG}" <<'PY'
import collections, json, re, sys
from pathlib import Path

trial, status, seconds, eval_config = Path(sys.argv[1]), sys.argv[2], int(sys.argv[3]), Path(sys.argv[4])
run = trial / "run"
print()
print(f"=== Antigravity trial: margin run exited {status} after {seconds // 60} min {seconds % 60} s")
settings = [line.strip() for line in eval_config.read_text(encoding="utf-8").splitlines()
            if re.search(r"concurren|parallel|timeout|retr", line)]
print("Eval settings: " + ("; ".join(settings) or "(none matched)"))

results_path = run / "results.json"
if results_path.is_file():
    results = json.loads(results_path.read_text(encoding="utf-8"))
    instances = results.get("instances", [])
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

streams = sorted(run.rglob("agy-stream.jsonl")) if run.is_dir() else []
totals = collections.Counter()
step_types = collections.Counter()
tools = collections.Counter()
outcomes = collections.Counter()
errors = []
samples = []
for stream in streams:
    for line in stream.read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        kind = event.get("event")
        payload = event.get(kind) if isinstance(event.get(kind), dict) else event
        if kind == "step_update":
            step_types[f"{payload.get('step_type')}/{payload.get('state')}"] += 1
            info = payload.get("tool_info") or {}
            if payload.get("state") == "DONE" and (info.get("name") or payload.get("tool_name")):
                tools[info.get("name") or payload.get("tool_name")] += 1
        elif kind == "result":
            outcomes[f"{payload.get('status')}/{payload.get('finished_reason', '-')}"] += 1
            for key, value in (payload.get("usage") or {}).items():
                if isinstance(value, (int, float)):
                    totals[key] += value
            if payload.get("error"):
                errors.append(str(payload.get("error"))[:200])
        if len(streams) <= 2 and len(samples) < 12 and (kind != "step_update" or len(samples) < 10):
            samples.append(line[:500])
print(f"Event streams: {len(streams)}; run outcomes: " + (", ".join(f"{k} {v}" for k, v in outcomes.most_common()) or "none"))
print("Usage, all tasks: " + (", ".join(f"{key} {value:,}" for key, value in totals.most_common()) or "none reported"))
print("Steps: " + ", ".join(f"{key} {value}" for key, value in step_types.most_common(12)))
print("Tools: " + ", ".join(f"{key} {value}" for key, value in tools.most_common(12)))
for error in errors[:5]:
    print(f"Error: {error}")
for stderr_log in sorted(run.rglob("agy.stderr.log"))[:3]:
    tail = stderr_log.read_text(encoding="utf-8", errors="replace").strip().splitlines()[-3:]
    if tail:
        print(f"agy stderr ({stderr_log.parent.parent.parent.name[-14:]}): " + " | ".join(line[:160] for line in tail))
transcripts = list(run.rglob("agy-transcripts/*.jsonl"))
trajectories = list(run.rglob("trajectory.json"))
print(f"Transcripts saved: {len(transcripts)}; trajectories: {len(trajectories)}")
if samples:
    print("First events:")
    for sample in samples:
        print("  " + sample)
for name in ("quota-before.json", "quota-after.json"):
    text = (trial / name).read_text(encoding="utf-8").strip() if (trial / name).is_file() else ""
    print(f"{name}: {text[:1200] or '(empty)'}")
PY
exit "${status}"
