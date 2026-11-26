#!/usr/bin/env python3
"""Pin the Antigravity CLI agent config to one agy release for the whole run.

Every task installs agy in its own container, and the versioned config asks for
the "latest" release, which each container would look up on its own: a release
during the run could split it across two versions. Before the run, this reads
Google's release manifests once for the platforms the task images use, checks
that they name the same release, downloads this machine's archive and checks its
SHA-512, and writes a copy of the config naming that release and its SHA-512s.
It prints the release.

The archive paths must match PLATFORM_ARCHIVES in
configs/agent-definitions/antigravity-cli/hooks/lib/agy.js, which installs a
named release from them.
"""
from __future__ import annotations

import argparse
import copy
import hashlib
import json
import os
import platform
import re
import shutil
import sys
import time
import tomllib
import urllib.request
from pathlib import Path
from typing import Callable

MANIFEST_BASE_URL = "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests"
RELEASE_BASE_URL = "https://storage.googleapis.com/antigravity-public/antigravity-cli"
PLATFORM_ARCHIVES = {
    "linux_amd64": "linux-x64/cli_linux_x64.tar.gz",
    "linux_amd64_musl": "linux-x64-musl/cli_linux_x64_musl.tar.gz",
    "linux_arm64": "linux-arm/cli_linux_arm64.tar.gz",
    "linux_arm64_musl": "linux-arm-musl/cli_linux_arm64_musl.tar.gz",
}
RELEASE = re.compile(r"^\d+\.\d+\.\d+-\d+$")
SHA512 = re.compile(r"^[0-9a-f]{128}$")


class PinError(Exception):
    """The release couldn't be resolved or checked yet; worth trying again."""


def host_platforms() -> tuple[str, str]:
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())
    if arch is None:
        raise SystemExit(f"Error: agy has no Linux build for {platform.machine()}")
    return f"linux_{arch}", f"linux_{arch}_musl"


def fetch_json(url: str, timeout: int) -> dict:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as response:
            return json.load(response)
    except (OSError, ValueError) as error:
        raise PinError(f"couldn't read {url}: {error}") from error


def resolve_release(platforms: tuple[str, ...], fetch: Callable[[str], dict]) -> tuple[str, dict[str, str], dict[str, str]]:
    """The one release the manifests name, with each platform's archive URL and SHA-512."""
    releases: dict[str, str] = {}
    urls: dict[str, str] = {}
    sha512s: dict[str, str] = {}
    for name in platforms:
        manifest = fetch(f"{MANIFEST_BASE_URL}/{name}.json")
        url = str(manifest.get("url", ""))
        sha512 = str(manifest.get("sha512", ""))
        prefix = f"{RELEASE_BASE_URL}/"
        release = url[len(prefix):].split("/", 1)[0] if url.startswith(prefix) else ""
        if not RELEASE.match(release) or url != f"{prefix}{release}/{PLATFORM_ARCHIVES[name]}":
            raise PinError(f"the {name} manifest names an archive outside the known layout: {url!r}")
        if not SHA512.match(sha512):
            raise PinError(f"the {name} manifest has no SHA-512")
        releases[name], urls[name], sha512s[name] = release, url, sha512
    if len(set(releases.values())) != 1:
        raise PinError(f"the manifests name different releases while one is going out: {releases}")
    return next(iter(releases.values())), urls, sha512s


def check_archive(url: str, sha512: str, timeout: int) -> None:
    digest = hashlib.sha512()
    try:
        with urllib.request.urlopen(url, timeout=timeout) as response:
            for chunk in iter(lambda: response.read(1 << 20), b""):
                digest.update(chunk)
    except OSError as error:
        raise PinError(f"couldn't download {url}: {error}") from error
    if digest.hexdigest() != sha512:
        raise PinError(f"{url} doesn't match its manifest's SHA-512")


