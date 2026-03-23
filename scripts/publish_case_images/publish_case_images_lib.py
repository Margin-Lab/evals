#!/usr/bin/env python3
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
import re
import subprocess
import sys
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

SUCCESS_STATUSES = {"published", "skipped_existing", "patched"}
IMAGE_LINE_RE = re.compile(r'^\s*image\s*=\s*".*"\s*$')
DESCRIPTION_LINE_RE = re.compile(r'^\s*description\s*=\s*".*"\s*$')


def utcnow_iso() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def sha256_file(path: Path, digest: "hashlib._Hash") -> None:
    with path.open("rb") as handle:
        while True:
            chunk = handle.read(1024 * 1024)
            if not chunk:
                return
            digest.update(chunk)


def hash_context_dir(path: Path) -> str:
    if not path.is_dir():
        raise FileNotFoundError(f"context directory not found: {path}")

    digest = hashlib.sha256()

    def walk(current: Path) -> None:
        for child in sorted(current.iterdir(), key=lambda item: item.name):
            rel = child.relative_to(path).as_posix()
            if child.is_symlink():
                digest.update(b"L\0")
                digest.update(rel.encode("utf-8"))
                digest.update(b"\0")
                digest.update(os.readlink(child).encode("utf-8"))
                digest.update(b"\0")
                continue
            if child.is_dir():
                digest.update(b"D\0")
                digest.update(rel.encode("utf-8"))
                digest.update(b"\0")
                walk(child)
                continue
            if child.is_file():
                digest.update(b"F\0")
                digest.update(rel.encode("utf-8"))
                digest.update(b"\0")
                sha256_file(child, digest)
                digest.update(b"\0")
                continue
            raise ValueError(f"unsupported filesystem entry in context: {child}")

    walk(path)
    return digest.hexdigest()


def sanitize_repository_component(value: str) -> str:
    normalized = value.strip().lower()
    normalized = re.sub(r"[^a-z0-9._-]+", "-", normalized)
    normalized = re.sub(r"[-._]{2,}", "-", normalized)
    normalized = normalized.strip("._-")
    return normalized or "case"


def lock_path_for(path: Path) -> Path:
    return path.with_name(path.name + ".lock")


@contextmanager
def exclusive_lock(path: Path):
    lock_path = lock_path_for(path)
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    with lock_path.open("a+", encoding="utf-8") as handle:
        fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)


def load_manifest(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {"version": 1, "entries": []}
    data = json.loads(path.read_text())
    if not isinstance(data, dict):
        raise ValueError(f"manifest {path} must be a JSON object")
    entries = data.setdefault("entries", [])
    if not isinstance(entries, list):
        raise ValueError(f"manifest {path} entries must be a JSON array")
    data.setdefault("version", 1)
    return data


def write_manifest(path: Path, data: dict[str, Any]) -> None:
    data["entries"] = sorted(
        data.get("entries", []),
        key=lambda entry: (
            str(entry.get("suite", "")),
            str(entry.get("case_id", "")),
        ),
    )
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + "\n")


def manifest_entry(data: dict[str, Any], suite: str, case_id: str) -> dict[str, Any] | None:
    for entry in data.get("entries", []):
        if entry.get("suite") == suite and entry.get("case_id") == case_id:
            return entry
    return None


def upsert_manifest(path: Path, suite: str, case_id: str, updates: dict[str, str]) -> None:
    with exclusive_lock(path):
        data = load_manifest(path)
        entry = manifest_entry(data, suite, case_id)
        if entry is None:
            entry = {"suite": suite, "case_id": case_id}
            data["entries"].append(entry)
        entry["suite"] = suite
        entry["case_id"] = case_id
        for key, value in updates.items():
            entry[key] = value
        entry["updated_at"] = utcnow_iso()
        write_manifest(path, data)


def manifest_resume_info(path: Path, suite: str, case_id: str, context_hash: str) -> tuple[str, str, str] | None:
    with exclusive_lock(path):
        data = load_manifest(path)
        entry = manifest_entry(data, suite, case_id)
        if entry is None:
            return None
        if entry.get("context_hash") != context_hash:
            return None
        status = str(entry.get("status", "")).strip()
        digest_ref = str(entry.get("digest_ref", "")).strip()
        if status not in SUCCESS_STATUSES or not digest_ref:
            return None
        publish_status = str(entry.get("publish_status", "")).strip() or status
        return status, digest_ref, publish_status


def init_manifest(path: Path) -> None:
    with exclusive_lock(path):
        write_manifest(path, load_manifest(path))


