package buildinfo

import "testing"

func TestCurrentReturnsNormalizedValues(t *testing.T) {
	origVersion := Version
	origBuildTime := BuildTime
	t.Cleanup(func() {
		Version = origVersion
		BuildTime = origBuildTime
	})

	Version = " v1.2.3 "
	BuildTime = " 2026-04-06T18:00:00Z "

	info := Current()
	if info.Version != "v1.2.3" {
		t.Fatalf("Version = %q", info.Version)
	}
	if info.BuildTime != "2026-04-06T18:00:00Z" {
		t.Fatalf("BuildTime = %q", info.BuildTime)
	}
}

func TestCurrentFallsBackForEmptyValues(t *testing.T) {
	origVersion := Version
	origBuildTime := BuildTime
	t.Cleanup(func() {
		Version = origVersion
		BuildTime = origBuildTime
	})

	Version = " "
	BuildTime = ""

	info := Current()
	if info.Version != DevVersion {
		t.Fatalf("Version = %q, want %q", info.Version, DevVersion)
	}
	if info.BuildTime != UnknownBuildTime {
		t.Fatalf("BuildTime = %q, want %q", info.BuildTime, UnknownBuildTime)
	}
}
