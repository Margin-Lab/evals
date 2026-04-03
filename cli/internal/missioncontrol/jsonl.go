package missioncontrol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func formatJSONLText(input string, truncated bool) (string, error) {
	lines := completeLogLines(input, truncated)
	if len(lines) == 0 {
		return "", nil
	}

	blocks := make([]string, 0, len(lines))
	var firstErr error
	for idx, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		formatted, err := formatJSONValue(line)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("jsonl parse error at line %d: %w", idx+1, err)
			}
			continue
		}
		blocks = append(blocks, formatted)
	}
	if len(blocks) == 0 && firstErr != nil {
		return "", firstErr
	}
	return strings.Join(blocks, "\n\n"), nil
}

func formatJSONValue(input string) (string, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(input), "", "  "); err != nil {
		return "", err
	}
	return out.String(), nil
}
