package scaffold

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestInitSuiteAndCase(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	suitePath := filepath.Join(root, "suites", "smoke")

	if err := InitSuite(suitePath, "smoke"); err != nil {
		t.Fatalf("InitSuite() error = %v", err)
	}
	gotName, err := InitCase(suitePath, "repo-build")
	if err != nil {
		t.Fatalf("InitCase() error = %v", err)
	}
	if gotName != "repo-build" {
		t.Fatalf("InitCase() name = %q, want %q", gotName, "repo-build")
	}

	mustExist(t, filepath.Join(suitePath, "suite.toml"))
	mustExist(t, filepath.Join(suitePath, "cases", "repo-build", "case.toml"))
	mustExist(t, filepath.Join(suitePath, "cases", "repo-build", "prompt.md"))
	testScript := filepath.Join(suitePath, "cases", "repo-build", "tests", "test.sh")
	mustExist(t, testScript)

	info, err := os.Stat(testScript)
	if err != nil {
		t.Fatalf("Stat(%s): %v", testScript, err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("test.sh perm = %o, want 755", info.Mode().Perm())
	}

	cases := mustReadSuiteCases(t, filepath.Join(suitePath, "suite.toml"))
	if !reflect.DeepEqual(cases, []string{"repo-build"}) {
		t.Fatalf("suite.toml cases = %#v, want %#v", cases, []string{"repo-build"})
	}
}

func TestInitCaseGeneratesNameAndAppendsToSuite(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	suitePath := filepath.Join(root, "suites", "smoke")

	if err := InitSuite(suitePath, "smoke"); err != nil {
		t.Fatalf("InitSuite() error = %v", err)
	}
	name1, err := InitCase(suitePath, "")
	if err != nil {
		t.Fatalf("InitCase() first error = %v", err)
	}
	name2, err := InitCase(suitePath, "")
	if err != nil {
		t.Fatalf("InitCase() second error = %v", err)
	}

	if name1 != "case_1" || name2 != "case_2" {
		t.Fatalf("generated names = (%q, %q), want (%q, %q)", name1, name2, "case_1", "case_2")
	}
	mustExist(t, filepath.Join(suitePath, "cases", "case_1", "case.toml"))
	mustExist(t, filepath.Join(suitePath, "cases", "case_2", "case.toml"))

	cases := mustReadSuiteCases(t, filepath.Join(suitePath, "suite.toml"))
	if !reflect.DeepEqual(cases, []string{"case_1", "case_2"}) {
		t.Fatalf("suite.toml cases = %#v, want %#v", cases, []string{"case_1", "case_2"})
	}
}

func TestInitAgentDefinition(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	definitionPath := filepath.Join(root, "definitions", "custom-agent")

	if err := InitAgentDefinition(definitionPath, "custom-agent"); err != nil {
		t.Fatalf("InitAgentDefinition() error = %v", err)
	}

	mustExist(t, filepath.Join(definitionPath, "definition.toml"))
	mustExist(t, filepath.Join(definitionPath, "schema.json"))
	mustExist(t, filepath.Join(definitionPath, "hooks", "install-check.sh"))
	mustExist(t, filepath.Join(definitionPath, "hooks", "install-run.sh"))
	mustExist(t, filepath.Join(definitionPath, "hooks", "run-prepare.js"))
}

func TestInitAgentConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	definitionPath := filepath.Join(root, "definitions", "custom-agent")
	configPath := filepath.Join(root, "configs", "agents", "custom-default")

	if err := InitAgentDefinition(definitionPath, "custom-agent"); err != nil {
		t.Fatalf("InitAgentDefinition() error = %v", err)
	}
	if err := InitAgentConfig(configPath, "custom-default", definitionPath); err != nil {
		t.Fatalf("InitAgentConfig() error = %v", err)
	}

	configTomlPath := filepath.Join(configPath, "config.toml")
	mustExist(t, configTomlPath)
	body, err := os.ReadFile(configTomlPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml): %v", err)
	}
	if !strings.Contains(string(body), `definition = "../../../definitions/custom-agent"`) {
		t.Fatalf("config.toml missing definition reference")
	}
	if !strings.Contains(string(body), `command = ["bash", "-lc", "echo custom agent ready"]`) {
		t.Fatalf("config.toml missing starter command")
	}
}

func TestInitEvalConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	evalPath := filepath.Join(root, "configs", "evals", "smoke.toml")

	if err := InitEvalConfig(evalPath, "smoke"); err != nil {
		t.Fatalf("InitEvalConfig() error = %v", err)
	}
	mustExist(t, evalPath)
	body, err := os.ReadFile(evalPath)
	if err != nil {
		t.Fatalf("ReadFile(eval): %v", err)
	}
	if !strings.Contains(string(body), "retry_count = 1") {
		t.Fatalf("eval config missing retry_count default")
	}
}

func TestInitRefusesOverwrite(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	suitePath := filepath.Join(root, "suites", "smoke")

	if err := InitSuite(suitePath, "smoke"); err != nil {
		t.Fatalf("InitSuite() first call error = %v", err)
	}
	err := InitSuite(suitePath, "smoke")
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected overwrite rejection, got %v", err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func mustReadSuiteCases(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var parsed struct {
		Cases []string `toml:"cases"`
	}
	if err := toml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("toml.Unmarshal(%s): %v", path, err)
	}
	return parsed.Cases
}
