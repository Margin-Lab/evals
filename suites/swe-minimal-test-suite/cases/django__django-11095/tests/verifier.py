# /// script
# requires-python = ">=3.11"
# dependencies = ["swebench==4.0.3", "datasets==2.16.1", "fastcore<1.11"]
# ///

from __future__ import annotations

import argparse
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass


PASS_EXIT = 0
FAIL_EXIT = 1
INFRA_EXIT = 2

START_TEST_OUTPUT = ">>>>> Start Test Output"
END_TEST_OUTPUT = ">>>>> End Test Output"

REPORT_PATH = pathlib.Path("/logs/verifier/report.json")
DEFAULT_TIMEOUT_SECONDS = int(
    os.environ.get("SWEBENCH_VERIFIER_TIMEOUT_SECONDS", "2940")
)


@dataclass(frozen=True)
class PatchEntry:
    old_path: str | None
    new_path: str | None


class HarnessError(Exception):
    def __init__(
        self,
        verdict: str,
        message: str,
        *,
        details: str | None = None,
        extra: dict[str, object] | None = None,
    ) -> None:
        super().__init__(message)
        self.verdict = verdict
        self.message = message
        self.details = details
        self.extra = extra or {}


def load_config(config_path: pathlib.Path) -> dict[str, object]:
    try:
        with config_path.open("r", encoding="utf-8") as handle:
            config = json.load(handle)
    except Exception as exc:  # pragma: no cover - defensive infra path
        raise HarnessError("infra", f"Failed to load {config_path}.", details=str(exc))
    if not isinstance(config, dict):
        raise HarnessError("infra", f"{config_path} must contain a JSON object.")
    return config


def default_entry() -> dict[str, object]:
    return {
        "patch_exists": False,
        "patch_successfully_applied": False,
        "resolved": False,
    }


def write_report(instance_id: str | None, entry: dict[str, object]) -> None:
    REPORT_PATH.parent.mkdir(parents=True, exist_ok=True)
    key = instance_id if instance_id else "_infra"
    with REPORT_PATH.open("w", encoding="utf-8") as handle:
        json.dump({key: entry}, handle, indent=2)
        handle.write("\n")


def finalize(
    config: dict[str, object] | None,
    verdict: str,
    exit_code: int,
    message: str | None = None,
    *,
    details: str | None = None,
    extra: dict[str, object] | None = None,
) -> int:
    instance_id = None
    if isinstance(config, dict):
        raw_instance_id = config.get("instance_id")
        if isinstance(raw_instance_id, str) and raw_instance_id:
            instance_id = raw_instance_id

    entry = default_entry()
    entry["verdict"] = verdict
    if extra:
        entry.update(extra)
    if message is not None:
        entry["message"] = message
        print(message, file=sys.stderr)
    if details is not None:
        entry["details"] = details
        print(details, file=sys.stderr)

    write_report(instance_id, entry)
    return exit_code


def fail(config: dict[str, object], message: str, **kwargs: object) -> int:
    extra = kwargs.pop("extra", None)
    details = kwargs.pop("details", None)
    return finalize(config, "fail", FAIL_EXIT, message, details=details, extra=extra)


def infra(config: dict[str, object] | None, message: str, **kwargs: object) -> int:
    extra = kwargs.pop("extra", None)
    details = kwargs.pop("details", None)
    return finalize(config, "infra", INFRA_EXIT, message, details=details, extra=extra)


def remaining_seconds(deadline: float) -> float:
    return max(1.0, deadline - time.monotonic())


def build_command_env() -> dict[str, str]:
    env = os.environ.copy()
    original_path = env.get("SWEBENCH_ORIG_PATH")
    if original_path:
        env["PATH"] = original_path
    env.pop("VIRTUAL_ENV", None)
    return env


def run_shell_command(
    command: str,
    *,
    timeout_seconds: float,
    capture_output: bool = False,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        shell=True,
        executable="/bin/bash",
        env=build_command_env(),
        text=True,
        stdout=subprocess.PIPE if capture_output else None,
        stderr=subprocess.STDOUT if capture_output else None,
        timeout=timeout_seconds,
        check=False,
    )


