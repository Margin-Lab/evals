package store

import "testing"

func TestDefaultArtifactFilenameAgentPTY(t *testing.T) {
	name, ok := DefaultArtifactFilename(ArtifactRoleAgentPTY)
	if !ok {
		t.Fatalf("expected filename mapping for %s", ArtifactRoleAgentPTY)
	}
	if name != "agent_server_pty.log" {
		t.Fatalf("unexpected filename for %s: %s", ArtifactRoleAgentPTY, name)
	}
}
