//go:build integration

package integration

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	dotenvPath := integrationDotenvPath()
	if _, err := os.Stat(dotenvPath); err == nil {
		if err := loadDotenvFile(dotenvPath); err != nil {
			fmt.Fprintf(os.Stderr, "load integration dotenv file %q: %v\n", dotenvPath, err)
			os.Exit(1)
		}
	}

	os.Exit(m.Run())
}

func integrationDotenvPath() string {
	if raw := strings.TrimSpace(os.Getenv(envITDotenvFile)); raw != "" {
		return raw
	}
	return filepath.Join(repoRootFromCaller(), ".env")
}

func loadDotenvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}
