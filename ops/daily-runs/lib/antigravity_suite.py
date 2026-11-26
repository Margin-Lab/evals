#!/usr/bin/env python3
"""Build one night's Antigravity CLI sample from the daily suite.

The Antigravity tracker runs only a few tasks a night: Google AI Pro's weekly
Antigravity limit covers about 47 daily-suite tasks with Gemini 4 Neon at high
effort (measured 2026-11-26), not 50 a day. Each night takes the next tasks in
suite order, by date, so every task runs about once every 50 / count days and a
7-day window holds 7 * count different tasks. A rerun of a date picks the same
tasks, and a skipped night leaves its tasks for the next pass.

Writes a suite directory with those cases and prints their names, one per line.
"""
from __future__ import annotations

import argparse
import json
import shutil
import sys
import tomllib
from datetime import date
from pathlib import Path

# The first night of the rotation: day 0 runs the suite's first tasks.
ROTATION_START = date(2026, 11, 27)


def cases_for(cases: list[str], night: date, count: int, start: date = ROTATION_START) -> list[str]:
    if count <= 0 or count > len(cases):
        raise ValueError(f"count must be between 1 and {len(cases)}")
    day = (night - start).days
    first = (day * count) % len(cases)
    return [cases[(first + offset) % len(cases)] for offset in range(count)]


def write_suite(source: Path, target: Path, chosen: list[str], night: date) -> None:
    suite = tomllib.loads((source / "suite.toml").read_text(encoding="utf-8"))
    (target / "cases").mkdir(parents=True)
    for case in chosen:
        shutil.copytree(source / "cases" / case, target / "cases" / case, symlinks=False)
    # Suite-level files, such as the preamble prompt, apply to every case.
    for extra in source.iterdir():
        if extra.is_file() and extra.name != "suite.toml":
            shutil.copy2(extra, target / extra.name)
    lines = [
        'kind = "test_suite"',
        f'name = {json.dumps(str(suite.get("name", "suite")) + "-antigravity-" + night.isoformat())}',
        f'description = {json.dumps(f"Antigravity CLI tracker sample for {night.isoformat()}: {len(chosen)} tasks")}',
        "cases = [",
        *[f"  {json.dumps(case)}," for case in chosen],
        "]",
    ]
    (target / "suite.toml").write_text("\n".join(lines) + "\n", encoding="utf-8")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("--source", required=True, type=Path, help="The full daily suite")
    parser.add_argument("--target", required=True, type=Path, help="New directory for the night's suite")
    parser.add_argument("--date", required=True, help="The night's target date, YYYY-MM-DD")
    parser.add_argument("--count", required=True, type=int, help="Tasks per night")
    args = parser.parse_args(argv)

    night = date.fromisoformat(args.date)
    if night < ROTATION_START:
        print(f"Error: {night} is before the rotation starts on {ROTATION_START}", file=sys.stderr)
        return 2
    cases = tomllib.loads((args.source / "suite.toml").read_text(encoding="utf-8"))["cases"]
    try:
        chosen = cases_for(cases, night, args.count)
    except ValueError as error:
        print(f"Error: {error}", file=sys.stderr)
        return 2
    missing = [case for case in chosen if not (args.source / "cases" / case).is_dir()]
    if missing:
        print(f"Error: {args.source} has no case {missing[0]}", file=sys.stderr)
        return 1
    if args.target.exists():
        print(f"Error: {args.target} already exists", file=sys.stderr)
        return 2
    write_suite(args.source, args.target, chosen, night)
    print("\n".join(chosen))
    return 0


if __name__ == "__main__":
    sys.exit(main())