def run_setup_commands(config: dict[str, object], deadline: float) -> None:
    raw_commands = config.get("setup_commands", [])
    if not isinstance(raw_commands, list):
        raise HarnessError("infra", "Verifier config setup_commands must be a list.")
    for raw_command in raw_commands:
        if not isinstance(raw_command, str) or not raw_command.strip():
            raise HarnessError("infra", "Verifier config setup_commands contains an invalid command.")
        command = raw_command.strip()
        if command.startswith("export ") and "=" in command:
            key, value = command[len("export ") :].split("=", 1)
            os.environ[key.strip()] = value
            continue
        try:
            result = run_shell_command(
                command,
                timeout_seconds=remaining_seconds(deadline),
                capture_output=False,
            )
        except subprocess.TimeoutExpired as exc:
            raise HarnessError(
                "infra",
                "Verifier setup timed out before candidate evaluation began.",
                details=str(exc),
            ) from exc
        if result.returncode != 0:
            raise HarnessError(
                "infra",
                "Verifier setup failed before candidate evaluation began.",
                details=f"Command: {command}\nExit code: {result.returncode}",
            )


def run_install_command(config: dict[str, object], deadline: float) -> None:
    raw_command = config.get("install_command")
    if raw_command in (None, ""):
        return
    if not isinstance(raw_command, str):
        raise HarnessError("infra", "Verifier config install_command must be a string or null.")
    command = raw_command.strip()
    try:
        result = run_shell_command(
            command,
            timeout_seconds=remaining_seconds(deadline),
            capture_output=False,
        )
    except subprocess.TimeoutExpired as exc:
        raise HarnessError(
            "fail",
            "Candidate install/build step timed out.",
            details=str(exc),
        ) from exc
    if result.returncode != 0:
        raise HarnessError(
            "fail",
            "Candidate install/build step failed.",
            details=f"Command: {command}\nExit code: {result.returncode}",
        )


def normalize_patch_path(raw_path: str) -> str | None:
    path = raw_path.strip()
    if path == "/dev/null":
        return None
    if path.startswith("a/") or path.startswith("b/"):
        return path[2:]
    return path


def parse_patch_entries(patch_text: str) -> list[PatchEntry]:
    if not patch_text.strip():
        raise HarnessError("infra", "Verifier config test_patch is empty.")

    entries: list[PatchEntry] = []
    current: dict[str, str | None] | None = None

    def flush_current() -> None:
        nonlocal current
        if current is None:
            return
        old_path = current.get("old_path")
        new_path = current.get("new_path")
        if old_path is None and new_path is None:
            raise HarnessError("infra", "Hidden test patch contains an invalid diff header.")
        entries.append(PatchEntry(old_path, new_path))
        current = None

    for line in patch_text.splitlines():
        if line.startswith("diff --git "):
            flush_current()
            current = {"old_path": None, "new_path": None}
            continue
        if current is None:
            continue
        if line.startswith("--- "):
            current["old_path"] = normalize_patch_path(line[4:])
        elif line.startswith("+++ "):
            current["new_path"] = normalize_patch_path(line[4:])

    flush_current()

    if not entries:
        raise HarnessError("infra", "Hidden test patch does not contain any file diffs.")
    return entries


def git_capture(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        check=False,
    )


