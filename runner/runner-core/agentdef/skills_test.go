package agentdef

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkillSpecFromDirReadsFrontmatterAndPackagesContents(t *testing.T) {
	t.Parallel()

	root := createSkillDir(t, "db-migration", "Use when creating or reviewing database migrations.")
	writeSkillFile(t, filepath.Join(root, "tools", "check.sh"), "#!/usr/bin/env bash\necho ok\n", 0o755)

	skill, err := LoadSkillSpecFromDir(root)
	if err != nil {
		t.Fatalf("LoadSkillSpecFromDir() error = %v", err)
	}
	if skill.Name != "db-migration" {
		t.Fatalf("skill name = %q", skill.Name)
	}
	if skill.Description != "Use when creating or reviewing database migrations." {
		t.Fatalf("skill description = %q", skill.Description)
	}
	body, err := ReadPackageFile(skill.Package, "tools/check.sh")
	if err != nil {
		t.Fatalf("ReadPackageFile() error = %v", err)
	}
	if !strings.Contains(string(body), "echo ok") {
		t.Fatalf("packaged file body = %q", string(body))
	}
}

func TestValidateAndNormalizeSkillSpecsRejectDuplicateNames(t *testing.T) {
	t.Parallel()

	first, err := LoadSkillSpecFromDir(createSkillDir(t, "db-migration", "first"))
	if err != nil {
		t.Fatalf("load first skill: %v", err)
	}
	second, err := LoadSkillSpecFromDir(createSkillDir(t, "db-migration", "second"))
	if err != nil {
		t.Fatalf("load second skill: %v", err)
	}

	_, err = ValidateAndNormalizeSkillSpecs([]SkillSpec{first, second})
	if err == nil || !strings.Contains(err.Error(), `duplicate skill name "db-migration"`) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestValidateAndNormalizeConfigSpecRejectsSkillsWhenDefinitionDoesNotSupportThem(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Run:  RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
	})
	skill, err := LoadSkillSpecFromDir(createSkillDir(t, "db-migration", "migration help"))
	if err != nil {
		t.Fatalf("load skill: %v", err)
	}

	_, err = ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name:   "fixture-direct",
		Mode:   ConfigModeDirect,
		Skills: []SkillSpec{skill},
		Input:  map[string]any{"command": "echo hello"},
	})
	if err == nil || !strings.Contains(err.Error(), "selected agent definition does not support skills") {
		t.Fatalf("expected unsupported skills error, got %v", err)
	}
}

func TestValidateAndNormalizeConfigSpecSortsSkillsWhenSupported(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Skills: &SkillManifestSpec{
			HomeRelDir: ".agents/skills",
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
	})
	zeta, err := LoadSkillSpecFromDir(createSkillDir(t, "zeta", "zeta skill"))
	if err != nil {
		t.Fatalf("load zeta skill: %v", err)
	}
	alpha, err := LoadSkillSpecFromDir(createSkillDir(t, "alpha", "alpha skill"))
	if err != nil {
		t.Fatalf("load alpha skill: %v", err)
	}

	config, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name:   "fixture-direct",
		Mode:   ConfigModeDirect,
		Skills: []SkillSpec{zeta, alpha},
		Input:  map[string]any{"command": "echo hello"},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigSpec() error = %v", err)
	}
	if got := []string{config.Skills[0].Name, config.Skills[1].Name}; got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("sorted skills = %#v", got)
	}
}

func createSkillDir(t *testing.T, name, description string) string {
	t.Helper()

	root := t.TempDir()
	writeSkillFile(t, filepath.Join(root, "SKILL.md"), `---
name: `+name+`
description: `+description+`
---

Skill body.
`, 0o644)
	return root
}

func writeSkillFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
