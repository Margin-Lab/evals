package main

import (
	"context"
	"fmt"
	"os"

	"github.com/marginlab/margin-eval/cli/internal/app"
)

func main() {
	if err := app.New(os.Stdout, os.Stderr).Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
