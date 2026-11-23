"""Pinning resolves one CLI version per run and waits out a release in progress."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import tomllib
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "ops/daily-runs/lib/pin_agent_config.py"

# Stands in for npm. "view" answers from FAKE_NPM_LATEST (one comma-separated
# entry per call, the last repeating), unless FAKE_NPM_STALE is set and it is
# asked the way a machine with prefer-offline and a saved copy would answer:
# without the public registry, or with a cache that is not empty. "install"
# writes a CLI whose --version fails for the first FAKE_NPM_BROKEN installs,
# the way Codex does while its platform build is still missing from npm. The
# first FAKE_NPM_HANG_VIEWS lookups and FAKE_NPM_HANG_INSTALLS installs stall,
# the way npm does when a download stops arriving.
FAKE_NPM = r'''#!/usr/bin/env python3
import json, os, sys, time
from pathlib import Path
state_path = Path(os.environ["FAKE_NPM_STATE"])
state = json.loads(state_path.read_text()) if state_path.exists() else {"view": 0, "install": 0}
args = sys.argv[1:]
cache = Path(args[args.index("--cache") + 1]) if "--cache" in args else None
fresh = (cache is not None and cache.is_dir() and not any(cache.iterdir())
         and "--registry=https://registry.npmjs.org/" in args)
with open(os.environ["FAKE_NPM_LOG"], "a") as log:
    log.write(json.dumps(args) + "\n")
if cache is not None and cache.is_dir():
    (cache / f"saved-{state['view'] + state['install']}").write_text("saved")
hangs = int(os.environ.get(f"FAKE_NPM_HANG_{args[0].upper()}S", "0"))
if state.get(f"{args[0]}_hangs", 0) < hangs:
    state[f"{args[0]}_hangs"] = state.get(f"{args[0]}_hangs", 0) + 1
    state_path.write_text(json.dumps(state))
    time.sleep(30)
    sys.exit(98)
if args[0] == "view":
    versions = os.environ["FAKE_NPM_LATEST"].split(",")
    stale = os.environ.get("FAKE_NPM_STALE")
    print(stale if stale and not fresh else versions[min(state["view"], len(versions) - 1)])
    state["view"] += 1
elif sys.argv[1] == "install":
    args = sys.argv[1:]
    prefix = Path(args[args.index("--prefix") + 1])
    version = args[-1].rsplit("@", 1)[1]
    broken = state["install"] < int(os.environ.get("FAKE_NPM_BROKEN", "0"))
    state["install"] += 1
    binary = prefix / "bin" / os.environ["FAKE_NPM_BINARY"]
    binary.parent.mkdir(parents=True)
    body = ("import sys; sys.stderr.write('Error: Missing optional dependency @example/cli-linux-x64\\n'); sys.exit(1)"
            if broken else
            "import json, os; from pathlib import Path; "
            "Path(os.environ['FAKE_PROBE_ENV']).write_text(json.dumps({'home': os.environ.get('HOME'), "
            "'codex_home': os.environ.get('CODEX_HOME')})); "
            f"print('example-cli {version}')")
    binary.write_text(f"#!{sys.executable}\n{body}\n")
    binary.chmod(0o755)
else:
    sys.exit(99)
state_path.write_text(json.dumps(state))
'''

CONFIG = '''kind = "agent_config"
name = "example-high"
definition = "../../agent-definitions/example"
mode = "direct"

[input]
example_version = "{version}"
startup_args = ["--model", "example-model"]
run_args = []
'''


class PinAgentConfigTests(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", str(self.tmp)], check=True))
        (self.tmp / "configs/agent-definitions/example").mkdir(parents=True)
        (self.tmp / "configs/agent-definitions/example/definition.toml").write_text('kind = "agent_definition"\n')
        bin_dir = self.tmp / "bin"
        bin_dir.mkdir()
        npm = bin_dir / "npm"
        npm.write_text(FAKE_NPM.replace("#!/usr/bin/env python3", f"#!{sys.executable}", 1))
        npm.chmod(0o755)
        self.log = self.tmp / "npm.log"
        self.probe_env = self.tmp / "probe-env.json"
        self.env = dict(
            os.environ,
            PATH=f"{bin_dir}:{os.environ['PATH']}",
            HOME=str(self.tmp / "real-home"),
            CODEX_HOME=str(self.tmp / "real-home/.codex"),
            FAKE_NPM_STATE=str(self.tmp / "npm-state.json"),
            FAKE_NPM_LOG=str(self.log),
            FAKE_NPM_BINARY="example",
            FAKE_NPM_LATEST="1.2.3",
            FAKE_PROBE_ENV=str(self.probe_env),
        )

    def config(self, version="latest"):
        config_dir = self.tmp / "configs/example-agent-configs/example-high"
        config_dir.mkdir(parents=True, exist_ok=True)
        (config_dir / "config.toml").write_text(CONFIG.format(version=version))
        return config_dir

    def pin(self, config_dir, *extra, **env):
        output = self.tmp / "pinned/example-high"
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--config", str(config_dir), "--version-input", "example_version",
             "--package", "@example/cli", "--binary", "example", "--output", str(output),
             "--retry-seconds", "0", *extra],
            env=dict(self.env, **env), capture_output=True, text=True,
        )
        return result, output

    def npm_calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_pins_latest_to_one_version(self):
        config_dir = self.config()
        result, output = self.pin(config_dir, "--wait-seconds", "0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "1.2.3\n")
        original = tomllib.loads((config_dir / "config.toml").read_text())
        pinned = tomllib.loads((output / "config.toml").read_text())
        self.assertEqual(pinned["input"]["example_version"], "1.2.3")
        self.assertEqual(pinned["definition"], str(self.tmp / "configs/agent-definitions/example"))
        original["input"]["example_version"] = "1.2.3"
        original["definition"] = pinned["definition"]
        self.assertEqual(pinned, original)
        calls = self.npm_calls()
        self.assertEqual(calls[0][:3], ["view", "@example/cli@latest", "version"])
        self.assertEqual(calls[1][:2], ["install", "--global"])
        self.assertEqual(calls[1][-1], "@example/cli@1.2.3")
        for call in calls:
            self.assertIn("--registry=https://registry.npmjs.org/", call)
            self.assertIn("--prefer-online", call)
        # One try asks and installs through the same new cache.
        self.assertEqual(calls[0][calls[0].index("--cache") + 1], calls[1][calls[1].index("--cache") + 1])
        # The version probe never sees this machine's real home or Codex login.
        probe = json.loads(self.probe_env.read_text())
        self.assertNotEqual(probe["home"], self.env["HOME"])
        self.assertIsNone(probe["codex_home"])

    def test_waits_out_a_release_that_is_still_uploading(self):
        result, output = self.pin(self.config(), "--wait-seconds", "60", FAKE_NPM_BROKEN="2")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "1.2.3\n")
        self.assertEqual(result.stderr.count("WARN: example --version failed"), 2)
        self.assertIn("Missing optional dependency", result.stderr)
        self.assertEqual([call[0] for call in self.npm_calls()], ["view", "install"] * 3)
        self.assertTrue((output / "config.toml").is_file())

    def test_never_takes_a_saved_answer_for_the_latest_release(self):
        # On one PC, npm with prefer-offline answered "latest" from a copy saved
        # eleven days earlier. Every try must ask fresh, as a task's container does.
        result, output = self.pin(self.config(), "--wait-seconds", "60", FAKE_NPM_BROKEN="1",
                                  FAKE_NPM_STALE="2.1.319", FAKE_NPM_LATEST="2.1.327")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "2.1.327\n")
        self.assertEqual(tomllib.loads((output / "config.toml").read_text())["input"]["example_version"], "2.1.327")
        views = [call for call in self.npm_calls() if call[0] == "view"]
        self.assertEqual(len(views), 2)
        caches = [call[call.index("--cache") + 1] for call in views]
        self.assertNotEqual(caches[0], caches[1], "each try needs its own empty cache")
        self.assertFalse(any(Path(cache).exists() for cache in caches), "caches are removed")

    def test_picks_up_a_release_that_replaces_a_broken_one(self):
        result, output = self.pin(self.config(), "--wait-seconds", "60", FAKE_NPM_BROKEN="1",
                                  FAKE_NPM_LATEST="2.0.0,2.0.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "2.0.1\n")
        self.assertEqual(tomllib.loads((output / "config.toml").read_text())["input"]["example_version"], "2.0.1")

    def test_tries_again_when_an_install_stalls(self):
        # On 2026-11-22 the first install of each CLI stalled for the whole wait.
        result, output = self.pin(self.config(), "--wait-seconds", "60", "--try-seconds", "1",
                                  FAKE_NPM_HANG_INSTALLS="1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "1.2.3\n")
        self.assertIn("WARN: npm install @example/cli@1.2.3 timed out", result.stderr)
        self.assertEqual([call[0] for call in self.npm_calls()], ["view", "install"] * 2)
        self.assertTrue((output / "config.toml").is_file())

    def test_tries_again_when_the_registry_lookup_stalls(self):
        result, output = self.pin(self.config(), "--wait-seconds", "60", "--try-seconds", "1",
                                  FAKE_NPM_HANG_VIEWS="1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "1.2.3\n")
        self.assertIn("WARN: npm view @example/cli@latest timed out", result.stderr)
        self.assertEqual([call[0] for call in self.npm_calls()], ["view", "view", "install"])
        self.assertTrue((output / "config.toml").is_file())

    def test_waits_an_hour_by_default_with_ten_minutes_per_npm_command(self):
        spec = importlib.util.spec_from_file_location("pin_agent_config", SCRIPT)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        required = ["--config", "c", "--version-input", "v", "--package", "p", "--binary", "b", "--output", "o"]
        with mock.patch.dict(os.environ):
            os.environ.pop("MARGINLAB_AGENT_INSTALL_WAIT_SECONDS", None)
            os.environ.pop("MARGINLAB_AGENT_INSTALL_TRY_SECONDS", None)
            args = module.parse_arguments(required)
        self.assertEqual(args.wait_seconds, 3600)
        self.assertEqual(args.try_seconds, 600)

    def test_gives_up_with_a_clear_error(self):
        result, output = self.pin(self.config(), "--wait-seconds", "0", FAKE_NPM_BROKEN="99")
        self.assertEqual(result.returncode, 1)
        self.assertIn("Error: @example/cli could not be installed after 1 tries", result.stderr)
        self.assertFalse(output.exists())

    def test_keeps_a_version_the_config_already_names(self):
        result, output = self.pin(self.config("1.0.0"), "--wait-seconds", "0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "1.0.0\n")
        calls = self.npm_calls()
        self.assertEqual([call[0] for call in calls], ["install"])
        self.assertEqual(calls[0][-1], "@example/cli@1.0.0")
        pinned = tomllib.loads((output / "config.toml").read_text())
        self.assertEqual(pinned["input"]["example_version"], "1.0.0")
        self.assertTrue(Path(pinned["definition"]).is_absolute())

    def test_rejects_an_existing_output(self):
        config_dir = self.config()
        (self.tmp / "pinned/example-high").mkdir(parents=True)
        result, _ = self.pin(config_dir, "--wait-seconds", "0")
        self.assertEqual(result.returncode, 2)
        self.assertEqual(self.npm_calls(), [])


if __name__ == "__main__":
    unittest.main()
