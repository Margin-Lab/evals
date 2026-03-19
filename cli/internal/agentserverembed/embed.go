package agentserverembed

import "embed"

//go:embed generated/*
var embeddedFiles embed.FS
