package missioncontrol

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

// ---------------------------------------------------------------------------
// Main view
// ---------------------------------------------------------------------------

func (m *model) View() string {
	if !m.snapshotLoaded {
		if m.errMsg != "" {
			return errorStyle.Render(m.errMsg)
		}
		return m.loadingSpinner.View() + " Loading run snapshot..."
	}

	layout := m.computeScreenLayout()
	width := layout.Width
	header := m.renderHeader(width)
	footer := m.renderFooter(width)
	body := m.renderBody(layout)

	out := strings.Join([]string{header, body, footer}, "\n")
	if m.confirmQuit {
		out += "\n" + m.renderQuitConfirm(width)
	}
	if m.errMsg != "" {
		out += "\n" + errorStyle.Render(m.errMsg)
	}
	return out
}

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

func (m *model) renderHeader(width int) string {
	run := m.snapshot.Run
	counts := run.Counts

	title := headerTitleStyle.Render("Run ") + headerRunIDStyle.Render(run.RunID)
	badge := runStateBadge(string(run.State))

	var countParts []string
	if counts.Pending > 0 {
		countParts = append(countParts,
			countLabelStyle.Render("pending:")+mutedStyle.Render(fmt.Sprintf("%d", counts.Pending)))
	}
	if counts.Running > 0 {
		countParts = append(countParts,
			countLabelStyle.Render("running:")+primaryStyle.Render(fmt.Sprintf("%d", counts.Running)))
	}
	if counts.Succeeded > 0 {
		countParts = append(countParts,
			countLabelStyle.Render("pass:")+okStyle.Render(fmt.Sprintf("%d", counts.Succeeded)))
	}
	if counts.TestFailed > 0 {
		countParts = append(countParts,
			countLabelStyle.Render("test_fail:")+badStyle.Render(fmt.Sprintf("%d", counts.TestFailed)))
	}
	if counts.InfraFailed > 0 {
		countParts = append(countParts,
			countLabelStyle.Render("infra_fail:")+badStyle.Render(fmt.Sprintf("%d", counts.InfraFailed)))
	}
	if counts.Canceled > 0 {
		countParts = append(countParts,
			countLabelStyle.Render("cancel:")+warnStyle.Render(fmt.Sprintf("%d", counts.Canceled)))
	}

	line := title + "  " + badge
	if len(countParts) > 0 {
		line += "  " + strings.Join(countParts, "  ")
	}

	perfParts := []string{
		countLabelStyle.Render("elapsed:") + stateValueStyle.Render(formatRunElapsed(run)),
		countLabelStyle.Render("rate:") + stateValueStyle.Render(formatRunCompletionRate(run)),
		countLabelStyle.Render("pass/fail:") + stateValueStyle.Render(formatRunPassFailRate(counts)),
	}
	perfLine := strings.Join(perfParts, "  ")

	sep := separatorStyle.Render(strings.Repeat("─", width))
	return line + "\n" + perfLine + "\n" + sep
}

// ---------------------------------------------------------------------------
// Body (two-pane layout)
// ---------------------------------------------------------------------------

func (m *model) renderBody(layout screenLayout) string {
	paneGap := "  "
	leftBorder := paneBorderStyle
	rightBorder := paneBorderFocusedStyle
	if m.focusedPane == paneLeft {
		leftBorder = paneBorderFocusedStyle
		rightBorder = paneBorderStyle
	}

	left := leftBorder.Width(layout.LeftPane.Outer.Width - 2).Height(layout.LeftPane.Outer.Height - 2).
		Render(m.renderInstances(layout.LeftPane.Inner.Width, layout.LeftPane.Inner.Height))
	right := rightBorder.Width(layout.RightPane.Pane.Outer.Width - 2).Height(layout.RightPane.Pane.Outer.Height - 2).
		Render(m.renderRightPane(layout.RightPane.Pane.Inner.Width, layout.RightPane.Pane.Inner.Height))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, paneGap, right)
}

// ---------------------------------------------------------------------------
// Left pane: instance list
// ---------------------------------------------------------------------------

