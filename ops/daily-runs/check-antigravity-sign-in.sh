#!/usr/bin/env bash
# Checks this machine's Antigravity CLI (agy) sign-in the way each task uses it:
# the saved sign-in copied alone into a new home, and into a container. Also lists
# the models the account can use and shows its quota. Spends no quota.
#
# Sign in first, once. Open the link agy prints on your own computer, sign in with
# the Google account the tracker uses, and paste the code back:
#   env -u DBUS_SESSION_BUS_ADDRESS ~/.local/bin/agy
# (Without a D-Bus session bus, agy saves the sign-in to a file that the runner can
# copy into each task, instead of a system keyring.)
#
# Usage: bash check-antigravity-sign-in.sh
set -uo pipefail

AGY="${MARGINLAB_AGY:-$HOME/.local/bin/agy}"
TOKEN_REL=".gemini/antigravity-cli/antigravity-oauth-token"
TOKEN="$HOME/$TOKEN_REL"
IMAGE="${MARGINLAB_AGY_CHECK_IMAGE:-debian:bookworm-slim}"
failures=0
ok() { printf 'ok    %s\n' "$*"; }
warn() { printf 'WARN  %s\n' "$*"; }
fail() { printf 'FAIL  %s\n' "$*"; failures=$((failures + 1)); }

work="$(mktemp -d "${TMPDIR:-/tmp}/agy-check.XXXXXX")"
trap 'rm -rf -- "${work}"' EXIT

# Prints a /usage answer's JSON, shortened, and returns non-zero for an error answer.
show_usage() {
  python3 -c '
import json, sys
text = sys.stdin.read().strip()
try:
    data = json.loads(text)
except ValueError:
    print("      (not JSON) " + text[:300]); sys.exit(1)
if isinstance(data, dict) and data.get("status") in ("ERROR", "INVALID"):
    print("      " + str(data.get("error") or data)[:300]); sys.exit(1)
print("      " + json.dumps(data)[:1500])
'
}

if [ ! -x "${AGY}" ]; then
  fail "agy isn't installed at ${AGY}. Install it: curl -fsSL https://antigravity.google/cli/install.sh | bash"
  exit 1
fi
ok "agy $("${AGY}" --version 2>/dev/null | head -1) at ${AGY}"

if [ ! -s "${TOKEN}" ]; then
  fail "No saved sign-in at ~/${TOKEN_REL}. Sign in with: env -u DBUS_SESSION_BUS_ADDRESS ${AGY}"
  exit 1
fi
format="$(python3 - "${TOKEN}" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("not JSON")
else:
    print("JSON with keys " + ", ".join(sorted(data)) if isinstance(data, dict) else "JSON")
PY
)"
ok "sign-in saved at ~/${TOKEN_REL} ($(stat -c '%s bytes, mode %a' "${TOKEN}"); ${format})"

# 1. A new home with only the sign-in, like each task's home.
fresh="${work}/home"
mkdir -p "${fresh}/$(dirname "${TOKEN_REL}")" "${work}/workspace"
cp "${TOKEN}" "${fresh}/${TOKEN_REL}"
if usage="$(cd "${work}/workspace" && env -i HOME="${fresh}" PATH=/usr/local/bin:/usr/bin:/bin TERM=dumb \
    timeout 150 "${AGY}" -p "/usage" --output-format json < /dev/null 2> "${work}/usage.err")" &&
  printf '%s' "${usage}" | show_usage > "${work}/usage.txt"; then
  ok "the sign-in works copied alone into a new home. Quota now:"
  cat "${work}/usage.txt"
else
  fail "the sign-in didn't work copied into a new home:"
  [ -s "${work}/usage.txt" ] && cat "${work}/usage.txt"
  grep -v -e '^\s*https://accounts.google.com' "${work}/usage.err" | tail -n 4 | sed 's/^/      /'
fi

# 2. A container: another machine ID, hostname and user, as in a task.
if ! command -v docker > /dev/null 2>&1; then
  warn "docker isn't available, so the container check was skipped"
elif container_usage="$(timeout 300 docker run --rm \
    -v "${TOKEN}:/sign-in:ro" -v "${AGY}:/usr/local/bin/agy:ro" \
    -v /etc/ssl/certs/ca-certificates.crt:/etc/ssl/certs/ca-certificates.crt:ro \
    -e HOME=/root -w /tmp "${IMAGE}" sh -c \
    "mkdir -p /root/$(dirname "${TOKEN_REL}") && cp /sign-in /root/${TOKEN_REL} && agy -p /usage --output-format json" \
    < /dev/null 2> "${work}/docker.err")" &&
  printf '%s' "${container_usage}" | show_usage > /dev/null; then
  ok "the sign-in works in a ${IMAGE} container"
else
  fail "the sign-in didn't work in a ${IMAGE} container:"
  grep -v -e '^\s*https://accounts.google.com' "${work}/docker.err" | tail -n 4 | sed 's/^/      /'
fi

# 3. The models this account can pick with --model.
if models="$(cd "${work}/workspace" && env -i HOME="${fresh}" PATH=/usr/local/bin:/usr/bin:/bin TERM=dumb \
    timeout 150 "${AGY}" models < /dev/null 2> "${work}/models.err")"; then
  ok "models this account can use:"
  printf '%s\n' "${models}" | sed 's/^/      /'
  if printf '%s\n' "${models}" | grep -q 'gemini-4-neon-high'; then
    ok "gemini-4-neon-high, the tracker's model, is available"
  else
    warn "gemini-4-neon-high isn't in the list; the tracker's model needs changing"
  fi
else
  fail "agy models failed: $(tail -n 2 "${work}/models.err")"
fi

if [ "${failures}" -eq 0 ]; then
  echo "All checks passed."
else
  echo "${failures} check(s) failed."
fi
exit "${failures}"
