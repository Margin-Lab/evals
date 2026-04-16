package missioncontrol

import (
	"bytes"
	"encoding/json"
	"strings"
)

func formatJSONLText(input string, truncated bool) (string, error) {
	lines := completeLogLines(input, truncated)
	if len(lines) == 0 {
		return "", nil
	}

	blocks := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		formatted, err := formatJSONValue(line)
		if err != nil {
			// PTY captures are a mixed byte stream, not a strict JSONL artifact.
			// Preserve non-JSON lines verbatim so readable output never disappears.
			blocks = append(blocks, line)
			continue
		}
		blocks = append(blocks, formatted)
	}
	return strings.Join(blocks, "\n"), nil
}

func formatJSONValue(input string) (string, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(input), "", "  "); err != nil {
		return "", err
	}
	return out.String(), nil
}
