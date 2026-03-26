package localexecutor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
)

var (
	authSourceGOOS = func() string {
		return runtime.GOOS
	}
	runAuthSourceCommand = func(name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			trimmed := strings.TrimSpace(string(output))
			if trimmed == "" {
				return nil, err
			}
			return nil, fmt.Errorf("%s: %w", trimmed, err)
		}
		return output, nil
	}
)

type resolvedAuthSource struct {
	Payload     []byte
	SourceKind  string
	SourceLabel string
}

func resolveOverrideAuthFile(path string) (resolvedAuthSource, error) {
	return resolveAuthFilePayload(path, "override_file")
}

func resolveLocalCredentialSource(source agentdef.AuthLocalSource, homeDir string) (resolvedAuthSource, error) {
	switch source.Kind {
	case agentdef.AuthLocalSourceKindHomeFile:
		path := filepath.Join(homeDir, filepath.FromSlash(strings.TrimSpace(source.HomeRelPath)))
		return resolveAuthFilePayload(path, "home_file")
	case agentdef.AuthLocalSourceKindMacOSKeychain:
		return resolveMacOSKeychainSource(source)
	default:
		return resolvedAuthSource{}, fmt.Errorf("source kind %q is not supported", strings.TrimSpace(string(source.Kind)))
	}
}

func resolveAuthFilePayload(path string, sourceKind string) (resolvedAuthSource, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return resolvedAuthSource{}, fmt.Errorf("%s path %q is invalid: %w", sourceKind, path, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return resolvedAuthSource{}, fmt.Errorf("%s %q is unavailable: %w", sourceKind, absPath, err)
	}
	if info.IsDir() {
		return resolvedAuthSource{}, fmt.Errorf("%s %q must be a file", sourceKind, absPath)
	}
	payload, err := os.ReadFile(absPath)
	if err != nil {
		return resolvedAuthSource{}, fmt.Errorf("read %s %q: %w", sourceKind, absPath, err)
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return resolvedAuthSource{}, fmt.Errorf("%s %q is empty", sourceKind, absPath)
	}
	return resolvedAuthSource{
		Payload:     payload,
		SourceKind:  sourceKind,
		SourceLabel: absPath,
	}, nil
}

func resolveMacOSKeychainSource(source agentdef.AuthLocalSource) (resolvedAuthSource, error) {
	if !sourceAppliesToCurrentPlatform(source.Platforms) {
		return resolvedAuthSource{}, fmt.Errorf("macos_keychain %q is not available on %s", strings.TrimSpace(source.Service), authSourceGOOS())
	}
	if authSourceGOOS() != "darwin" {
		return resolvedAuthSource{}, fmt.Errorf("macos_keychain %q is only supported on darwin", strings.TrimSpace(source.Service))
	}
	service := strings.TrimSpace(source.Service)
	output, err := runAuthSourceCommand("security", "find-generic-password", "-s", service, "-w")
	if err != nil {
		return resolvedAuthSource{}, fmt.Errorf("macos_keychain %q is unavailable: %w", service, err)
	}
	payload := bytes.TrimSpace(output)
	if len(payload) == 0 {
		return resolvedAuthSource{}, fmt.Errorf("macos_keychain %q returned an empty payload", service)
	}
	return resolvedAuthSource{
		Payload:     payload,
		SourceKind:  "macos_keychain",
		SourceLabel: service,
	}, nil
}

func sourceAppliesToCurrentPlatform(platforms []string) bool {
	if len(platforms) == 0 {
		return true
	}
	goos := authSourceGOOS()
	for _, platform := range platforms {
		if strings.EqualFold(strings.TrimSpace(platform), goos) {
			return true
		}
	}
	return false
}

func validateCredentialPayload(credential agentdef.AuthLocalCredential, payload []byte) error {
	validatePath := strings.TrimSpace(credential.ValidateJSONPath)
	if validatePath == "" {
		return nil
	}
	var doc any
	if err := json.Unmarshal(payload, &doc); err != nil {
		return fmt.Errorf("payload is not valid JSON: %w", err)
	}
	value, ok := lookupJSONPath(doc, validatePath)
	if !ok {
		return fmt.Errorf("payload is missing JSON path %q", validatePath)
	}
	if text, isString := value.(string); isString && strings.TrimSpace(text) == "" {
		return fmt.Errorf("payload JSON path %q is empty", validatePath)
	}
	return nil
}

func lookupJSONPath(doc any, path string) (any, bool) {
	current := doc
	for _, segment := range strings.Split(path, ".") {
		key := strings.TrimSpace(segment)
		if key == "" {
			return nil, false
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := obj[key]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}
