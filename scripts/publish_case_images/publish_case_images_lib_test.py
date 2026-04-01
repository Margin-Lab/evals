from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path


HELPER_PATH = Path(__file__).with_name("publish_case_images_lib.py")
SPEC = importlib.util.spec_from_file_location("publish_case_images_lib", HELPER_PATH)
assert SPEC is not None and SPEC.loader is not None
lib = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(lib)


class PublishCaseImagesLibTest(unittest.TestCase):
    def test_hash_context_is_stable_and_changes_with_contents(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            env_dir = Path(tmp) / "env"
            env_dir.mkdir()
            (env_dir / "Dockerfile").write_text("FROM scratch\n")
            nested = env_dir / "task"
            nested.mkdir()
            (nested / "input.txt").write_text("alpha\n")

            first = lib.hash_context_dir(env_dir)
            second = lib.hash_context_dir(env_dir)
            self.assertEqual(first, second)

            (nested / "input.txt").write_text("beta\n")
            third = lib.hash_context_dir(env_dir)
            self.assertNotEqual(first, third)

    def test_patch_case_inserts_image_after_description(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            case_path = Path(tmp) / "case.toml"
            case_path.write_text(
                'kind = "test_case"\n'
                'name = "demo"\n'
                'description = "demo case"\n'
                'agent_cwd = "/workspace"\n'
                'test_cwd = "/"\n'
            )

            mode = lib.patch_case_file(case_path, "ghcr.io/acme/demo@sha256:1234", write=True)
            self.assertEqual(mode, "inserted")
            self.assertEqual(
                case_path.read_text(),
                'kind = "test_case"\n'
                'name = "demo"\n'
                'description = "demo case"\n'
                'image = "ghcr.io/acme/demo@sha256:1234"\n'
                'agent_cwd = "/workspace"\n'
                'test_cwd = "/"\n',
            )

    def test_patch_case_replaces_existing_image(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            case_path = Path(tmp) / "case.toml"
            case_path.write_text(
                'kind = "test_case"\n'
                'name = "demo"\n'
                'description = "demo case"\n'
                'image = "ghcr.io/acme/demo@sha256:old"\n'
                'agent_cwd = "/workspace"\n'
                'test_cwd = "/"\n'
            )

            mode = lib.patch_case_file(case_path, "ghcr.io/acme/demo@sha256:new", write=True)
            self.assertEqual(mode, "replaced")
            self.assertIn('image = "ghcr.io/acme/demo@sha256:new"\n', case_path.read_text())

    def test_manifest_count_and_success_records(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            manifest_path = Path(tmp) / "manifest.json"
            lib.init_manifest(manifest_path)
            lib.upsert_manifest(
                manifest_path,
                suite="demo-suite",
                case_id="alpha",
                updates={
                    "case_toml_path": "/tmp/alpha/case.toml",
                    "status": "published",
                    "publish_status": "published",
                    "digest_ref": "ghcr.io/acme/demo-suite/alpha@sha256:1",
                },
            )
            lib.upsert_manifest(
                manifest_path,
                suite="demo-suite",
                case_id="beta",
                updates={
                    "case_toml_path": "/tmp/beta/case.toml",
                    "status": "failed",
                    "error": "build failed",
                },
            )
            lib.upsert_manifest(
                manifest_path,
                suite="demo-suite",
                case_id="gamma",
                updates={
                    "case_toml_path": "/tmp/gamma/case.toml",
                    "status": "patched",
                    "publish_status": "skipped_existing",
                    "digest_ref": "ghcr.io/acme/demo-suite/gamma@sha256:3",
                },
            )

            self.assertEqual(lib.manifest_count(manifest_path), 3)
            self.assertEqual(lib.manifest_count(manifest_path, status="published"), 1)
            self.assertEqual(lib.manifest_count(manifest_path, status="failed"), 1)
            self.assertEqual(lib.manifest_count(manifest_path, publish_status="published"), 1)
            self.assertEqual(lib.manifest_count(manifest_path, publish_status="skipped_existing"), 1)
            self.assertEqual(
                lib.manifest_success_records(manifest_path),
                [
                    (
                        "demo-suite",
                        "alpha",
                        "/tmp/alpha/case.toml",
                        "ghcr.io/acme/demo-suite/alpha@sha256:1",
                        "published",
                    ),
                    (
                        "demo-suite",
                        "gamma",
                        "/tmp/gamma/case.toml",
                        "ghcr.io/acme/demo-suite/gamma@sha256:3",
                        "skipped_existing",
                    ),
                ],
            )

    def test_cleanup_state_batches_and_drains(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            state_path = Path(tmp) / "cleanup.json"
            lib.init_cleanup_state(state_path)

            self.assertEqual(
                lib.cleanup_register_build(
                    state_path,
                    threshold=2,
                    refs=["ghcr.io/acme/demo:one", "ghcr.io/acme/demo:stable"],
                ),
                [],
            )
            self.assertEqual(lib.cleanup_run_count(state_path), 0)
            self.assertEqual(
                lib.cleanup_register_build(
                    state_path,
                    threshold=2,
                    refs=["ghcr.io/acme/demo:two", "ghcr.io/acme/demo:stable-two"],
                ),
                [
                    "ghcr.io/acme/demo:one",
                    "ghcr.io/acme/demo:stable",
                    "ghcr.io/acme/demo:two",
                    "ghcr.io/acme/demo:stable-two",
                ],
            )
            self.assertEqual(lib.cleanup_run_count(state_path), 1)
            self.assertEqual(
                lib.cleanup_register_build(
                    state_path,
                    threshold=2,
                    refs=["ghcr.io/acme/demo:three", "ghcr.io/acme/demo:stable-three"],
                ),
                [],
            )
            self.assertEqual(
                lib.cleanup_drain(state_path),
                [
                    "ghcr.io/acme/demo:three",
                    "ghcr.io/acme/demo:stable-three",
                ],
            )
            self.assertEqual(lib.cleanup_run_count(state_path), 2)

    def test_cleanup_trigger_counter(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            state_path = Path(tmp) / "cleanup.json"
            lib.init_cleanup_state(state_path)

            self.assertFalse(lib.cleanup_register_build_trigger(state_path, threshold=3))
            self.assertFalse(lib.cleanup_register_build_trigger(state_path, threshold=3))
            self.assertEqual(lib.cleanup_run_count(state_path), 0)
            self.assertTrue(lib.cleanup_register_build_trigger(state_path, threshold=3))
            self.assertEqual(lib.cleanup_run_count(state_path), 1)
            self.assertFalse(lib.cleanup_register_build_trigger(state_path, threshold=3))


if __name__ == "__main__":
    unittest.main()
