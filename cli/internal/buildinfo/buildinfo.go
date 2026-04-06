package buildinfo

import "strings"

const DevVersion = "dev"
const UnknownBuildTime = "unknown"

// Version is set at release build time via -ldflags. Local builds default to dev.
var Version = DevVersion
var BuildTime = UnknownBuildTime

type Info struct {
	Version   string
	BuildTime string
}

func Current() Info {
	return Info{
		Version:   normalizedValue(Version, DevVersion),
		BuildTime: normalizedValue(BuildTime, UnknownBuildTime),
	}
}

func CurrentVersion() string {
	return Current().Version
}

func normalizedValue(raw, fallback string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
