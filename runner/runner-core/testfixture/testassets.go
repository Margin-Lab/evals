package testfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

const (
	minimalArchiveBase64 = "H4sIAAAAAAAC/+3NQQrCMBSE4axziohrTVRiz5PCkwaklrzE8xu6KbhXBP9vM8NsporWo07mk0I3xLhm954hxMvW1304X0/GBfMFTWsq/dL8p/3ONy1+zLOX+enGpJNVqe4g7eGWvMgt5butpYk1AAAAAAAAAAAAAAAAAIDf8QLX6+4FACgAAA=="
	minimalArchiveSHA256 = "32e2807f93a86a87c24cd79cfde22b62adb1861c1778c1d6218a13baa38c285c"
	minimalArchiveBytes  = 136
)

func MinimalTestAssets() runbundle.TestAssets {
	return runbundle.TestAssets{
		ArchiveTGZBase64: minimalArchiveBase64,
		ArchiveTGZSHA256: minimalArchiveSHA256,
		ArchiveTGZBytes:  minimalArchiveBytes,
	}
}

func MinimalTestAssetsSpec() map[string]any {
	return map[string]any{
		"archive_tgz_base64": minimalArchiveBase64,
		"archive_tgz_sha256": minimalArchiveSHA256,
		"archive_tgz_bytes":  minimalArchiveBytes,
	}
}

var (
	minimalAgentPackageOnce sync.Once
	minimalAgentPackageDesc testassets.Descriptor
	minimalAgentPackageErr  error
)

func MinimalAgent() runbundle.Agent {
	return runbundle.Agent{
		Definition: MinimalDefinitionSnapshot(),
		Config:     MinimalConfigSpec(),
	}
}

func MinimalDefinitionSnapshot() agentdef.DefinitionSnapshot {
	return agentdef.DefinitionSnapshot{
		Manifest: agentdef.Manifest{
			Kind: "agent_definition",
			Name: "fixture-agent",
			Run: agentdef.RunSpec{
				PrepareHook: agentdef.HookRef{Path: "hooks/run-prepare.sh"},
			},
		},
		Package: minimalAgentPackage(),
	}
}

func MinimalConfigSpec() agentdef.ConfigSpec {
	return agentdef.ConfigSpec{
		Name: "fixture-agent-default",
		Mode: agentdef.ConfigModeDirect,
		Input: map[string]any{
			"command": []any{"bash", "-lc", "echo hello"},
		},
	}
}

func minimalAgentPackage() testassets.Descriptor {
	minimalAgentPackageOnce.Do(func() {
		root, err := os.MkdirTemp("", "runner-core-agentdef-*")
		if err != nil {
			minimalAgentPackageErr = fmt.Errorf("create temp dir: %w", err)
			return
		}
		defer os.RemoveAll(root)
		hookPath := filepath.Join(root, "hooks", "run-prepare.sh")
		if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
			minimalAgentPackageErr = fmt.Errorf("create hook dir: %w", err)
			return
		}
		if err := os.WriteFile(hookPath, []byte("#!/usr/bin/env bash\nset -euo pipefail\nprintf '{}\\n'\n"), 0o755); err != nil {
			minimalAgentPackageErr = fmt.Errorf("write hook: %w", err)
			return
		}
		minimalAgentPackageDesc, err = testassets.PackDir(root)
		if err != nil {
			minimalAgentPackageErr = fmt.Errorf("pack agent definition: %w", err)
		}
	})
	if minimalAgentPackageErr != nil {
		panic(minimalAgentPackageErr)
	}
	return minimalAgentPackageDesc
}
