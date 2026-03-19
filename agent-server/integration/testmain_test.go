//go:build integration

package integration

import (
	"fmt"
	"os"
	"testing"

	"github.com/joho/godotenv"
)

func TestMain(m *testing.M) {
	dotenvPath := integrationDotenvPath()
	if _, err := os.Stat(dotenvPath); err == nil {
		if err := godotenv.Overload(dotenvPath); err != nil {
			fmt.Fprintf(os.Stderr, "load integration dotenv file %q: %v\n", dotenvPath, err)
			os.Exit(1)
		}
	}

	os.Exit(m.Run())
}

func integrationDotenvPath() string {
	if raw := os.Getenv(envITDotenvFile); raw != "" {
		return raw
	}
	return "../.env"
}