func (m *model) renderInstances(width, height int) string {
	if height <= 0 {
		return ""
	}

	title := instanceHeaderStyle.Render("Instances")
	total := len(m.snapshot.Instances)
	if total == 0 {
		if height > 1 {
			return title + "\n" + mutedStyle.Render("  No instances yet")
		}
		return title
	}

	// Mini summary line
	counts := m.snapshot.Run.Counts
	summary := mutedStyle.Render(fmt.Sprintf("  %d total", total))
	if counts.Succeeded > 0 || counts.TestFailed > 0 || counts.InfraFailed > 0 {
		summary += "  " + okStyle.Render(fmt.Sprintf("%d pass", counts.Succeeded))
		if counts.TestFailed > 0 {
			summary += mutedStyle.Render("/") + badStyle.Render(fmt.Sprintf("%d test_fail", counts.TestFailed))
		}
		if counts.InfraFailed > 0 {
			summary += mutedStyle.Render("/") + badStyle.Render(fmt.Sprintf("%d infra_fail", counts.InfraFailed))
		}
	}

	lines := []string{title, summary}
	visibleRows := maxInt(0, height-2) // -2 for title + summary
	if visibleRows == 0 {
		return strings.Join(lines, "\n")
	}
	m.clampInstanceOffset(visibleRows)

	end := minInt(m.instancesOffset+visibleRows, total)
	for i := m.instancesOffset; i < end; i++ {
		item := m.snapshot.Instances[i]
		icon := instanceStateIcon(item.Instance.State)
		caseID := strings.TrimSpace(item.Instance.Case.CaseID)
		if caseID == "" {
			caseID = "(no-case-id)"
		}

		cursor := "  "
		if i == m.selectedIdx {
			cursor = "▸ "
		}

		ordinal := fmt.Sprintf("%03d", item.Instance.Ordinal)
		prefix := fmt.Sprintf("%s%s %s  ", cursor, icon, ordinal)
		prefixWidth := lipgloss.Width(prefix)
		var row string
		if prefixWidth >= width {
			row = padRight(truncateText(prefix, width), width)
		} else {
			state := simplifiedStateLabelForInstanceState(item.Instance.State)
			remaining := width - prefixWidth
			stateWidth := minInt(20, maxInt(10, remaining/3))
			if stateWidth > remaining {
				stateWidth = remaining
			}
			caseWidth := remaining - stateWidth
			caseGap := 0
			if caseWidth >= 3 {
				caseGap = 2
				caseWidth -= caseGap
			}

			row = prefix + padRight(truncateText(state, stateWidth), stateWidth)
			if caseWidth > 0 {
				row += strings.Repeat(" ", caseGap) + padRight(truncateText(caseID, caseWidth), caseWidth)
			}
		}

		if i == m.selectedIdx {
			row = instanceSelectedStyle.Render(padRight(row, width))
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Right pane: unified detail view
// ---------------------------------------------------------------------------

func (m *model) renderRightPane(width, height int) string {
	if height <= 0 {
		return ""
	}

	inst := m.selectedInstance()
	if inst == nil {
		return mutedStyle.Render("Select an instance to view details")
	}

	identityParts := []string{m.renderIdentityBar(inst, width)}
	if eb := renderErrorBanner(inst, width); eb != "" {
		identityParts = append(identityParts, eb)
	}

	topSections := []string{
		strings.Join(identityParts, "\n"),
		strings.Join([]string{
			renderSectionHeader("States", width),
			m.renderStateBreadcrumb(width, simplifiedStateForInstanceState(inst.Instance.State)),
		}, "\n"),
		renderSectionHeader("Logs", width),
	}
	top := strings.Join(topSections, "\n\n")
	logHeight := height - lipgloss.Height(top) - 1
	if logHeight <= 0 {
		return truncateLines(top, height)
	}

	logPanel := m.renderSelectedStateLogs(width, logHeight)
	return top + "\n" + logPanel
}

func renderSectionHeader(title string, width int) string {
	prefix := "── "
	suffix := " "
	titleLen := len([]rune(prefix)) + len([]rune(title)) + len([]rune(suffix))
	ruleLen := maxInt(0, width-titleLen)
	return separatorStyle.Render(prefix) +
		stateSectionStyle.Render(title) +
		separatorStyle.Render(suffix+strings.Repeat("─", ruleLen))
}

// ---------------------------------------------------------------------------
// Identity bar
// ---------------------------------------------------------------------------

func (m *model) renderIdentityBar(inst *runnerapi.InstanceSnapshot, width int) string {
	instLine := renderStyledKeyValue("inst", inst.Instance.InstanceID, width)
	caseID := strings.TrimSpace(inst.Instance.Case.CaseID)
	if caseID == "" {
		return instLine
	}
	caseLine := renderStyledKeyValue("case", caseID, width)
	combined := instLine + "  " + caseLine
	if width <= 0 || lipgloss.Width(combined) <= width {
		return combined
	}
	return instLine + "\n" + caseLine
}

// ---------------------------------------------------------------------------
// Error banner
// ---------------------------------------------------------------------------

func renderErrorBanner(inst *runnerapi.InstanceSnapshot, width int) string {
	if inst.Result == nil {
		return ""
	}
	code := strings.TrimSpace(inst.Result.ErrorCode)
	msg := strings.TrimSpace(inst.Result.ErrorMessage)
	if code == "" && msg == "" {
		return ""
	}
	label := errorBannerKeyStyle.Render("error: ")
	value := code
	if msg != "" {
		if value != "" {
			value += " — "
		}
		value += msg
	}
	labelWidth := lipgloss.Width(label)
	available := width - labelWidth
	if available > 0 {
		value = truncateText(value, available)
	}
	return label + errorBannerValueStyle.Render(value)
}

// renderStateBreadcrumb renders a horizontal state-transition diagram.
// Each lifecycle state is a bordered box; arrows connect them.
// Index comparisons use the global simplifiedStates slice which is ordered by
// lifecycle progression (pending → building → ... → terminal).
func (m *model) renderStateBreadcrumb(width int, current simplifiedState) string {
	return m.buildStateBreadcrumbLayout(width, current, 0, 0).Rendered
}

func (m *model) renderSelectedStateLogs(width, height int) string {
	if height <= 0 {
		return ""
	}

	spec := m.selectedSimplifiedStateSpec()

	m.logViewport.Width = maxInt(10, width)

	if len(spec.LogStreams) == 0 {
		m.logViewport.Height = maxInt(1, height)
		m.refreshLogViewportContent(m.logViewport.Width)
		return m.logViewport.View()
	}

	// Status indicators only (label already shown in breadcrumb)
	var parts []string
	if m.logLoading {
		parts = append(parts, logStatusStyle.Render("loading..."))
	} else if m.logStatus != "" {
		parts = append(parts, logStatusStyle.Render(m.logStatus))
	}
	if m.logTruncated {
		parts = append(parts, warnStyle.Render("[truncated]"))
	}
	if m.logFollowTail {
		parts = append(parts, logStatusStyle.Render("[follow]"))
	} else {
		parts = append(parts, warnStyle.Render("[paused]"))
	}

	status := strings.Join(parts, " ")
	if height == 1 {
		return status
	}

	m.logViewport.Height = maxInt(1, height-1)
	m.refreshLogViewportContent(m.logViewport.Width)
	return status + "\n" + m.logViewport.View()
}

func renderStateLogSections(sections []stateLogSection, width int) string {
	rendered := make([]string, 0, len(sections)*2)
	showHeaders := len(sections) > 1
	for _, section := range sections {
		if showHeaders {
			rendered = append(rendered, renderSectionHeader(section.Title, width))
		}
		rendered = append(rendered, renderStateLogSectionBody(section, width))
	}
	return strings.Join(rendered, "\n\n")
}

func renderStateLogSectionBody(section stateLogSection, width int) string {
	switch section.Render {
	case logRenderStructuredKV:
		if len(section.Records) == 0 {
			return mutedStyle.Render(emptyDash(section.EmptyMessage))
		}
		return renderStructuredLogCards(section.Records, width)
	case logRenderJSONL, logRenderRaw:
		if section.Text == "" {
			return mutedStyle.Render(emptyDash(section.EmptyMessage))
		}
		return wrapTextHardWithLineNumbers(section.Text, width)
	default:
		return mutedStyle.Render(emptyDash(section.EmptyMessage))
	}
}

// ---------------------------------------------------------------------------
// Structured log cards
// ---------------------------------------------------------------------------

func renderStructuredLogCards(records []structuredLogRecord, width int) string {
	cards := make([]string, 0, len(records))
	for _, rec := range records {
		cards = append(cards, renderOneCard(rec, width))
	}
	return strings.Join(cards, "\n")
}

const cardBorderPaddingOverhead = 4 // 2 border + 2 padding

func renderOneCard(rec structuredLogRecord, outerWidth int) string {
	innerWidth := outerWidth - cardBorderPaddingOverhead
	if innerWidth < 4 {
		innerWidth = 4
	}

	header := buildCardHeader(rec, innerWidth)

	msgWrapped := xansi.Hardwrap(rec.Message, innerWidth, true)
	styledMsg := cardMessageStyle.Render(msgWrapped)

	body := header + "\n" + styledMsg

	if rec.Kind == "step" && len(rec.Details) > 0 {
		keys := make([]string, 0, len(rec.Details))
		for k := range rec.Details {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		detailLines := make([]string, 0, len(keys))
		for _, k := range keys {
			line := cardDetailKeyStyle.Render(k+": ") + cardDetailValStyle.Render(rec.Details[k])
			detailLines = append(detailLines, line)
		}
		body += "\n" + strings.Join(detailLines, "\n")
	}

	border := cardBorderStyleForKind(rec.Kind)
	return border.Width(innerWidth).Render(body)
}

func buildCardHeader(rec structuredLogRecord, innerWidth int) string {
	var kindBadge, nameStr, statusStr string
	ts := formatCardTimestamp(rec.TS)
	tsRendered := cardTimestampStyle.Render(ts)

	switch rec.Kind {
	case "step":
		kindBadge = cardKindStepBadge.Render("STEP")
		nameStr = cardStepNameStyle.Render(rec.Step)
		statusStr = cardStatusBadge(rec.Status)
	case "output":
		kindBadge = cardKindOutputBadge.Render("OUT")
		nameStr = cardSourceNameStyle.Render(rec.Source)
	default:
		kindBadge = cardKindOutputBadge.Render(strings.ToUpper(rec.Kind))
	}

	leftParts := []string{kindBadge, nameStr}
	if statusStr != "" {
		leftParts = append(leftParts, statusStr)
	}
	left := strings.Join(leftParts, " ")

	leftWidth := lipgloss.Width(left)
	tsWidth := lipgloss.Width(tsRendered)

	// If timestamp doesn't fit, omit it.
	if leftWidth+1+tsWidth > innerWidth {
		return left
	}

	gap := innerWidth - leftWidth - tsWidth
	return left + strings.Repeat(" ", gap) + tsRendered
}

func formatCardTimestamp(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t2, err2 := time.Parse(time.RFC3339, ts)
		if err2 != nil {
			return ts
		}
		return t2.Format("15:04:05")
	}
	return t.Format("15:04:05")
}

// Footer
// ---------------------------------------------------------------------------

func (m *model) renderFooter(width int) string {
	groups := []string{
		footerKeyStyle.Render("tab") + footerStyle.Render(" focus"),
		footerKeyStyle.Render("↑/↓") + footerStyle.Render(" list/scroll"),
		footerKeyStyle.Render("←/→") + footerStyle.Render(" state"),
		footerKeyStyle.Render("g/G") + footerStyle.Render(" top/bottom"),
		footerKeyStyle.Render("q") + footerStyle.Render(" quit"),
	}
	if m.terminal {
		groups = append(groups, warnStyle.Render("(terminal)"))
	}
	sep := footerSepStyle.Render(" │ ")
	controls := strings.Join(groups, sep)
	sepLine := separatorStyle.Render(strings.Repeat("─", width))
	return sepLine + "\n" + controls
}

// ---------------------------------------------------------------------------
// Quit confirmation modal
// ---------------------------------------------------------------------------

func (m *model) renderQuitConfirm(width int) string {
	title := modalTitleStyle.Render("Confirm Quit")
	state := string(m.snapshot.Run.State)
	body := modalTextStyle.Render(fmt.Sprintf(
		"Run is %s. Quitting now aborts this local in-process run.",
		warnStyle.Render(state),
	))
	controls := footerKeyStyle.Render("Enter/Y") + modalTextStyle.Render(" quit  ") +
		footerKeyStyle.Render("Esc/N") + modalTextStyle.Render(" continue")

	inner := lipgloss.JoinVertical(lipgloss.Center, title, "", body, "", controls)
	modalWidth := minInt(60, width-4)
	modal := modalOverlayStyle.Width(modalWidth).Render(inner)
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, modal)
}

// ---------------------------------------------------------------------------
// Text utilities
// ---------------------------------------------------------------------------

func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	remaining := strings.TrimSpace(text)
	if remaining == "" {
		return []string{""}
	}

	lines := make([]string, 0, 4)
	for len([]rune(remaining)) > width {
		runes := []rune(remaining)
		split := width
		for i := width - 1; i >= 0; i-- {
			if unicode.IsSpace(runes[i]) {
				split = i
				break
			}
		}
		if split <= 0 {
			split = width
		}
		lines = append(lines, strings.TrimSpace(string(runes[:split])))
		remaining = strings.TrimLeftFunc(string(runes[split:]), unicode.IsSpace)
	}
	if remaining != "" {
		lines = append(lines, remaining)
	}
	return lines
}

func renderStyledKeyValue(key, value string, width int) string {
	labelPlain := key + ": "
	if width <= 0 {
		width = 1
	}
	if width <= len([]rune(labelPlain)) {
		return stateKeyStyle.Render(truncateText(labelPlain, width))
	}
	return stateKeyStyle.Render(labelPlain) + stateValueStyle.Render(truncateText(value, width-len([]rune(labelPlain))))
}

func wrapBreadcrumbCompact(parts []string, width int) string {
	if len(parts) == 0 {
		return ""
	}
	if width <= 0 {
		return strings.Join(parts, " ")
	}
	sep := breadcrumbArrowStyle.Render(" -> ")
	lines := make([]string, 0, 2)
	currentLine := parts[0]
	for _, part := range parts[1:] {
		candidate := currentLine + sep + part
		if lipgloss.Width(candidate) <= width {
			currentLine = candidate
			continue
		}
		lines = append(lines, currentLine)
		currentLine = part
	}
	lines = append(lines, currentLine)
	return strings.Join(lines, "\n")
}

func truncateLines(text string, height int) string {
	if height <= 0 || text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= height {
		return text
	}
	return strings.Join(lines[:height], "\n")
}

func truncateText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 1 {
		for _, r := range value {
			if lipgloss.Width(string(r)) > width {
				return ""
			}
			return string(r)
		}
		return ""
	}
	targetWidth := width - lipgloss.Width("…")
	if targetWidth < 0 {
		targetWidth = 0
	}

	var out strings.Builder
	currentWidth := 0
	for _, r := range value {
		rw := lipgloss.Width(string(r))
		if currentWidth+rw > targetWidth {
			break
		}
		out.WriteRune(r)
		currentWidth += rw
	}
	return out.String() + "…"
}