def manifest_count(path: Path, status: str | None = None, publish_status: str | None = None) -> int:
    with exclusive_lock(path):
        data = load_manifest(path)
    entries = data.get("entries", [])
    count = 0
    for entry in entries:
        entry_status = str(entry.get("status", "")).strip()
        entry_publish_status = str(entry.get("publish_status", "")).strip()
        if status is not None and entry_status != status:
            continue
        if publish_status is not None and entry_publish_status != publish_status:
            continue
        count += 1
    return count


def manifest_success_records(path: Path) -> list[tuple[str, str, str, str, str]]:
    with exclusive_lock(path):
        data = load_manifest(path)
    records: list[tuple[str, str, str, str, str]] = []
    for entry in data.get("entries", []):
        status = str(entry.get("status", "")).strip()
        digest_ref = str(entry.get("digest_ref", "")).strip()
        if status not in SUCCESS_STATUSES or not digest_ref:
            continue
        records.append(
            (
                str(entry.get("suite", "")).strip(),
                str(entry.get("case_id", "")).strip(),
                str(entry.get("case_toml_path", "")).strip(),
                digest_ref,
                str(entry.get("publish_status", "")).strip() or status,
            )
        )
    records.sort(key=lambda item: (item[0], item[1]))
    return records


def load_cleanup_state(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {
            "version": 1,
            "pending_refs": [],
            "builds_since_cleanup": 0,
            "cleanup_runs": 0,
        }
    data = json.loads(path.read_text())
    if not isinstance(data, dict):
        raise ValueError(f"cleanup state {path} must be a JSON object")
    pending_refs = data.setdefault("pending_refs", [])
    if not isinstance(pending_refs, list):
        raise ValueError(f"cleanup state {path} pending_refs must be a JSON array")
    data["builds_since_cleanup"] = int(data.get("builds_since_cleanup", 0))
    data["cleanup_runs"] = int(data.get("cleanup_runs", 0))
    data.setdefault("version", 1)
    return data


def write_cleanup_state(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + "\n")


def ordered_unique(items: list[str]) -> list[str]:
    return list(dict.fromkeys(item for item in items if item))


def init_cleanup_state(path: Path) -> None:
    with exclusive_lock(path):
        write_cleanup_state(path, load_cleanup_state(path))


def cleanup_register_build(path: Path, threshold: int, refs: list[str]) -> list[str]:
    if threshold <= 0:
        return []
    with exclusive_lock(path):
        data = load_cleanup_state(path)
        pending_refs = list(data.get("pending_refs", []))
        pending_refs.extend(ref for ref in refs if ref)
        data["pending_refs"] = pending_refs
        data["builds_since_cleanup"] = int(data.get("builds_since_cleanup", 0)) + 1
        claimed_refs: list[str] = []
        if data["builds_since_cleanup"] >= threshold:
            claimed_refs = ordered_unique([str(ref).strip() for ref in pending_refs if str(ref).strip()])
            data["pending_refs"] = []
            data["builds_since_cleanup"] = 0
            data["cleanup_runs"] = int(data.get("cleanup_runs", 0)) + 1
        write_cleanup_state(path, data)
    return claimed_refs


def cleanup_register_build_trigger(path: Path, threshold: int) -> bool:
    if threshold <= 0:
        return False
    with exclusive_lock(path):
        data = load_cleanup_state(path)
        data["builds_since_cleanup"] = int(data.get("builds_since_cleanup", 0)) + 1
        triggered = data["builds_since_cleanup"] >= threshold
        if triggered:
            data["builds_since_cleanup"] = 0
            data["cleanup_runs"] = int(data.get("cleanup_runs", 0)) + 1
        write_cleanup_state(path, data)
    return triggered


def cleanup_drain(path: Path) -> list[str]:
    with exclusive_lock(path):
        data = load_cleanup_state(path)
        claimed_refs = ordered_unique([str(ref).strip() for ref in data.get("pending_refs", []) if str(ref).strip()])
        if claimed_refs:
            data["pending_refs"] = []
            data["builds_since_cleanup"] = 0
            data["cleanup_runs"] = int(data.get("cleanup_runs", 0)) + 1
            write_cleanup_state(path, data)
            return claimed_refs
        write_cleanup_state(path, data)
    return []


def cleanup_run_count(path: Path) -> int:
    with exclusive_lock(path):
        data = load_cleanup_state(path)
    return int(data.get("cleanup_runs", 0))


def repository_from_reference(reference: str) -> str:
    trimmed = reference.strip()
    if "@" in trimmed:
        trimmed = trimmed.split("@", 1)[0]
    last_colon = trimmed.rfind(":")
    last_slash = trimmed.rfind("/")
    if last_colon > last_slash:
        return trimmed[:last_colon]
    return trimmed


def parse_remote_digest_payload(payload: Any, reference: str, platform: str) -> str:
    wanted_os = ""
    wanted_arch = ""
    if "/" in platform:
        wanted_os, wanted_arch = platform.split("/", 1)

    entries: list[dict[str, Any]]
    if isinstance(payload, list):
        entries = [entry for entry in payload if isinstance(entry, dict)]
    elif isinstance(payload, dict):
        entries = [payload]
    else:
        raise ValueError("unexpected docker manifest payload")

    def choose(candidates: list[dict[str, Any]]) -> dict[str, Any]:
        if not candidates:
            raise ValueError("docker manifest payload contained no entries")
        for entry in candidates:
            descriptor = entry.get("Descriptor") or {}
            platform_data = descriptor.get("platform") or entry.get("Platform") or {}
            os_name = str(platform_data.get("os", "")).strip().lower()
            arch_name = str(platform_data.get("architecture", "")).strip().lower()
            if os_name == wanted_os and arch_name == wanted_arch:
                return entry
        return candidates[0]

    chosen = choose(entries)
    descriptor = chosen.get("Descriptor") or {}
    digest = str(descriptor.get("digest", "")).strip()
    if not digest:
        digest = str(chosen.get("Digest", "")).strip()
    if not digest:
        ref = str(chosen.get("Ref", "")).strip()
        if "@sha256:" in ref:
            digest = "sha256:" + ref.split("@sha256:", 1)[1]
    if not digest.startswith("sha256:"):
        raise ValueError(f"docker manifest payload for {reference} did not contain a sha256 digest")
    return f"{repository_from_reference(reference)}@{digest}"


def remote_digest(docker_bin: str, reference: str, platform: str, timeout_seconds: int) -> str:
    command = [docker_bin, "manifest", "inspect", "-v", reference]
    result = subprocess.run(
        command,
        capture_output=True,
        text=True,
        timeout=timeout_seconds,
        check=False,
    )
    if result.returncode != 0:
        stderr = result.stderr.strip()
        stdout = result.stdout.strip()
        message = stderr or stdout or f"docker manifest inspect failed for {reference}"
        raise RuntimeError(message)
    payload = json.loads(result.stdout)
    return parse_remote_digest_payload(payload, reference, platform)


def patch_case_file(path: Path, digest_ref: str, write: bool) -> str:
    original = path.read_text()
    lines = original.splitlines(keepends=True)
    newline = "\n"
    for line in lines:
        if line.endswith("\r\n"):
            newline = "\r\n"
            break
        if line.endswith("\n"):
            newline = "\n"
            break
    image_line = f'image = "{digest_ref}"{newline}'

    for idx, line in enumerate(lines):
        if IMAGE_LINE_RE.match(line):
            if line == image_line:
                return "unchanged"
            lines[idx] = image_line
            updated = "".join(lines)
            if write:
                path.write_text(updated)
            return "replaced"

    insert_at = None
    for idx, line in enumerate(lines):
        if DESCRIPTION_LINE_RE.match(line):
            insert_at = idx + 1
            break
    if insert_at is None:
        for idx, line in enumerate(lines):
            if line.lstrip().startswith("["):
                insert_at = idx
                break
    if insert_at is None:
        insert_at = len(lines)

    lines.insert(insert_at, image_line)
    updated = "".join(lines)
    if write:
        path.write_text(updated)
    return "inserted"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Helpers for publish-case-images.sh")
    subparsers = parser.add_subparsers(dest="command", required=True)

    hash_parser = subparsers.add_parser("hash-context")
    hash_parser.add_argument("--path", required=True)

    sanitize_parser = subparsers.add_parser("sanitize-repo-component")
    sanitize_parser.add_argument("--value", required=True)

    init_parser = subparsers.add_parser("manifest-init")
    init_parser.add_argument("--path", required=True)

    upsert_parser = subparsers.add_parser("manifest-upsert")
    upsert_parser.add_argument("--path", required=True)
    upsert_parser.add_argument("--suite", required=True)
    upsert_parser.add_argument("--case-id", required=True)
    upsert_parser.add_argument("--set", dest="sets", action="append", default=[])

    resume_parser = subparsers.add_parser("manifest-resume")
    resume_parser.add_argument("--path", required=True)
    resume_parser.add_argument("--suite", required=True)
    resume_parser.add_argument("--case-id", required=True)
    resume_parser.add_argument("--context-hash", required=True)

    count_parser = subparsers.add_parser("manifest-count")
    count_parser.add_argument("--path", required=True)
    count_parser.add_argument("--status")
    count_parser.add_argument("--publish-status")

    success_parser = subparsers.add_parser("manifest-success-records")
    success_parser.add_argument("--path", required=True)

    remote_parser = subparsers.add_parser("remote-digest")
    remote_parser.add_argument("--docker-bin", required=True)
    remote_parser.add_argument("--reference", required=True)
    remote_parser.add_argument("--platform", required=True)
    remote_parser.add_argument("--timeout", required=True, type=int)

    patch_parser = subparsers.add_parser("patch-case")
    patch_parser.add_argument("--path", required=True)
    patch_parser.add_argument("--digest-ref", required=True)
    patch_parser.add_argument("--write", action="store_true")

    cleanup_init_parser = subparsers.add_parser("cleanup-init")
    cleanup_init_parser.add_argument("--path", required=True)

    cleanup_register_parser = subparsers.add_parser("cleanup-register-build")
    cleanup_register_parser.add_argument("--path", required=True)
    cleanup_register_parser.add_argument("--threshold", required=True, type=int)
    cleanup_register_parser.add_argument("--ref", dest="refs", action="append", default=[])

    cleanup_trigger_parser = subparsers.add_parser("cleanup-register-build-trigger")
    cleanup_trigger_parser.add_argument("--path", required=True)
    cleanup_trigger_parser.add_argument("--threshold", required=True, type=int)

    cleanup_drain_parser = subparsers.add_parser("cleanup-drain")
    cleanup_drain_parser.add_argument("--path", required=True)

    cleanup_runs_parser = subparsers.add_parser("cleanup-run-count")
    cleanup_runs_parser.add_argument("--path", required=True)

    return parser


def parse_set_arguments(values: list[str]) -> dict[str, str]:
    updates: dict[str, str] = {}
    for value in values:
        if "=" not in value:
            raise ValueError(f"expected key=value for --set, got {value!r}")
        key, parsed = value.split("=", 1)
        key = key.strip()
        if not key:
            raise ValueError(f"expected non-empty key for --set, got {value!r}")
        updates[key] = parsed
    return updates


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)

    try:
        if args.command == "hash-context":
            print(hash_context_dir(Path(args.path)))
            return 0

        if args.command == "sanitize-repo-component":
            print(sanitize_repository_component(args.value))
            return 0

        if args.command == "manifest-init":
            init_manifest(Path(args.path))
            return 0

        if args.command == "manifest-upsert":
            upsert_manifest(
                Path(args.path),
                suite=args.suite,
                case_id=args.case_id,
                updates=parse_set_arguments(args.sets),
            )
            return 0

        if args.command == "manifest-resume":
            resumed = manifest_resume_info(
                Path(args.path),
                suite=args.suite,
                case_id=args.case_id,
                context_hash=args.context_hash,
            )
            if resumed is None:
                return 1
            print("\t".join(resumed))
            return 0

        if args.command == "manifest-count":
            print(
                manifest_count(
                    Path(args.path),
                    status=args.status,
                    publish_status=args.publish_status,
                )
            )
            return 0

        if args.command == "manifest-success-records":
            for record in manifest_success_records(Path(args.path)):
                print("\t".join(record))
            return 0

        if args.command == "remote-digest":
            print(
                remote_digest(
                    docker_bin=args.docker_bin,
                    reference=args.reference,
                    platform=args.platform,
                    timeout_seconds=args.timeout,
                )
            )
            return 0

        if args.command == "patch-case":
            print(
                patch_case_file(
                    Path(args.path),
                    digest_ref=args.digest_ref,
                    write=args.write,
                )
            )
            return 0

        if args.command == "cleanup-init":
            init_cleanup_state(Path(args.path))
            return 0

        if args.command == "cleanup-register-build":
            for ref in cleanup_register_build(Path(args.path), threshold=args.threshold, refs=args.refs):
                print(ref)
            return 0

        if args.command == "cleanup-register-build-trigger":
            print("1" if cleanup_register_build_trigger(Path(args.path), threshold=args.threshold) else "0")
            return 0

        if args.command == "cleanup-drain":
            for ref in cleanup_drain(Path(args.path)):
                print(ref)
            return 0

        if args.command == "cleanup-run-count":
            print(cleanup_run_count(Path(args.path)))
            return 0

    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    parser.error(f"unknown command {args.command!r}")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
