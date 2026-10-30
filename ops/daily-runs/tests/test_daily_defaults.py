"""Verify daily runners select usable, versioned model defaults without an eval."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[3]


class DailyDefaultsTests(unittest.TestCase):
    def test_daily_model_defaults(self):
        for tracker, name, model in (
            ("codex", "codex-gpt-6.1-sol-xhigh", "gpt-6.1-sol"),
            ("claude-code", "claude-code-opus-5.5-xhigh", "claude-opus-5-5"),
        ):
            with self.subTest(tracker=tracker), tempfile.TemporaryDirectory() as tmp:
                workspace = Path(tmp)
                state = workspace / "state"
                state.mkdir()
                (state / "warmup-claude.sh").write_text("#!/bin/sh\nexit 0\n")
                binary = workspace / "bin"
                binary.mkdir()
                margin = binary / "margin"
                margin.write_text(
                    "#!/usr/bin/env python3\n"
                    "import json, os, sys\n"
                    "from pathlib import Path\n"
                    "Path(os.environ['CAPTURE']).write_text(json.dumps(sys.argv[1:]))\n"
                    "sys.exit(99)\n"
                )
                margin.chmod(0o755)
                capture = workspace / "args.json"
                env = dict(os.environ, PATH=f"{binary}:{os.environ['PATH']}",
                           MARGINLAB_EVAL_WORKSPACE_ROOT=str(workspace),
                           MARGINLAB_DAILY_STATE_DIR=str(state),
                           MARGINLAB_RAW_REPOSITORY=str(workspace / "raw"),
                           RUN_TIMESTAMP="20261030-040000", FORCE_RUN="1",
                           CAPTURE=str(capture))
                result = subprocess.run(
                    [str(ROOT / "ops/daily-runs" / f"run-{tracker}.sh")],
                    env=env, capture_output=True, text=True,
                )
                # The stub deliberately produces no results: statistics must fail closed.
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(capture.exists(), result.stderr)
                args = json.loads(capture.read_text())
                config_dir = Path(args[args.index("--agent-config") + 1]).resolve()
                self.assertEqual(config_dir, ROOT / "configs/example-agent-configs" / name)
                config = tomllib.loads((config_dir / "config.toml").read_text())
                self.assertEqual(config["mode"], "direct")
                definition = (config_dir / config["definition"]).resolve()
                self.assertTrue((definition / "definition.toml").is_file())
                schema = json.loads((definition / "schema.json").read_text())
                self.assertTrue(set(schema["required"]) <= config["input"].keys())
                self.assertTrue(config["input"].keys() <= schema["properties"].keys())
                if tracker == "codex":
                    settings = tomllib.loads(config["input"]["config_toml"])
                    self.assertEqual(settings["model"], model)
                    self.assertEqual(settings["model_reasoning_effort"], "xhigh")
                    self.assertIn("/gpt-6-1-sol-xhigh/", args[args.index("--output") + 1])
                else:
                    startup = config["input"]["startup_args"]
                    self.assertEqual(startup[startup.index("--model") + 1], model)
                    self.assertEqual(startup[startup.index("--effort") + 1], "xhigh")
                    settings = json.loads(config["input"]["settings_json"])
                    self.assertEqual(settings["model"], model)
                    self.assertEqual(settings["effortLevel"], "xhigh")


if __name__ == "__main__":
    unittest.main()
