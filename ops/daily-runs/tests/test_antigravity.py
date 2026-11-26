#!/usr/bin/env python3
"""Offline tests for the Antigravity CLI tracker's helpers."""
from __future__ import annotations

import io
import json
import sys
import tempfile
import tomllib
import unittest
from contextlib import redirect_stderr, redirect_stdout
from datetime import timedelta
from pathlib import Path

DAILY_RUNS = Path(__file__).resolve().parents[1]
REPOSITORY = DAILY_RUNS.parents[1]
sys.path.insert(0, str(DAILY_RUNS / "lib"))

import antigravity_limits  # noqa: E402
import antigravity_run_check  # noqa: E402
import antigravity_suite  # noqa: E402
import pin_agy_config  # noqa: E402

CONFIG = REPOSITORY / "configs" / "example-agent-configs" / "antigravity-cli-gemini-4-neon-high"
SHA_A = "a" * 128
SHA_B = "b" * 128
# What agy 1.5.18 answered to `agy -p /usage --output-format json` on 2026-11-26.
USAGE = json.dumps({
    "conversation_id": "",
    "status": "SUCCESS",
    "response": (
        "Gemini Models\tWeekly Limit Remaining\t82%\t2026-12-03T17:26:06Z\n"
        "Gemini Models\tFive Hour Limit Remaining\t36%\t2026-11-26T22:26:06Z\n"
        "Claude and GPT models\tWeekly Limit Remaining\t100%\t1970-01-01T00:00:00Z\n"
    ),
})


def quiet(function, *args):
    out, err = io.StringIO(), io.StringIO()
    with redirect_stdout(out), redirect_stderr(err):
        status = function(*args)
    return status, out.getvalue(), err.getvalue()


class RotationTest(unittest.TestCase):
    cases = [f"case-{index:02d}" for index in range(50)]

    def test_each_night_takes_the_next_tasks_and_wraps(self):
        start = antigravity_suite.ROTATION_START
        self.assertEqual(antigravity_suite.cases_for(self.cases, start, 6), self.cases[0:6])
        self.assertEqual(antigravity_suite.cases_for(self.cases, start + timedelta(days=1), 6), self.cases[6:12])
        self.assertEqual(antigravity_suite.cases_for(self.cases, start + timedelta(days=8), 6),
                         self.cases[48:50] + self.cases[0:4])

    def test_a_week_covers_42_different_tasks(self):
        start = antigravity_suite.ROTATION_START + timedelta(days=3)
        week = [case for day in range(7) for case in
                antigravity_suite.cases_for(self.cases, start + timedelta(days=day), 6)]
        self.assertEqual(len(set(week)), 42)

    def test_writes_a_suite_with_the_nights_cases(self):
        with tempfile.TemporaryDirectory() as tmp:
            source, target = Path(tmp) / "source", Path(tmp) / "target"
            for case in self.cases:
                (source / "cases" / case).mkdir(parents=True)
                (source / "cases" / case / "case.toml").write_text(f'name = "{case}"\n')
            (source / "suite.toml").write_text(
                'kind = "test_suite"\nname = "curated"\ncases = [' + ", ".join(json.dumps(c) for c in self.cases) + "]\n")
            (source / "preamble-prompt.md").write_text("Fix the bug.\n")
            night = antigravity_suite.ROTATION_START + timedelta(days=2)
            status, out, _ = quiet(antigravity_suite.main, [
                "--source", str(source), "--target", str(target), "--date", night.isoformat(), "--count", "6"])
            self.assertEqual(status, 0)
            self.assertEqual(out.split(), self.cases[12:18])
            suite = tomllib.loads((target / "suite.toml").read_text())
            self.assertEqual(suite["cases"], self.cases[12:18])
            self.assertEqual(sorted(path.name for path in (target / "cases").iterdir()), self.cases[12:18])
            self.assertEqual((target / "preamble-prompt.md").read_text(), "Fix the bug.\n")
            before = antigravity_suite.ROTATION_START - timedelta(days=1)
            status, _, err = quiet(antigravity_suite.main, [
                "--source", str(source), "--target", str(Path(tmp) / "early"), "--date", before.isoformat(), "--count", "6"])
            self.assertEqual(status, 2)
            self.assertIn("before the rotation starts", err)


