#!/usr/bin/env python3
"""Pin a daily agent config to one CLI version for the whole run.

The versioned daily configs ask for the "latest" CLI, and every task installs it
in its own container. A release during a run then splits the run across two CLI
versions, and while a release is still uploading its platform builds, installs
fail outright. On 2026-11-17, @openai/codex 0.169.1 became "latest" at 22:26 UTC
and its Linux build reached npm at 22:28; seven Codex tasks failed to install in
those minutes and the day could not be published.

Before the run, this resolves "latest" once, checks that the version installs and
runs on this machine (waiting out a release that is still uploading), and writes a
copy of the agent config that asks for exactly that version. It prints the version.

Each npm command in a try has its own time limit, well inside the whole wait, so a
download that stalls is dropped and tried again. On 2026-11-22, the first install
of each CLI stalled for the full 15-minute wait, so neither tracker ran that day.
"""
from __future__ import annotations

import argparse
import copy
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import tomllib
from pathlib import Path

VERSION = re.compile(r"^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.+-]+)?$")
PROBE_TIMEOUT_SECONDS = 120
# Each task's container asks npm's public registry with an empty cache and no
# npm settings of this machine. Ask the same way, with a new empty cache on
# every try: with prefer-offline set, npm answers from any copy it has saved,
# and one machine pinned a Claude Code release eleven days old that way.
REGISTRY = "https://registry.npmjs.org/"


def npm_options(cache: Path) -> list[str]:
    return [f"--registry={REGISTRY}", "--cache", str(cache), "--prefer-online"]


class InstallError(Exception):
    """A version could not be resolved or installed yet; worth trying again."""


def last_line(text: str) -> str:
    lines = [line.strip() for line in (text or "").splitlines() if line.strip()]
    return lines[-1] if lines else "no output"


def resolve_latest(package: str, cache: Path, timeout: int) -> str:
    try:
        result = subprocess.run(
            ["npm", "view", f"{package}@latest", "version", *npm_options(cache)],
            capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=timeout,
        )
    except subprocess.TimeoutExpired as error:
        raise InstallError(f"npm view {package}@latest timed out") from error
    version = result.stdout.strip()
    if result.returncode != 0 or not VERSION.match(version):
        raise InstallError(f"npm view {package}@latest failed: {last_line(result.stderr or result.stdout)}")
    return version


def check_install(package: str, version: str, binary: str, cache: Path, timeout: int) -> None:
    """Install the version the way each task does, then run its --version."""
    with tempfile.TemporaryDirectory(prefix="marginlab-agent-check-") as tmp:
        prefix = Path(tmp) / "prefix"
        home = Path(tmp) / "home"
        home.mkdir()
        try:
            install = subprocess.run(
                ["npm", "install", "--global", "--prefix", str(prefix), *npm_options(cache),
                 "--no-audit", "--no-fund", f"{package}@{version}"],
                capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=timeout,
            )
        except subprocess.TimeoutExpired as error:
            raise InstallError(f"npm install {package}@{version} timed out") from error
        if install.returncode != 0:
            raise InstallError(f"npm install {package}@{version} failed: {last_line(install.stderr)}")
        binary_path = prefix / "bin" / binary
        if not binary_path.exists():
            raise InstallError(f"npm install {package}@{version} did not create bin/{binary}")
        # A throwaway home keeps the probe away from this machine's real logins.
        env = {key: value for key, value in os.environ.items() if key not in {"CODEX_HOME", "CLAUDE_CONFIG_DIR"}}
        env["HOME"] = str(home)
        try:
            probe = subprocess.run(
                [str(binary_path), "--version"],
                capture_output=True, text=True, stdin=subprocess.DEVNULL, env=env, timeout=PROBE_TIMEOUT_SECONDS,
            )
        except subprocess.TimeoutExpired as error:
            raise InstallError(f"{binary} --version timed out after installing {package}@{version}") from error
        if probe.returncode != 0:
            raise InstallError(
                f"{binary} --version failed after installing {package}@{version}: "
                f"{last_line(probe.stderr or probe.stdout)}"
            )
        if version not in probe.stdout:
            raise InstallError(f"{binary} --version printed {last_line(probe.stdout)!r}, not {version}")