func padRight(value string, width int) string {
	if width <= 0 {
		return ""
	}
	visible := lipgloss.Width(value)
	if visible >= width {
		return truncateText(value, width)
	}
	return value + strings.Repeat(" ", width-visible)
}

func wrapTextHard(text string, width int) string {
	if width <= 0 || text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		runes := []rune(line)
		for len(runes) > width {
			wrapped = append(wrapped, string(runes[:width]))
			runes = runes[width:]
		}
		wrapped = append(wrapped, string(runes))
	}
	return strings.Join(wrapped, "\n")
}

// wrapTextHardWithLineNumbers hard-wraps text and prepends line numbers.
// Only the first segment of a wrapped line gets a number; continuation
// lines receive a blank gutter.
func wrapTextHardWithLineNumbers(text string, width int) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	totalLines := len(lines)
	numWidth := len(strconv.Itoa(totalLines))
	// gutter = "  123 │ "  →  2 + numWidth + 3
	gutterTotal := numWidth + 5
	contentWidth := width - gutterTotal
	if contentWidth < 1 {
		contentWidth = 1
	}

	blankGutter := strings.Repeat(" ", gutterTotal)
	wrapped := make([]string, 0, len(lines))
	for i, line := range lines {
		num := fmt.Sprintf("  %*d │ ", numWidth, i+1)
		styledNum := logLineNumStyle.Render(num)
		if line == "" {
			wrapped = append(wrapped, styledNum)
			continue
		}
		segments := strings.Split(xansi.Hardwrap(line, contentWidth, true), "\n")
		for segIdx, segment := range segments {
			if segIdx == 0 {
				wrapped = append(wrapped, styledNum+segment)
				continue
			}
			wrapped = append(wrapped, blankGutter+segment)
		}
	}
	return strings.Join(wrapped, "\n")
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func intPtrToString(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func formatRunElapsed(run store.Run) string {
	start := run.CreatedAt
	if run.StartedAt != nil && !run.StartedAt.IsZero() {
		start = *run.StartedAt
	}
	if start.IsZero() {
		return "-"
	}
	end := time.Now().UTC()
	if run.EndedAt != nil && !run.EndedAt.IsZero() {
		end = *run.EndedAt
	}
	if end.Before(start) {
		return "-"
	}
	elapsed := end.Sub(start)
	return formatDuration(&elapsed)
}

func formatRunCompletionRate(run store.Run) string {
	start := run.CreatedAt
	if run.StartedAt != nil && !run.StartedAt.IsZero() {
		start = *run.StartedAt
	}
	if start.IsZero() {
		return "-"
	}
	end := time.Now().UTC()
	if run.EndedAt != nil && !run.EndedAt.IsZero() {
		end = *run.EndedAt
	}
	if !end.After(start) {
		return "-"
	}
	completed := run.Counts.Succeeded + run.Counts.TestFailed + run.Counts.InfraFailed + run.Counts.Canceled
	if completed == 0 {
		return "0.00 inst/min"
	}
	rate := float64(completed) / end.Sub(start).Minutes()
	return fmt.Sprintf("%.2f inst/min", rate)
}

func formatRunPassFailRate(counts domain.RunCounts) string {
	failed := counts.TestFailed + counts.InfraFailed
	denom := counts.Succeeded + failed
	if denom == 0 {
		return "-"
	}
	passPct := (float64(counts.Succeeded) / float64(denom)) * 100
	failPct := (float64(failed) / float64(denom)) * 100
	return fmt.Sprintf("%.1f%%/%.1f%%", passPct, failPct)
}

func durationBetweenValue(start, end time.Time) *time.Duration {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	d := end.Sub(start)
	return &d
}

func durationBetweenPtr(start time.Time, end *time.Time) *time.Duration {
	if end == nil {
		return nil
	}
	return durationBetweenValue(start, *end)
}

func durationBetweenPtrs(start, end *time.Time) *time.Duration {
	if start == nil || end == nil {
		return nil
	}
	return durationBetweenValue(*start, *end)
}

func formatDuration(value *time.Duration) string {
	if value == nil || *value < 0 {
		return "-"
	}
	d := value.Round(time.Second)
	if d < time.Second {
		return "0s"
	}
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	parts := make([]string, 0, 3)
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	return strings.Join(parts, "")
}

func (m *model) clampInstanceOffset(visibleRows int) {
	if visibleRows <= 0 || len(m.snapshot.Instances) == 0 {
		m.instancesOffset = 0
		return
	}

	maxOffset := maxInt(0, len(m.snapshot.Instances)-visibleRows)
	if m.instancesOffset > maxOffset {
		m.instancesOffset = maxOffset
	}
	if m.instancesOffset < 0 {
		m.instancesOffset = 0
	}
	if m.selectedIdx < m.instancesOffset {
		m.instancesOffset = m.selectedIdx
	}
	if m.selectedIdx >= m.instancesOffset+visibleRows {
		m.instancesOffset = m.selectedIdx - visibleRows + 1
	}
	if m.instancesOffset > maxOffset {
		m.instancesOffset = maxOffset
	}
	if m.instancesOffset < 0 {
		m.instancesOffset = 0
	}
}