class LimitsTest(unittest.TestCase):
    def test_parses_the_gemini_limits(self):
        limits = antigravity_limits.parse_limits(USAGE)
        self.assertEqual(limits["Weekly Limit Remaining"], (82.0, "2026-12-03T17:26:06Z"))
        self.assertEqual(limits["Five Hour Limit Remaining"], (36.0, "2026-11-26T22:26:06Z"))

    def run_with_fake_agy(self, answer: str, exit_code: int = 0, minimums=("20", "30")):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp) / "home"
            token = home / antigravity_limits.TOKEN_RELATIVE_PATH
            token.parent.mkdir(parents=True)
            token.write_text('{"token": {}}')
            (home / ".gemini" / "antigravity-cli" / "settings.json").write_text("{}")
            agy = Path(tmp) / "agy"
            seen = Path(tmp) / "seen"
            # The fake records what it was run with, then answers.
            agy.write_text(
                "#!/usr/bin/env bash\n"
                f"{{ echo \"TERM=$TERM\"; ls -A \"$HOME/.gemini/antigravity-cli\"; echo \"args=$*\"; }} > {seen}\n"
                f"cat <<'JSON'\n{answer}\nJSON\nexit {exit_code}\n")
            agy.chmod(0o755)
            result = quiet(antigravity_limits.main, [
                "--agy", str(agy), "--home", str(home), "--min-weekly", minimums[0], "--min-five-hour", minimums[1]])
            return result, seen.read_text() if seen.exists() else ""

    def test_runs_usage_in_a_clean_home_and_reports_the_limits(self):
        (status, out, _), seen = self.run_with_fake_agy(USAGE)
        self.assertEqual(status, 0)
        self.assertIn("weekly 82%", out)
        self.assertIn("5-hour 36%", out)
        self.assertIn("TERM=dumb", seen)
        # Only the sign-in is copied: the machine's own agy settings stay behind.
        self.assertIn("antigravity-oauth-token", seen)
        self.assertNotIn("settings.json", seen)
        self.assertIn("args=-p /usage --output-format json", seen)

    def test_skips_when_a_limit_is_low_and_fails_when_unreadable(self):
        (status, _, err), _ = self.run_with_fake_agy(USAGE, minimums=("20", "70"))
        self.assertEqual(status, antigravity_limits.SKIP)
        self.assertIn("Not enough left", err)
        (status, _, err), _ = self.run_with_fake_agy("not json")
        self.assertEqual(status, 1)
        self.assertIn("other than JSON", err)
        (status, _, err), _ = self.run_with_fake_agy(USAGE, exit_code=3)
        self.assertEqual(status, 1)
        self.assertIn("failed with status 3", err)


class PinTest(unittest.TestCase):
    def manifests(self, releases: dict[str, str]):
        def fetch(url: str) -> dict:
            name = url.rsplit("/", 1)[1].removesuffix(".json")
            release = releases[name]
            return {
                "version": release.split("-")[0],
                "url": f"{pin_agy_config.RELEASE_BASE_URL}/{release}/{pin_agy_config.PLATFORM_ARCHIVES[name]}",
                "sha512": SHA_A if name.endswith("musl") else SHA_B,
            }
        return fetch

    def test_resolves_one_release_for_both_builds(self):
        release, urls, sha512s = pin_agy_config.resolve_release(
            ("linux_amd64", "linux_amd64_musl"),
            self.manifests({"linux_amd64": "1.5.18-6386224290725888", "linux_amd64_musl": "1.5.18-6386224290725888"}))
        self.assertEqual(release, "1.5.18-6386224290725888")
        self.assertTrue(urls["linux_amd64"].endswith("/1.5.18-6386224290725888/linux-x64/cli_linux_x64.tar.gz"))
        self.assertEqual(sha512s, {"linux_amd64": SHA_B, "linux_amd64_musl": SHA_A})

    def test_waits_while_a_release_is_going_out_or_the_layout_changed(self):
        with self.assertRaisesRegex(pin_agy_config.PinError, "different releases"):
            pin_agy_config.resolve_release(
                ("linux_amd64", "linux_amd64_musl"),
                self.manifests({"linux_amd64": "1.5.19-1", "linux_amd64_musl": "1.5.18-6386224290725888"}))
        with self.assertRaisesRegex(pin_agy_config.PinError, "known layout"):
            pin_agy_config.resolve_release(("linux_amd64",), lambda url: {"url": "https://example.com/agy.tar.gz", "sha512": SHA_A})

    def test_writes_a_config_naming_the_release_and_its_checksums(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / "pinned"
            pin_agy_config.write_pinned_config(
                CONFIG, output, "1.5.18-6386224290725888", {"linux_amd64": SHA_B, "linux_amd64_musl": SHA_A})
            pinned = tomllib.loads((output / "config.toml").read_text())
            original = tomllib.loads((CONFIG / "config.toml").read_text())
            self.assertEqual(pinned["input"]["agy_version"], "1.5.18-6386224290725888")
            self.assertEqual(pinned["input"]["agy_sha512"], {"linux_amd64": SHA_B, "linux_amd64_musl": SHA_A})
            self.assertTrue(Path(pinned["definition"]).is_absolute())
            self.assertTrue((Path(pinned["definition"]) / "definition.toml").is_file())
            self.assertEqual(pinned["input"]["model"], original["input"]["model"])


class RunCheckTest(unittest.TestCase):
    def test_rejects_a_night_with_a_task_stopped_by_a_limit(self):
        with tempfile.TemporaryDirectory() as tmp:
            run = Path(tmp)
            for name, extra in (
                ("inst-0001", {"status": "SUCCESS"}),
                ("inst-0002", {"status": "ERROR", "error": "agent crashed"}),
            ):
                (run / "instances" / name).mkdir(parents=True)
                (run / "instances" / name / "trajectory.json").write_text(json.dumps({"final_metrics": {"extra": extra}}))
            self.assertEqual(antigravity_run_check.limit_stops(run), [])
            (run / "instances" / "inst-0003").mkdir()
            (run / "instances" / "inst-0003" / "trajectory.json").write_text(json.dumps(
                {"final_metrics": {"extra": {"status": "ERROR", "error": "You have exhausted your quota on this model."}}}))
            status, _, err = quiet(antigravity_run_check.main, ["--run", str(run)])
            self.assertEqual(status, 1)
            self.assertIn("inst-0003: You have exhausted your quota", err)


if __name__ == "__main__":
    unittest.main()
