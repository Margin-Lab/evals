#!/usr/bin/env python3
"""Reject an Antigravity CLI night whose agy runs stopped on the account's limits.

When the Google account runs out of quota, agy stops the task's turn, and the
task is still graded: its tests fail and it counts against the model. A night
with such tasks would understate the model, so it is not synced or published.
The limits are checked before the night starts; this catches a task that uses
far more than usual.

Reads each task's trajectory, whose final metrics keep agy's result status and
error. Exits 0 when no task stopped on a limit, and 1 (naming the tasks) when any did.
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

LIMIT_ERROR = re.compile(
    r"quota|exhausted|resource.?exhausted|rate.?limit|credits balance|prepaid balance|limit (?:reached|exceeded)",
    re.IGNORECASE,
)


def limit_stops(run_dir: Path) -> list[str]:
    stopped = []
    for path in sorted(run_dir.glob("instances/*/trajectory.json")):
        try:
            trajectory = json.loads(path.read_text(encoding="utf-8")) or {}
        except (OSError, ValueError):
            continue
        extra = (trajectory.get("final_metrics") or {}).get("extra") or {}
        error = str(extra.get("error") or "")
        if extra.get("status") != "SUCCESS" and LIMIT_ERROR.search(error):
            stopped.append(f"{path.parent.name}: {error[:200]}")
    return stopped


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("--run", required=True, type=Path, help="The margin run output directory")
    args = parser.parse_args(argv)
    stopped = limit_stops(args.run)
    if stopped:
        print(f"Error: {len(stopped)} task(s) stopped on the account's limits, so this night's results "
              "would understate the model and are not used:", file=sys.stderr)
        for line in stopped:
            print(f"  {line}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
