package missioncontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func Run(ctx context.Context, cfg Config) (Outcome, error) {
	if strings.TrimSpace(cfg.RunID) == "" {
		return Outcome{}, fmt.Errorf("run id is required")
	}
	if cfg.Source == nil {
		return Outcome{}, fmt.Errorf("mission-control source is required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 200 * time.Millisecond
	}
	if cfg.TextPreviewLimit <= 0 {
		cfg.TextPreviewLimit = DefaultTextPreviewLimit
	}

	m := newModel(ctx, cfg)
	program := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		return Outcome{}, err
	}
	resolved, ok := finalModel.(*model)
	if !ok {
		return Outcome{}, fmt.Errorf("unexpected mission-control model type %T", finalModel)
	}
	return resolved.outcome(), nil
}