def git_object_exists(spec: str) -> bool:
    result = subprocess.run(
        ["git", "cat-file", "-e", spec],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    return result.returncode == 0


def validate_patch_state(config: dict[str, object], entries: list[PatchEntry]) -> str:
    raw_base_commit = config.get("base_commit")
    if not isinstance(raw_base_commit, str) or not raw_base_commit.strip():
        raise HarnessError("infra", "Verifier config is missing base_commit.")
    base_commit = raw_base_commit.strip()

    git_root = git_capture("rev-parse", "--show-toplevel")
    if git_root.returncode != 0:
        raise HarnessError(
            "infra",
            "Verifier could not access the candidate repository as a git checkout.",
            details=git_root.stdout.strip(),
        )

    commit_check = git_capture("rev-parse", "--verify", f"{base_commit}^{{commit}}")
    if commit_check.returncode != 0:
        raise HarnessError(
            "infra",
            "Verifier base_commit is missing from the repository checkout.",
            details=commit_check.stdout.strip(),
        )

    for entry in entries:
        if entry.old_path and not git_object_exists(f"{base_commit}:{entry.old_path}"):
            raise HarnessError(
                "infra",
                "Hidden test patch refers to a base path that does not exist in base_commit.",
                details=entry.old_path,
            )
        if entry.new_path and entry.old_path != entry.new_path:
            if git_object_exists(f"{base_commit}:{entry.new_path}"):
                raise HarnessError(
                    "infra",
                    "Hidden test patch expects a new path that already exists in base_commit.",
                    details=entry.new_path,
                )
            if pathlib.Path(entry.new_path).exists():
                raise HarnessError(
                    "fail",
                    "Candidate repo state blocks hidden test patch application.",
                    details=entry.new_path,
                    extra={"patch_exists": True},
                )

    return base_commit


def prepare_hidden_test_patch(config: dict[str, object], deadline: float) -> None:
    raw_patch = config.get("test_patch")
    if not isinstance(raw_patch, str):
        raise HarnessError("infra", "Verifier config test_patch must be a string.")

    entries = parse_patch_entries(raw_patch)
    base_commit = validate_patch_state(config, entries)

    restore_paths = sorted({entry.old_path for entry in entries if entry.old_path})
    if restore_paths:
        try:
            reset_result = subprocess.run(
                ["git", "checkout", base_commit, "--", *restore_paths],
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=remaining_seconds(deadline),
                check=False,
            )
        except subprocess.TimeoutExpired as exc:
            raise HarnessError(
                "infra",
                "Timed out while restoring hidden test targets.",
                details=str(exc),
                extra={"patch_exists": True},
            ) from exc
        if reset_result.returncode != 0:
            raise HarnessError(
                "infra",
                "Failed to restore hidden test targets before applying the verifier patch.",
                details=reset_result.stdout.strip(),
                extra={"patch_exists": True},
            )

    with tempfile.NamedTemporaryFile("w", encoding="utf-8", delete=False) as handle:
        handle.write(raw_patch)
        patch_path = handle.name

    try:
        try:
            check_result = subprocess.run(
                ["git", "apply", "--check", patch_path],
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=remaining_seconds(deadline),
                check=False,
            )
        except subprocess.TimeoutExpired as exc:
            raise HarnessError(
                "infra",
                "Timed out while validating the hidden test patch.",
                details=str(exc),
                extra={"patch_exists": True},
            ) from exc
        if check_result.returncode != 0:
            raise HarnessError(
                "infra",
                "Hidden test patch did not apply cleanly after restoring base test targets.",
                details=check_result.stdout.strip(),
                extra={"patch_exists": True},
            )

        try:
            apply_result = subprocess.run(
                ["git", "apply", patch_path],
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=remaining_seconds(deadline),
                check=False,
            )
        except subprocess.TimeoutExpired as exc:
            raise HarnessError(
                "infra",
                "Timed out while applying the hidden test patch.",
                details=str(exc),
                extra={"patch_exists": True},
            ) from exc
        if apply_result.returncode != 0:
            raise HarnessError(
                "infra",
                "Hidden test patch validation succeeded, but git apply still failed.",
                details=apply_result.stdout.strip(),
                extra={"patch_exists": True},
            )
    finally:
        try:
            pathlib.Path(patch_path).unlink(missing_ok=True)
        except Exception:
            pass


def run_test_command(config: dict[str, object], deadline: float) -> pathlib.Path:
    raw_command = config.get("test_command")
    if not isinstance(raw_command, str) or not raw_command.strip():
        raise HarnessError("infra", "Verifier config is missing test_command.")
    command = raw_command.strip()

    log_file = tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        delete=False,
        prefix="swebench-log-",
        suffix=".txt",
    )
    log_path = pathlib.Path(log_file.name)

    with log_file:
        log_file.write(f"{START_TEST_OUTPUT}\n")
        log_file.flush()
        process = subprocess.Popen(
            command,
            shell=True,
            executable="/bin/bash",
            env=build_command_env(),
            stdout=log_file,
            stderr=subprocess.STDOUT,
        )
        try:
            process.wait(timeout=remaining_seconds(deadline))
        except subprocess.TimeoutExpired as exc:
            process.kill()
            process.wait()
            log_file.write(f"\n{END_TEST_OUTPUT}\n")
            log_file.flush()
            raise HarnessError(
                "fail",
                "Candidate test command timed out.",
                details=str(exc),
                extra={"patch_exists": True, "patch_successfully_applied": True},
            ) from exc
        log_file.write(f"\n{END_TEST_OUTPUT}\n")
        log_file.flush()

    return log_path


def run_parser(config_path: pathlib.Path, log_path: pathlib.Path) -> int:
    if shutil.which("uv") is None:
        config = load_config(config_path)
        return infra(
            config,
            "Verifier parser runtime is unavailable.",
            details="Missing required command: uv",
            extra={"patch_exists": True, "patch_successfully_applied": True},
        )
    command = ["uv", "run", str(pathlib.Path("/tests/verifier.py")), "parse", str(config_path), str(log_path)]
    parser_result = subprocess.run(command, text=True, check=False)
    if parser_result.returncode in {PASS_EXIT, FAIL_EXIT, INFRA_EXIT}:
        return parser_result.returncode
    config = load_config(config_path)
    return infra(
        config,
        "Verifier parser exited unexpectedly before producing a verdict.",
        details=f"Exit code: {parser_result.returncode}",
        extra={"patch_exists": True, "patch_successfully_applied": True},
    )


