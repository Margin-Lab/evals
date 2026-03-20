package buildinfo

import "strings"

const DevVersion = "dev"

// Version is set at release build time via -ldflags. Local builds default to dev.
var Version = DevVersion

func CurrentVersion() string {
	trimmed := strings.TrimSpace(Version)
	if trimmed == "" {
		return DevVersion
	}
	return trimmed
}