def write_pinned_config(config_dir: Path, output_dir: Path, release: str, sha512s: dict[str, str]) -> None:
    text = (config_dir / "config.toml").read_text(encoding="utf-8")
    original = tomllib.loads(text)
    definition = (config_dir / original["definition"]).resolve()
    if not (definition / "definition.toml").is_file():
        raise SystemExit(f"Error: {config_dir}/config.toml definition is not an agent definition: {definition}")

    # The copy lives elsewhere, so its definition path must be absolute.
    pinned, count = re.subn(
        r'(?m)^(\s*definition\s*=\s*)"[^"\n]*"[ \t]*$',
        lambda match: match.group(1) + json.dumps(str(definition)),
        text,
    )
    if count != 1:
        raise SystemExit(f"Error: expected one definition line in {config_dir}/config.toml; found {count}")
    table = ", ".join(f"{name} = {json.dumps(value)}" for name, value in sorted(sha512s.items()))
    pinned, count = re.subn(
        r'(?m)^(\s*)agy_version\s*=\s*"latest"[ \t]*$',
        lambda match: f'{match.group(1)}agy_version = {json.dumps(release)}\n{match.group(1)}agy_sha512 = {{ {table} }}',
        pinned,
    )
    if count != 1:
        raise SystemExit(f'Error: expected one agy_version = "latest" line in {config_dir}/config.toml; found {count}')

    expected = copy.deepcopy(original)
    expected["definition"] = str(definition)
    expected["input"]["agy_version"] = release
    expected["input"]["agy_sha512"] = dict(sha512s)
    if tomllib.loads(pinned) != expected:
        raise SystemExit(f"Error: pinning {config_dir}/config.toml changed more than the release and definition")
    shutil.copytree(config_dir, output_dir)
    (output_dir / "config.toml").write_text(pinned, encoding="utf-8")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("--config", required=True, type=Path, help="Versioned agent config directory")
    parser.add_argument("--output", required=True, type=Path, help="New directory for the pinned config")
    parser.add_argument("--wait-seconds", type=int,
                        default=int(os.environ.get("MARGINLAB_AGENT_INSTALL_WAIT_SECONDS", "3600")),
                        help="How long to keep trying before giving up (default 3600)")
    parser.add_argument("--try-seconds", type=int,
                        default=int(os.environ.get("MARGINLAB_AGENT_INSTALL_TRY_SECONDS", "600")),
                        help="Time limit for each download in a try (default 600)")
    parser.add_argument("--retry-seconds", type=int,
                        default=int(os.environ.get("MARGINLAB_AGENT_INSTALL_RETRY_SECONDS", "60")),
                        help="Pause between tries (default 60)")
    args = parser.parse_args(argv)

    config_dir = args.config.resolve()
    config = tomllib.loads((config_dir / "config.toml").read_text(encoding="utf-8"))
    if str(config.get("input", {}).get("agy_version", "")) != "latest":
        print(f'Error: {config_dir}/config.toml must ask for agy_version = "latest"', file=sys.stderr)
        return 2
    if args.output.exists():
        print(f"Error: {args.output} already exists", file=sys.stderr)
        return 2

    glibc, musl = host_platforms()
    deadline = time.monotonic() + max(args.wait_seconds, 0)
    attempt = 0
    while True:
        attempt += 1
        try:
            release, urls, sha512s = resolve_release((glibc, musl), lambda url: fetch_json(url, 60))
            # Download this machine's archive the way each task does, so a release
            # that is still uploading is waited out here, not in the tasks.
            check_archive(urls[glibc], sha512s[glibc], args.try_seconds)
            break
        except PinError as error:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                print(f"Error: agy could not be pinned after {attempt} tries over "
                      f"{args.wait_seconds // 60} minutes: {error}", file=sys.stderr)
                return 1
            pause = min(max(args.retry_seconds, 0), remaining)
            print(f"WARN: {error}; trying again in {pause:.0f}s", file=sys.stderr)
            time.sleep(pause)

    write_pinned_config(config_dir, args.output, release, sha512s)
    print(release)
    return 0


if __name__ == "__main__":
    sys.exit(main())
