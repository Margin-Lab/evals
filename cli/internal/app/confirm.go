package app

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
)

type runConfirmationAuthItem struct {
	Method      string
	Source      string
	FilePath    string
	Requirement string
}

type runConfirmationSpec struct {
	AgentName       string
	RunID           string
	OutputDir       string
	ResumeFromDir   string
	Auth            []runConfirmationAuthItem
	PruneBuiltImage int
	ExecutionMode   runbundle.ExecutionMode
	ResumeWarning   *resumeWarningSummary
}

func (s runConfirmationSpec) DryRun() bool {
	return s.ExecutionMode == runbundle.ExecutionModeDryRun
}

func (s runConfirmationSpec) OracleRun() bool {
	return s.ExecutionMode == runbundle.ExecutionModeOracleRun
}

func runConfirmationTUI(out io.Writer, spec runConfirmationSpec) (bool, error) {
	configureTUIRenderer(out)
	model := newRunConfirmationModel(spec)
	program := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithInput(runConfirmationInput),
		tea.WithOutput(out),
	)
	finalModel, err := program.Run()
	if err != nil {
		return false, err
	}
	resolved, ok := finalModel.(*runConfirmationModel)
	if !ok {
		return false, fmt.Errorf("unexpected run confirmation model type %T", finalModel)
	}
	return resolved.confirmed, nil
}

type runConfirmationModel struct {
	spec      runConfirmationSpec
	width     int
	height    int
	confirmed bool
}

func newRunConfirmationModel(spec runConfirmationSpec) *runConfirmationModel {
	return &runConfirmationModel{
		spec:   spec,
		width:  110,
		height: 28,
	}
}

func (m *runConfirmationModel) Init() tea.Cmd {
	return nil
}

func (m *runConfirmationModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
	case tea.KeyMsg:
		switch strings.ToLower(typed.String()) {
		case "enter", "y":
			m.confirmed = true
			return m, tea.Quit
		case "esc", "q", "ctrl+c", "n":
			m.confirmed = false
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *runConfirmationModel) View() string {
	width := m.width
	if width <= 0 {
		width = 110
	}
	height := m.height
	if height <= 0 {
		height = 28
	}

	contentWidth := minInt(96, maxInt(72, width-10))
	sections := []string{
		runConfirmationTitle(contentWidth),
		"",
		"",
	}
	if m.spec.ResumeWarning != nil {
		sections = append(sections, renderRunResumeWarningBlock(contentWidth, *m.spec.ResumeWarning))
		sections = append(sections, "", "")
	}
	sections = append(sections, renderRunDestinationBlock(contentWidth, m.spec))
	sections = append(sections, "", "")
	sections = append(sections, renderRunAuthBlock(contentWidth, m.spec))
	if m.spec.PruneBuiltImage > 0 {
		sections = append(sections, renderRunPruneBlock(contentWidth))
	}
	sections = append(sections, "", "")
	sections = append(sections, renderRunConfirmFooter(contentWidth))

	body := lipgloss.JoinVertical(lipgloss.Left, sections...)
	panel := runConfirmationPanelStyle.Width(contentWidth).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

var (
	runConfirmationBorderColor = lipgloss.AdaptiveColor{Light: "240", Dark: "241"}
	runConfirmationTextColor   = lipgloss.AdaptiveColor{Light: "235", Dark: "255"}
	runConfirmationMutedColor  = lipgloss.AdaptiveColor{Light: "240", Dark: "248"}
	runConfirmationWarnBorder  = lipgloss.AdaptiveColor{Light: "220", Dark: "220"}
	runConfirmationWarnText    = lipgloss.AdaptiveColor{Light: "235", Dark: "255"}
	runConfirmationCancelColor = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	runConfirmationOkColor     = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}
)

var (
	runConfirmationPanelStyle = lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					BorderForeground(runConfirmationBorderColor).
					Padding(1, 2)
	runConfirmationTitleStyle = lipgloss.NewStyle().
					Bold(true).
					Underline(true).
					Padding(0, 1).
					Foreground(runConfirmationTextColor)
	runConfirmationSectionTitleStyle = lipgloss.NewStyle().
						Bold(true).
						Underline(true).
						Foreground(runConfirmationWarnBorder)
	runConfirmationMethodStyle = lipgloss.NewStyle().
					Bold(true)
	runConfirmationCredentialStyle = lipgloss.NewStyle().
					Bold(true)
	runConfirmationWarnTextStyle = lipgloss.NewStyle().
					Foreground(runConfirmationWarnText)
	runConfirmationFooterStyle = lipgloss.NewStyle().
					Foreground(runConfirmationMutedColor)
	runConfirmationConfirmStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(runConfirmationOkColor)
	runConfirmationCancelStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(runConfirmationCancelColor)
)

func runConfirmationTitle(width int) string {
	return lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Center).
		Render(runConfirmationTitleStyle.Render("Run Confirmation"))
}

