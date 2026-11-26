#!/usr/bin/env python3
"""Read the Google account's Antigravity limits, and say whether a night's sample fits.

Runs `agy -p /usage`, which answers from the account without starting a turn
and spends no quota, in a new home that holds only the saved sign-in and with a
clean environment. From the machine's own home and terminal settings, agy tried
to use the terminal from the background and was stopped there (2026-11-26).

Prints the Gemini models' weekly and 5-hour limits left, in percent. Exits 0 when
both are at least the given minimums, 75 when either is lower, and 1 when the
limits couldn't be read (a sign-in that no longer works shows up here).
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

TOKEN_RELATIVE_PATH = Path(".gemini/antigravity-cli/antigravity-oauth-token")
WEEKLY = "Weekly Limit Remaining"
FIVE_HOUR = "Five Hour Limit Remaining"
TIMEOUT_SECONDS = 150
SKIP = 75


def parse_limits(text: str) -> dict[str, tuple[float, str]]:
    """Gemini limits from /usage's JSON: {limit name: (percent left, reset time)}."""
    response = json.loads(text).get("response", "")
    limits: dict[str, tuple[float, str]] = {}
    for line in str(response).splitlines():
        parts = line.split("\t")
        if len(parts) >= 3 and parts[0] == "Gemini Models" and parts[2].endswith("%"):
            limits[parts[1]] = (float(parts[2][:-1]), parts[3] if len(parts) > 3 else "")
    return limits


def read_limits(agy: Path, home: Path) -> dict[str, tuple[float, str]]:
    token = home / TOKEN_RELATIVE_PATH
    if not token.is_file():
        raise RuntimeError(f"no saved Antigravity sign-in at {token}")
    with tempfile.TemporaryDirectory(prefix="agy-limits-") as tmp:
        clean_home = Path(tmp)
        (clean_home / TOKEN_RELATIVE_PATH).parent.mkdir(parents=True)
        shutil.copy2(token, clean_home / TOKEN_RELATIVE_PATH)
        env = {"HOME": str(clean_home), "PATH": "/usr/local/bin:/usr/bin:/bin", "TERM": "dumb"}
        try:
            result = subprocess.run(
                [str(agy), "-p", "/usage", "--output-format", "json"],
                cwd=clean_home, env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True,
                timeout=TIMEOUT_SECONDS,
            )
        except subprocess.TimeoutExpired as error:
            raise RuntimeError(f"agy -p /usage didn't answer in {TIMEOUT_SECONDS} seconds") from error
    if result.returncode != 0:
        last = (result.stderr or result.stdout).strip().splitlines()[-1:] or ["no output"]
        raise RuntimeError(f"agy -p /usage failed with status {result.returncode}: {last[0][:300]}")
    try:
        limits = parse_limits(result.stdout)
    except (ValueError, AttributeError) as error:
        raise RuntimeError(f"agy -p /usage printed something other than JSON: {result.stdout[:200]!r}") from error
    for name in (WEEKLY, FIVE_HOUR):
        if name not in limits:
            raise RuntimeError(f"agy -p /usage showed no Gemini {name.lower()}")
    return limits


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("--agy", type=Path, default=Path(os.environ.get("HOME", "~")) / ".local/bin/agy")
    parser.add_argument("--home", type=Path, default=Path(os.environ.get("HOME", "~")))
    parser.add_argument("--min-weekly", type=float, required=True, help="Percent of the weekly limit needed")
    parser.add_argument("--min-five-hour", type=float, required=True, help="Percent of the 5-hour limit needed")
    args = parser.parse_args(argv)

    try:
        limits = read_limits(args.agy, args.home)
    except RuntimeError as error:
        print(f"Error: couldn't read the Antigravity limits: {error}", file=sys.stderr)
        return 1
    weekly, weekly_reset = limits[WEEKLY]
    five_hour, five_hour_reset = limits[FIVE_HOUR]
    print(f"Gemini limits left: weekly {weekly:g}% (resets {weekly_reset}), 5-hour {five_hour:g}% (resets {five_hour_reset})")
    if weekly < args.min_weekly or five_hour < args.min_five_hour:
        print(
            f"Not enough left for tonight's sample: it needs {args.min_weekly:g}% of the weekly limit "
            f"and {args.min_five_hour:g}% of the 5-hour limit",
            file=sys.stderr,
        )
        return SKIP
    return 0


if __name__ == "__main__":
    sys.exit(main())