def cmd_run(config_path: pathlib.Path) -> int:
    config = load_config(config_path)
    deadline = time.monotonic() + DEFAULT_TIMEOUT_SECONDS

    try:
        run_setup_commands(config, deadline)
        run_install_command(config, deadline)
        prepare_hidden_test_patch(config, deadline)
        log_path = run_test_command(config, deadline)
    except HarnessError as exc:
        if exc.verdict == "fail":
            return fail(config, exc.message, details=exc.details, extra=exc.extra)
        return infra(config, exc.message, details=exc.details, extra=exc.extra)

    return run_parser(config_path, log_path)


def cmd_parse(config_path: pathlib.Path, log_path: pathlib.Path) -> int:
    config = load_config(config_path)
    if not log_path.is_file():
        return infra(
            config,
            "Verifier log file is missing.",
            details=str(log_path),
            extra={"patch_exists": True, "patch_successfully_applied": True},
        )

    try:
        from swebench.harness.constants import (
            FAIL_ONLY_REPOS,
            FAIL_TO_PASS,
            KEY_INSTANCE_ID,
            PASS_TO_PASS,
            EvalType,
            ResolvedStatus,
        )
        from swebench.harness.grading import (
            get_eval_tests_report,
            get_logs_eval,
            get_resolution_status,
        )
        from swebench.harness.test_spec.test_spec import make_test_spec
    except Exception as exc:  # pragma: no cover - parser dependency failure
        return infra(
            config,
            "Failed to import SWE-bench verifier dependencies.",
            details=str(exc),
            extra={"patch_exists": True, "patch_successfully_applied": True},
        )

    instance_id = config.get(KEY_INSTANCE_ID)
    if not isinstance(instance_id, str) or not instance_id:
        return infra(
            config,
            "Verifier config is missing the instance_id.",
            extra={"patch_exists": True, "patch_successfully_applied": True},
        )

    try:
        test_spec = make_test_spec(config)
    except Exception as exc:
        return infra(
            config,
            "Failed to build the SWE-bench test spec from config.",
            details=str(exc),
            extra={"patch_exists": True, "patch_successfully_applied": True},
        )

    try:
        eval_status_map, found = get_logs_eval(test_spec, str(log_path))
    except Exception as exc:
        return infra(
            config,
            "Verifier failed to parse the SWE-bench test logs.",
            details=str(exc),
            extra={"patch_exists": True, "patch_successfully_applied": True},
        )

    if not found:
        return infra(
            config,
            "Verifier could not derive a trustworthy result from the test logs.",
            extra={"patch_exists": True, "patch_successfully_applied": True},
        )

    eval_reference = {
        KEY_INSTANCE_ID: test_spec.instance_id,
        FAIL_TO_PASS: test_spec.FAIL_TO_PASS,
        PASS_TO_PASS: test_spec.PASS_TO_PASS,
    }
    eval_type = (
        EvalType.FAIL_ONLY if test_spec.repo in FAIL_ONLY_REPOS else EvalType.PASS_AND_FAIL
    )

    try:
        tests_status = get_eval_tests_report(
            eval_status_map,
            eval_reference,
            eval_type=eval_type,
        )
        resolution_status = get_resolution_status(tests_status)
    except Exception as exc:
        return infra(
            config,
            "Verifier could not build a trustworthy SWE-bench verdict.",
            details=str(exc),
            extra={"patch_exists": True, "patch_successfully_applied": True},
        )

    extra = {
        "patch_exists": True,
        "patch_successfully_applied": True,
        "tests_status": tests_status,
        "resolution_status": resolution_status,
    }

    if resolution_status == ResolvedStatus.FULL.value:
        extra["resolved"] = True
        return finalize(config, "pass", PASS_EXIT, extra=extra)

    known_statuses = {status.value for status in ResolvedStatus}
    if resolution_status in known_statuses:
        return finalize(config, "fail", FAIL_EXIT, extra=extra)

    return infra(
        config,
        "Verifier returned an unknown SWE-bench resolution status.",
        details=repr(resolution_status),
        extra=extra,
    )


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    run_parser_cmd = subparsers.add_parser("run")
    run_parser_cmd.add_argument("config_path", type=pathlib.Path)

    parse_parser_cmd = subparsers.add_parser("parse")
    parse_parser_cmd.add_argument("config_path", type=pathlib.Path)
    parse_parser_cmd.add_argument("log_path", type=pathlib.Path)

    return parser


def main() -> int:
    args = build_arg_parser().parse_args()
    if args.command == "run":
        return cmd_run(args.config_path)
    if args.command == "parse":
        return cmd_parse(args.config_path, args.log_path)
    raise AssertionError(f"Unhandled command: {args.command}")


if __name__ == "__main__":
    sys.exit(main())
