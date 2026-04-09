package runfs

import (
	"path/filepath"
	"testing"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

func TestRelativePathForRole(t *testing.T) {
	tests := []struct {
		role    string
		wantRel string
		want    ArtifactView
	}{
		{role: store.ArtifactRoleTrajectory, wantRel: "instances/inst_1/trajectory.json", want: ArtifactView{Key: "trajectory"}},
		{role: store.ArtifactRoleDockerBuild, wantRel: "instances/inst_1/image/docker_build.log", want: ArtifactView{Stage: "image", Key: "docker_build_log"}},
		{role: store.ArtifactRoleAgentBoot, wantRel: "instances/inst_1/bootstrap/agent_server_bootstrap.log", want: ArtifactView{Stage: "bootstrap", Key: "agent_server_bootstrap_log"}},
		{role: store.ArtifactRoleAgentControl, wantRel: "instances/inst_1/run/agent_server_control.log", want: ArtifactView{Stage: "run", Key: "agent_server_control_log"}},
		{role: store.ArtifactRoleAgentRuntime, wantRel: "instances/inst_1/run/agent_server_runtime.log", want: ArtifactView{Stage: "run", Key: "agent_server_runtime_log"}},
		{role: store.ArtifactRoleAgentPTY, wantRel: "instances/inst_1/run/agent_server_pty.log", want: ArtifactView{Stage: "run", Key: "agent_server_pty_log"}},
		{role: store.ArtifactRoleTestStdout, wantRel: "instances/inst_1/test/test_stdout.txt", want: ArtifactView{Stage: "test", Key: "test_stdout"}},
		{role: store.ArtifactRoleTestStderr, wantRel: "instances/inst_1/test/test_stderr.txt", want: ArtifactView{Stage: "test", Key: "test_stderr"}},
	}

	for _, tc := range tests {
		gotRel, gotView, ok := RelativePathForRole("inst_1", tc.role)
		if !ok {
			t.Fatalf("RelativePathForRole(%q) reported not ok", tc.role)
		}
		if gotRel != tc.wantRel {
			t.Fatalf("RelativePathForRole(%q) rel = %q, want %q", tc.role, gotRel, tc.wantRel)
		}
		if gotView != tc.want {
			t.Fatalf("RelativePathForRole(%q) view = %+v, want %+v", tc.role, gotView, tc.want)
		}
	}
}

func TestRelativePathForArtifactUsesExtraDirForUnknownRole(t *testing.T) {
	rel, view := RelativePathForArtifact("inst_1", store.Artifact{
		ArtifactID: "custom-report",
		Role:       "custom_role",
	}, "/tmp/report.json")

	if rel != "instances/inst_1/run/extra/custom-report.json" {
		t.Fatalf("unexpected rel path: %s", rel)
	}
	if view.Stage != filepath.ToSlash(filepath.Join("run", "extra")) || view.Key != "custom_role" {
		t.Fatalf("unexpected view: %+v", view)
	}
}

func TestBundlePathUsesInternalDir(t *testing.T) {
	got := BundlePath("/tmp/root/run_1")
	want := filepath.Join("/tmp/root", "run_1", "internal", "bundle.json")
	if got != want {
		t.Fatalf("BundlePath() = %q, want %q", got, want)
	}
}
