package app

import (
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	termx "github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

var (
	envColorProfile = termenv.EnvColorProfile
	envNoColor      = termenv.EnvNoColor
	envTerm         = func() string { return os.Getenv("TERM") }
	stdinIsTTY      = func() bool { return termx.IsTerminal(os.Stdin.Fd()) }
	writerIsTTY     = func(w io.Writer) bool {
		fdWriter, ok := w.(interface{ Fd() uintptr })
		if !ok {
			return false
		}
		return termx.IsTerminal(fdWriter.Fd())
	}
)

func configureTUIRenderer(out io.Writer) {
	renderer := lipgloss.DefaultRenderer()
	renderer.SetOutput(termenv.NewOutput(out))
	renderer.SetColorProfile(resolveTUIColorProfile(out))
}

func resolveTUIColorProfile(out io.Writer) termenv.Profile {
	profile := envColorProfile()
	if profile != termenv.Ascii {
		return profile
	}
	if envNoColor() {
		return termenv.Ascii
	}

	term := strings.TrimSpace(envTerm())
	if term == "" || strings.EqualFold(term, "dumb") {
		return termenv.Ascii
	}
	if !stdinIsTTY() || !writerIsTTY(out) {
		return termenv.Ascii
	}

	// Modern interactive terminals occasionally report an unknown TERM string.
	// In that case prefer a safe 256-color fallback over no color at all.
	return termenv.ANSI256
}
