#!/bin/sh
set -eu

PROMPT_FILE="/marginlab/state/fake-last-prompt.txt"
PROMPT=""
if [ -f "$PROMPT_FILE" ]; then
  PROMPT="$(cat "$PROMPT_FILE")"
fi

if printf '%s' "$PROMPT" | grep -q "\[FAKE_TEST_SLEEP\]"; then
  sleep 2
fi

if printf '%s' "$PROMPT" | grep -q "\[FAKE_TEST_FAIL\]"; then
  echo "fake eval test: requested failure"
  exit 2
fi

echo "fake eval test: success"
exit 0