func renderRunAuthBlock(width int, spec runConfirmationSpec) string {
	item := primaryAuthItem(spec.Auth)
	agentName := strings.TrimSpace(spec.AgentName)
	if agentName == "" {
		agentName = "(unknown)"
	}

	lines := []string{
		runConfirmationSectionTitleStyle.Render("Authentication"),
	}
	switch item.Method {
	case "API key":
		if spec.OracleRun() {
			lines = append(lines, runConfirmationWarnTextStyle.Render("Oracle-run mode active: agent authentication is not used in this run."))
			break
		}
		if spec.DryRun() {
			lines = append(lines, runConfirmationWarnTextStyle.Render("Dry-run mode active: No token usage in this run."))
			break
		}
		lines = append(lines, renderStyledWrappedLine(width, []styledSegment{
			{text: "Will use ", style: runConfirmationWarnTextStyle},
			{text: "API key", style: runConfirmationMethodStyle},
			{text: " ", style: runConfirmationWarnTextStyle},
			{text: orUnknown(item.Requirement), style: runConfirmationCredentialStyle},
			{text: " to run the agent " + agentName + ". Please ensure sufficient API credits before confirming the run.", style: runConfirmationWarnTextStyle},
		}))
	case "OAuth credential file":
		if spec.OracleRun() {
			lines = append(lines, runConfirmationWarnTextStyle.Render("Oracle-run mode active: agent authentication is not used in this run."))
			break
		}
		if spec.DryRun() {
			lines = append(lines, runConfirmationWarnTextStyle.Render("Dry-run mode active: No token usage in this run."))
			break
		}
		lines = append(lines, renderStyledWrappedLine(width, []styledSegment{
			{text: "Will use ", style: runConfirmationWarnTextStyle},
			{text: "OAuth file", style: runConfirmationMethodStyle},
			{text: " ", style: runConfirmationWarnTextStyle},
			{text: orUnknown(item.FilePath), style: runConfirmationCredentialStyle},
			{text: " to run the agent " + agentName + ". Note that this will use tokens.", style: runConfirmationWarnTextStyle},
		}))
	case "OAuth credential":
		if spec.OracleRun() {
			lines = append(lines, runConfirmationWarnTextStyle.Render("Oracle-run mode active: agent authentication is not used in this run."))
			break
		}
		if spec.DryRun() {
			lines = append(lines, runConfirmationWarnTextStyle.Render("Dry-run mode active: No token usage in this run."))
			break
		}
		lines = append(lines, renderStyledWrappedLine(width, []styledSegment{
			{text: "Will use ", style: runConfirmationWarnTextStyle},
			{text: "OAuth credential", style: runConfirmationMethodStyle},
			{text: " from ", style: runConfirmationWarnTextStyle},
			{text: orUnknown(item.Source), style: runConfirmationCredentialStyle},
			{text: " to run the agent " + agentName + ". Note that this will use tokens.", style: runConfirmationWarnTextStyle},
		}))
	default:
		if spec.OracleRun() {
			lines = append(lines, runConfirmationWarnTextStyle.Render("Oracle-run mode active: agent authentication is not used in this run."))
			break
		}
		if spec.DryRun() {
			lines = append(lines, runConfirmationWarnTextStyle.Render("Dry-run mode active: No token usage in this run."))
			break
		}
		lines = append(lines, runConfirmationWarnTextStyle.Render("Will run the agent "+agentName+"."))
	}
	return strings.Join(lines, "\n")
}

func renderRunDestinationBlock(width int, spec runConfirmationSpec) string {
	lines := []string{
		runConfirmationSectionTitleStyle.Render("Run Destination"),
		runConfirmationWarnTextStyle.Render("Run ID: " + orUnknown(spec.RunID)),
		runConfirmationWarnTextStyle.Render("Output: " + orUnknown(spec.OutputDir)),
	}
	if strings.TrimSpace(spec.ResumeFromDir) != "" {
		lines = append(lines, runConfirmationWarnTextStyle.Render("Resume from: "+spec.ResumeFromDir))
	}
	return strings.Join(lines, "\n")
}

