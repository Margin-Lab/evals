package app

import (
	"io"
	"testing"

	"github.com/muesli/termenv"
)

type fakeWriter struct{}

func (fakeWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestResolveTUIColorProfile(t *testing.T) {
	origEnvColorProfile := envColorProfile
	origEnvNoColor := envNoColor
	origEnvTerm := envTerm
	origStdinIsTTY := stdinIsTTY
	origWriterIsTTY := writerIsTTY
	t.Cleanup(func() {
		envColorProfile = origEnvColorProfile
		envNoColor = origEnvNoColor
		envTerm = origEnvTerm
		stdinIsTTY = origStdinIsTTY
		writerIsTTY = origWriterIsTTY
	})

	tests := []struct {
		name      string
		profile   termenv.Profile
		noColor   bool
		term      string
		stdinTTY  bool
		writerTTY bool
		want      termenv.Profile
		writer    io.Writer
	}{
		{
			name:      "preserves detected truecolor",
			profile:   termenv.TrueColor,
			term:      "xterm-ghostty",
			stdinTTY:  true,
			writerTTY: true,
			want:      termenv.TrueColor,
			writer:    fakeWriter{},
		},
		{
			name:      "respects no color",
			profile:   termenv.Ascii,
			noColor:   true,
			term:      "xterm-ghostty",
			stdinTTY:  true,
			writerTTY: true,
			want:      termenv.Ascii,
			writer:    fakeWriter{},
		},
		{
			name:      "keeps ascii for dumb terminal",
			profile:   termenv.Ascii,
			term:      "dumb",
			stdinTTY:  true,
			writerTTY: true,
			want:      termenv.Ascii,
			writer:    fakeWriter{},
		},
		{
			name:      "keeps ascii for non tty output",
			profile:   termenv.Ascii,
			term:      "xterm-ghostty",
			stdinTTY:  true,
			writerTTY: false,
			want:      termenv.Ascii,
			writer:    fakeWriter{},
		},
		{
			name:      "falls back to ansi256 for interactive unknown terminal",
			profile:   termenv.Ascii,
			term:      "xterm-ghostty",
			stdinTTY:  true,
			writerTTY: true,
			want:      termenv.ANSI256,
			writer:    fakeWriter{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			envColorProfile = func() termenv.Profile { return tc.profile }
			envNoColor = func() bool { return tc.noColor }
			envTerm = func() string { return tc.term }
			stdinIsTTY = func() bool { return tc.stdinTTY }
			writerIsTTY = func(io.Writer) bool { return tc.writerTTY }

			if got := resolveTUIColorProfile(tc.writer); got != tc.want {
				t.Fatalf("resolveTUIColorProfile() = %v, want %v", got, tc.want)
			}
		})
	}
}
