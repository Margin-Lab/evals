package runbundle

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

func validBundle() Bundle {
	return Bundle{
		SchemaVersion: SchemaVersionV1,
		BundleID:      "bun_123",
		CreatedAt:     time.Date(2026, 2, 26, 18, 0, 0, 0, time.UTC),
		Source: Source{
			Kind:            SourceKindLocalFiles,
			SubmitProjectID: "proj_123",
		},
		ResolvedSnapshot: ResolvedSnapshot{
			Name: "smoke",
			Execution: Execution{
				Mode:                  ExecutionModeFull,
				MaxConcurrency:        8,
				FailFast:              false,
				InstanceTimeoutSecond: 1200,
			},
			Agent: minimalAgent(),
			RunDefaults: RunDefault{
				Cwd: "/work",
				Env: map[string]string{"TERM": "xterm-256color"},
				PTY: PTY{Cols: 120, Rows: 40},
			},
			Cases: []Case{{
				CaseID:            "repo-build",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     "Fix build",
				TestCommand:       []string{"bash", "-lc", "./test.sh"},
				TestCwd:           "/work",
				TestTimeoutSecond: 900,
				TestAssets:        minimalTestAssets(),
			}},
		},
	}
}

func minimalTestAssets() TestAssets {
	return TestAssets{
		ArchiveTGZBase64: "H4sIAAAAAAAC/+3NQQrCMBSE4axziohrTVRiz5PCkwaklrzE8xu6KbhXBP9vM8NsporWo07mk0I3xLhm954hxMvW1304X0/GBfMFTWsq/dL8p/3ONy1+zLOX+enGpJNVqe4g7eGWvMgt5butpYk1AAAAAAAAAAAAAAAAAIDf8QLX6+4FACgAAA==",
		ArchiveTGZSHA256: "32e2807f93a86a87c24cd79cfde22b62adb1861c1778c1d6218a13baa38c285c",
		ArchiveTGZBytes:  136,
	}
}

var (
	minimalAgentPackageOnce sync.Once
	minimalAgentPackageDesc testassets.Descriptor
	minimalAgentPackageErr  error
)

func minimalAgent() Agent {
	return Agent{
		Definition: agentdef.DefinitionSnapshot{
			Manifest: agentdef.Manifest{
				Kind: "agent_definition",
				Name: "fixture-agent",
				Run: agentdef.RunSpec{
					PrepareHook: agentdef.HookRef{Path: "hooks/run-prepare.sh"},
				},
			},
			Package: minimalAgentPackage(),
		},
		Config: agentdef.ConfigSpec{
			Name: "fixture-agent-default",
			Mode: agentdef.ConfigModeDirect,
			Input: map[string]any{
				"command": []any{"bash", "-lc", "echo hello"},
			},
		},
	}
}

func minimalAgentPackage() testassets.Descriptor {
	minimalAgentPackageOnce.Do(func() {
		root, err := os.MkdirTemp("", "runbundle-agentdef-*")
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