def write_pinned_config(config_dir: Path, output_dir: Path, version_input: str, version: str) -> None:
    text = (config_dir / "config.toml").read_text(encoding="utf-8")
    original = tomllib.loads(text)
    definition = (config_dir / original["definition"]).resolve()
    if not (definition / "definition.toml").is_file():
        raise SystemExit(f"Error: {config_dir}/config.toml definition is not an agent definition: {definition}")

    # The copy lives elsewhere, so its definition path must be absolute.
    pinned_text, count = re.subn(
        r'(?m)^(\s*definition\s*=\s*)"[^"\n]*"[ \t]*$',
        lambda match: match.group(1) + json.dumps(str(definition)),
        text,
    )
    if count != 1:
        raise SystemExit(f"Error: expected one definition line in {config_dir}/config.toml; found {count}")
    if original["input"][version_input] != version:
        pinned_text, count = re.subn(
            rf'(?m)^(\s*{re.escape(version_input)}\s*=\s*)"latest"[ \t]*$',
            lambda match: match.group(1) + json.dumps(version),
            pinned_text,
        )
        if count != 1:
            raise SystemExit(
                f'Error: expected one {version_input} = "latest" line in {config_dir}/config.toml; found {count}'
            )

    expected = copy.deepcopy(original)
    expected["definition"] = str(definition)
    expected["input"][version_input] = version
    if tomllib.loads(pinned_text) != expected:
        raise SystemExit(f"Error: pinning {config_dir}/config.toml changed more than the version and definition")

    shutil.copytree(config_dir, output_dir)
    (output_dir / "config.toml").write_text(pinned_text, encoding="utf-8")


def parse_arguments(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("--config", required=True, type=Path, help="Versioned agent config directory")
    parser.add_argument("--version-input", required=True, help="Config input that names the CLI version")
    parser.add_argument("--package", required=True, help="npm package the agent definition installs")
    parser.add_argument("--binary", required=True, help="Executable the package installs under bin/")
    parser.add_argument("--output", required=True, type=Path, help="New directory for the pinned config")
    parser.add_argument(
        "--wait-seconds", type=int,
        default=int(os.environ.get("MARGINLAB_AGENT_INSTALL_WAIT_SECONDS", "3600")),
        help="How long to keep trying before giving up (default 3600)",
    )
    parser.add_argument(
        "--try-seconds", type=int,
        default=int(os.environ.get("MARGINLAB_AGENT_INSTALL_TRY_SECONDS", "600")),
        help="Time limit for each npm command in a try (default 600)",
    )
    parser.add_argument(
        "--retry-seconds", type=int,
        default=int(os.environ.get("MARGINLAB_AGENT_INSTALL_RETRY_SECONDS", "60")),
        help="Pause between tries (default 60)",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_arguments(argv)
    config_dir = args.config.resolve()
    config = tomllib.loads((config_dir / "config.toml").read_text(encoding="utf-8"))
    requested = str(config.get("input", {}).get(args.version_input, "")).strip()
    if not requested:
        print(f"Error: {config_dir}/config.toml has no input.{args.version_input}", file=sys.stderr)
        return 2
    if args.output.exists():
        print(f"Error: {args.output} already exists", file=sys.stderr)
        return 2

    deadline = time.monotonic() + max(args.wait_seconds, 0)
    attempt = 0
    while True:
        attempt += 1
        try:
            # Resolve again on every try, so a broken release that is replaced is picked up.
            with tempfile.TemporaryDirectory(prefix="marginlab-agent-npm-cache-") as cache:
                version = (resolve_latest(args.package, Path(cache), args.try_seconds)
                           if requested == "latest" else requested)
                check_install(args.package, version, args.binary, Path(cache), args.try_seconds)
            break
        except InstallError as error:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                print(
                    f"Error: {args.package} could not be installed after {attempt} tries "
                    f"over {args.wait_seconds // 60} minutes: {error}",
                    file=sys.stderr,
                )
                return 1
            pause = min(max(args.retry_seconds, 0), remaining)
            print(f"WARN: {error}; trying again in {pause:.0f}s", file=sys.stderr)
            time.sleep(pause)

    write_pinned_config(config_dir, args.output, args.version_input, version)
    print(version)
    return 0


if __name__ == "__main__":
    sys.exit(main())
