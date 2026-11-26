"""Verify daily runners select usable, versioned model defaults without an eval."""
import json
import os
import re
from pathlib import Path
import subprocess
import tempfile
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[3]


class DailyDefaultsTests(unittest.TestCase):
    def test_daily_model_defaults(self):
        for tracker, name, model in (
            ("codex", "codex-gpt-6.1-sol-high", "gpt-6.1-sol"),
            ("claude-code", "claude-code-opus-5.5-high", "claude-opus-5-5"),
        ):
            with self.subTest(tracker=tracker), tempfile.TemporaryDirectory() as tmp:
                workspace = Path(tmp)
                state = workspace / "state"
                state.mkdir()
                (state / "warmup-claude.sh").write_text("#!/bin/sh\nexit 0\n")
                binary = workspace / "bin"
                binary.mkdir()
                margin = binary / "margin"
                # The pinned config is deleted when the runner exits, so keep a copy.
                margin.write_text(
                    "#!/usr/bin/env python3\n"
                    "import json, os, sys\n"
                    "from pathlib import Path\n"
                    "args = sys.argv[1:]\n"
                    "config = Path(args[args.index('--agent-config') + 1]) / 'config.toml'\n"
                    "Path(os.environ['CAPTURE']).write_text(json.dumps({'args': args, 'config': config.read_text()}))\n"
                    "sys.exit(99)\n"
                )
                margin.chmod(0o755)
                # npm resolves "latest" to 9.9.9 and installs a CLI that reports it.
                npm = binary / "npm"
                npm.write_text(
                    "#!/usr/bin/env python3\n"
                    "import sys\n"
                    "from pathlib import Path\n"
                    "args = sys.argv[1:]\n"
                    "if args[0] == 'view':\n"
                    "    print('9.9.9')\n"
                    "elif args[0] == 'install':\n"
                    "    bin_dir = Path(args[args.index('--prefix') + 1]) / 'bin'\n"
                    "    bin_dir.mkdir(parents=True)\n"
                    "    for name in ('codex', 'claude'):\n"
                    "        (bin_dir / name).write_text('#!/bin/sh\\necho cli 9.9.9\\n')\n"
                    "        (bin_dir / name).chmod(0o755)\n"
                    "else:\n"
                    "    sys.exit(99)\n"
                )
                npm.chmod(0o755)
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
                captured = json.loads(capture.read_text())
                args = captured["args"]
                # The run gets the versioned config with one CLI version pinned
                # for every task, and the definition as an absolute path.
                config_path = Path(args[args.index("--agent-config") + 1])
                self.assertEqual(config_path.name, name)
                self.assertFalse(config_path.exists(), "the pinned config should be removed after the run")
                versioned_dir = ROOT / "configs/example-agent-configs" / name
                versioned = tomllib.loads((versioned_dir / "config.toml").read_text())
                config = tomllib.loads(captured["config"])
                version_input = "codex_version" if tracker == "codex" else "claude_version"
                self.assertEqual(versioned["input"][version_input], "latest")
                self.assertEqual(config["input"][version_input], "9.9.9")
                definition = (versioned_dir / versioned["definition"]).resolve()
                self.assertEqual(config["definition"], str(definition))
                self.assertEqual(
                    config,
                    {**versioned, "definition": str(definition),
                     "input": {**versioned["input"], version_input: "9.9.9"}},
                )
                self.assertIn(f"uses {'@openai/codex' if tracker == 'codex' else '@anthropic-ai/claude-code'} 9.9.9",
                              result.stdout)
                self.assertEqual(config["mode"], "direct")
                self.assertTrue((definition / "definition.toml").is_file())
                schema = json.loads((definition / "schema.json").read_text())
                self.assertTrue(set(schema["required"]) <= config["input"].keys())
                self.assertTrue(config["input"].keys() <= schema["properties"].keys())
                if tracker == "codex":
                    settings = tomllib.loads(config["input"]["config_toml"])
                    self.assertEqual(settings["model"], model)
                    self.assertEqual(settings["model_reasoning_effort"], "high")
                    self.assertIn("/gpt-6-1-sol-high/", args[args.index("--output") + 1])
                else:
                    startup = config["input"]["startup_args"]
                    self.assertEqual(startup[startup.index("--model") + 1], model)
                    self.assertEqual(startup[startup.index("--effort") + 1], "high")
                    settings = json.loads(config["input"]["settings_json"])
                    self.assertEqual(settings["model"], model)
                    self.assertEqual(settings["effortLevel"], "high")

    def test_baselines_match_readme(self):
        # Each window-test baseline is documented with the days it came from.
        readme = (ROOT / "ops/daily-runs/README.md").read_text()
        for script, variable, label in (
            ("run-codex.sh", "CODEX_BASELINE_P0", "Codex (GPT-6.1 Sol/high)"),
            ("run-claude-code.sh", "CLAUDE_CODE_BASELINE_P0", "Claude Code (Opus 5.5/high)"),
        ):
            with self.subTest(script=script):
                text = (ROOT / "ops/daily-runs" / script).read_text()
                match = re.search(rf'BASELINE_P0="\$\{{{variable}:-([0-9.]+)\}}"', text)
                self.assertIsNotNone(match, f"{script} has no {variable} default")
                self.assertIn('--baseline "${BASELINE_P0}"', text)
                self.assertIn(f"| {label} | {match.group(1)} |", readme)


if __name__ == "__main__":
    unittest.main()