func renderRunResumeWarningBlock(width int, summary resumeWarningSummary) string {
	lines := []string{
		runConfirmationSectionTitleStyle.Render("Resume Warning"),
	}
	for _, line := range resumeWarningLines(summary) {
		wrapped := wrapWarningText(line, maxInt(12, width))
		if len(wrapped) == 0 {
			continue
		}
		for _, part := range wrapped {
			lines = append(lines, runConfirmationWarnTextStyle.Render(part))
		}
	}
	return strings.Join(lines, "\n")
}

func renderRunPruneBlock(width int) string {
	lines := []string{
		runConfirmationSectionTitleStyle.Render("Docker Image Pruning"),
	}
	for _, line := range wrapWarningText("--prune-built-image enabled, this will prune all unused docker images intermittently", maxInt(12, width)) {
		lines = append(lines, runConfirmationWarnTextStyle.Render(line))
	}
	return strings.Join(lines, "\n")
}

func renderRunConfirmFooter(width int) string {
	text := runConfirmationConfirmStyle.Render("Enter") +
		runConfirmationFooterStyle.Render(" confirm   ") +
		runConfirmationCancelStyle.Render("Esc") +
		runConfirmationFooterStyle.Render(" cancel")
	return lipgloss.NewStyle().Width(width).Render(text)
}

func primaryAuthItem(items []runConfirmationAuthItem) runConfirmationAuthItem {
	if len(items) == 0 {
		return runConfirmationAuthItem{}
	}
	return items[0]
}

func orUnknown(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "(unknown)"
	}
	return trimmed
}

type styledSegment struct {
	text  string
	style lipgloss.Style
}

func renderStyledWrappedLine(width int, segments []styledSegment) string {
	lines := wrapStyledSegments(maxInt(12, width), segments)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		var b strings.Builder
		for _, seg := range line {
			b.WriteString(seg.style.Render(seg.text))
		}
		rendered = append(rendered, b.String())
	}
	return strings.Join(rendered, "\n")
}

func wrapStyledSegments(width int, segments []styledSegment) [][]styledSegment {
	if width <= 0 {
		return [][]styledSegment{segments}
	}

	var lines [][]styledSegment
	var current []styledSegment
	currentWidth := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		lines = append(lines, current)
		current = nil
		currentWidth = 0
	}

	for _, segment := range segments {
		for _, token := range tokenizeForWrap(segment.text) {
			tokenWidth := utf8.RuneCountInString(token)
			if strings.TrimSpace(token) == "" {
				if currentWidth == 0 {
					continue
				}
				current = append(current, styledSegment{text: token, style: segment.style})
				currentWidth += tokenWidth
				continue
			}
			for tokenWidth > width {
				if currentWidth > 0 {
					flush()
				}
				runes := []rune(token)
				chunk := string(runes[:width])
				lines = append(lines, []styledSegment{{text: chunk, style: segment.style}})
				token = string(runes[width:])
				tokenWidth = utf8.RuneCountInString(token)
			}
			if currentWidth+tokenWidth > width && currentWidth > 0 {
				flush()
			}
			current = append(current, styledSegment{text: token, style: segment.style})
			currentWidth += tokenWidth
		}
	}

	flush()
	if len(lines) == 0 {
		return [][]styledSegment{{}}
	}
	return lines
}

func tokenizeForWrap(text string) []string {
	if text == "" {
		return nil
	}
	var tokens []string
	var current []rune
	inSpace := false
	for i, r := range text {
		space := r == ' ' || r == '\t'
		if i == 0 {
			inSpace = space
			current = append(current, r)
			continue
		}
		if space == inSpace {
			current = append(current, r)
			continue
		}
		tokens = append(tokens, string(current))
		current = []rune{r}
		inSpace = space
	}
	if len(current) > 0 {
		tokens = append(tokens, string(current))
	}
	return tokens
}

func wrapWarningText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	lines := []string{}
	current := words[0]
	for _, word := range words[1:] {
		candidate := current + " " + word
		if utf8.RuneCountInString(candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	lines = append(lines, current)
	return lines
}

func wrapHard(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, (len(runes)+width-1)/width)
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	lines = append(lines, string(runes))
	return lines
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
