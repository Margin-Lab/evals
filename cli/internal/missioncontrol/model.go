package missioncontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type paneType int

const (
	paneLeft paneType = iota
	paneRight
)

type logStream string

const (
	logStreamTestOutput   logStream = "test_output"
	logStreamDockerBuild  logStream = "docker_build"
	logStreamAgentBoot    logStream = "agent_bootstrap"
	logStreamAgentControl logStream = "agent_control"
	logStreamAgentRuntime logStream = "agent_runtime"
	logStreamAgentPTY     logStream = "agent_pty"
)

type logRenderMode int

const (
	logRenderRaw logRenderMode = iota
	logRenderStructuredKV
	logRenderJSONL
)

type logStreamSpec struct {
	ID     logStream
	Label  string
	Roles  []string
	Render logRenderMode
}

var logStreams = []logStreamSpec{
	{
		ID:     logStreamDockerBuild,
		Label:  "Docker Build",
		Roles:  []string{store.ArtifactRoleDockerBuild},
		Render: logRenderStructuredKV,
	},
	{
		ID:     logStreamAgentBoot,
		Label:  "Bootstrap",
		Roles:  []string{store.ArtifactRoleAgentBoot},
		Render: logRenderStructuredKV,
	},
	{
		ID:     logStreamAgentRuntime,
		Label:  "Runtime",
		Roles:  []string{store.ArtifactRoleAgentRuntime},
		Render: logRenderStructuredKV,
	},
	{
		ID:     logStreamAgentControl,
		Label:  "Control",
		Roles:  []string{store.ArtifactRoleAgentControl},
		Render: logRenderStructuredKV,
	},
	{
		ID:     logStreamAgentPTY,
		Label:  "Agent PTY",
		Roles:  []string{store.ArtifactRoleAgentPTY},
		Render: logRenderJSONL,
	},
	{
		ID:     logStreamTestOutput,
		Label:  "Test Output",
		Render: logRenderRaw,
	},
}

var (
	ansiCSIRegex = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSCRegex = regexp.MustCompile(`\x1b\][^\x07]*(?:\x07|\x1b\\)`)
	ansiESCRegex = regexp.MustCompile(`\x1b[@-_]`)
)

type pollTickMsg struct{}

type snapshotLoadedMsg struct {
	snapshot runnerapi.RunSnapshot
	err      error
}

type ctxDoneMsg struct {
	err error
}

type logLoadedMsg struct {
	key     string
	stream  logStreamSpec
	content ArtifactText
	err     error
	status  string
}

type logArtifactTarget struct {
	Section  string
	Artifact store.Artifact
}

type stateLogSection struct {
	Title        string
	Render       logRenderMode
	Text         string
	Records      []structuredLogRecord
	EmptyMessage string
}

type stateLogRequestSection struct {
	Title       string
	Stream      logStreamSpec
	Targets     []logArtifactTarget
	Key         string
	Placeholder string
}

type stateLogsLoadedMsg struct {
	key      string
	sections []loadedStateLogSection
}

type loadedStateLogSection struct {
	Title       string
	Stream      logStreamSpec
	Content     ArtifactText
	Err         error
	Status      string
	Placeholder string
}

type model struct {
	ctx              context.Context
	source           Source
	runID            string
	pollInterval     time.Duration
	textPreviewLimit int64

	snapshot        runnerapi.RunSnapshot
	snapshotLoaded  bool
	terminal        bool
	selectedIdx     int
	selectedState   simplifiedState
	autoStateSelect bool
	logKey          string
	logText         string
	logRecords      []structuredLogRecord
	logActiveRender logRenderMode
	logSections     []stateLogSection
	logWrappedText  string
	logWrappedWidth int
	logStatus       string
	logLoading      bool
	logTruncated    bool
	logFollowTail   bool

	focusedPane paneType
	aborted     bool
	confirmQuit bool
	errMsg      string
	width       int
	height      int

	instancesOffset int
	logViewport     viewport.Model
	loadingSpinner  spinner.Model
}

func newModel(ctx context.Context, cfg Config) *model {
	logVP := viewport.New(0, 0)
	logVP.SetContent("")
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)
	return &model{
		ctx:              ctx,
		source:           cfg.Source,
		runID:            cfg.RunID,
		pollInterval:     cfg.PollInterval,
		textPreviewLimit: cfg.TextPreviewLimit,
		selectedState:    simplifiedStatePending,
		autoStateSelect:  true,
		logFollowTail:    true,
		logViewport:      logVP,
		loadingSpinner:   sp,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.fetchSnapshotCmd(), waitForCtxDoneCmd(m.ctx), m.loadingSpinner.Tick)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !m.snapshotLoaded {
			var cmd tea.Cmd
			m.loadingSpinner, cmd = m.loadingSpinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case pollTickMsg:
		if m.terminal {
			return m, nil
		}
		return m, m.fetchSnapshotCmd()
	case ctxDoneMsg:
		if msg.err == nil || m.terminal {
			return m, nil
		}
		m.errMsg = fmt.Sprintf("run session ended: %v", msg.err)
		m.aborted = true
		return m, tea.Quit
	case snapshotLoadedMsg:
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("snapshot refresh failed: %v", msg.err)
			if m.terminal {
				return m, nil
			}
			return m, tickCmd(m.pollInterval)
		}
		m.errMsg = ""
		m.applySnapshot(msg.snapshot)
		cmds := make([]tea.Cmd, 0, 2)
		if cmd := m.maybeLoadSelectedLog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if !m.terminal {
			cmds = append(cmds, tickCmd(m.pollInterval))
		}
		return m, tea.Batch(cmds...)
	case logLoadedMsg:
		if msg.key != m.logKey {
			return m, nil
		}
		m.logLoading = false
		if msg.err != nil {
			m.logStatus = fmt.Sprintf("failed to load %s: %v", msg.stream.Label, msg.err)
			m.setLogContent("")
			return m, nil
		}
		m.logTruncated = msg.content.Truncated
		statusParts := make([]string, 0, 2)
		if msg.content.Truncated {
			statusParts = append(statusParts, fmt.Sprintf("showing first %d bytes", m.textPreviewLimit))
		}
		if msg.status != "" {
			statusParts = append(statusParts, msg.status)
		}
		m.logStatus = strings.Join(statusParts, "; ")
		parsed, parseErr := parseLogContent(msg.stream.Render, msg.content)
		if parseErr != nil {
			m.logStatus = fmt.Sprintf("failed to parse %s: %v", msg.stream.Label, parseErr)
			m.setTextLogContent("", msg.stream.Render)
			return m, nil
		}
		if msg.stream.Render == logRenderStructuredKV {
			m.setStructuredLogContent(parsed.Records)
		} else {
			m.setTextLogContent(parsed.Text, msg.stream.Render)
		}
		return m, nil
	case stateLogsLoadedMsg:
		if msg.key != m.logKey {
			return m, nil
		}
		m.logLoading = false
		statusParts := make([]string, 0, len(msg.sections))
		sections := make([]stateLogSection, 0, len(msg.sections))
		m.logTruncated = false
		for _, section := range msg.sections {
			emptyMessage := section.Placeholder
			if emptyMessage == "" {
				emptyMessage = "No log content"
			}
			if section.Err != nil {
				statusParts = append(statusParts, fmt.Sprintf("failed to load %s: %v", section.Title, section.Err))
				sections = append(sections, stateLogSection{
					Title:        section.Title,
					Render:       section.Stream.Render,
					EmptyMessage: emptyMessage,
				})
				continue
			}
			if section.Status != "" {
				statusParts = append(statusParts, section.Status)
			}
			if section.Content.Truncated {
				m.logTruncated = true
			}
			parsed, parseErr := parseLogContent(section.Stream.Render, section.Content)
			if parseErr != nil {
				statusParts = append(statusParts, fmt.Sprintf("failed to parse %s: %v", section.Title, parseErr))
				sections = append(sections, stateLogSection{
					Title:        section.Title,
					Render:       section.Stream.Render,
					EmptyMessage: emptyMessage,
				})
				continue
			}
			sections = append(sections, stateLogSection{
				Title:        section.Title,
				Render:       section.Stream.Render,
				Text:         parsed.Text,
				Records:      parsed.Records,
				EmptyMessage: emptyMessage,
			})
		}
		m.logStatus = strings.Join(statusParts, "; ")
		m.setStateLogSections(sections)
		return m, nil
	case tea.KeyMsg:
		if m.confirmQuit {
			switch strings.ToLower(msg.String()) {
			case "enter", "y":
				m.aborted = !m.terminal
				m.confirmQuit = false
				return m, tea.Quit
			case "esc", "n":
				m.confirmQuit = false
				return m, nil
			default:
				return m, nil
			}
		}

		viewportMsg, cmd, handled := m.handleKeyMsg(msg)
		if handled {
			return m, cmd
		}
		return m, m.updateLogViewport(viewportMsg)
	}

	return m, m.updateLogViewport(msg)
}

func (m *model) applySnapshot(snapshot runnerapi.RunSnapshot) {
	prevSelectedID := m.selectedInstanceID()
	m.snapshot = snapshot
	m.snapshotLoaded = true
	m.terminal = snapshot.Run.State.IsTerminal()
	if strings.TrimSpace(m.snapshot.Run.RunID) == "" {
		m.snapshot.Run.RunID = m.runID
	}
	m.restoreSelection(prevSelectedID)
	if m.selectedInstanceID() != prevSelectedID {
		m.autoStateSelect = true
	}
	if m.autoStateSelect {
		m.syncSelectedStateToCurrent()
	}
}

func (m *model) restoreSelection(prevSelectedID string) {
	if len(m.snapshot.Instances) == 0 {
		m.selectedIdx = 0
		return
	}
	if prevSelectedID != "" {
		for i := range m.snapshot.Instances {
			if m.snapshot.Instances[i].Instance.InstanceID == prevSelectedID {
				m.selectedIdx = i
				return
			}
		}
	}
	if m.selectedIdx >= len(m.snapshot.Instances) {
		m.selectedIdx = len(m.snapshot.Instances) - 1
	}
	if m.selectedIdx < 0 {
		m.selectedIdx = 0
	}
}

func (m *model) selectedInstanceID() string {
	inst := m.selectedInstance()
	if inst == nil {
		return ""
	}
	return inst.Instance.InstanceID
}

func (m *model) selectedInstance() *runnerapi.InstanceSnapshot {
	if len(m.snapshot.Instances) == 0 {
		return nil
	}
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.snapshot.Instances) {
		m.selectedIdx = 0
	}
	return &m.snapshot.Instances[m.selectedIdx]
}

func (m *model) selectedInstanceState() domain.InstanceState {
	inst := m.selectedInstance()
	if inst == nil {
		return ""
	}
	return inst.Instance.State
}

func (m *model) currentSimplifiedState() simplifiedState {
	return simplifiedStateForInstanceState(m.selectedInstanceState())
}

func (m *model) visibleSimplifiedStateSpecs() []simplifiedStateSpec {
	return visibleSimplifiedStates(m.currentSimplifiedState())
}

func (m *model) selectedSimplifiedStateSpec() simplifiedStateSpec {
	return simplifiedStateSpecByID(m.selectedState)
}

func (m *model) syncSelectedStateToCurrent() {
	m.selectedState = m.currentSimplifiedState()
}

func (m *model) ensureSelectedStateVisible() {
	for _, spec := range m.visibleSimplifiedStateSpecs() {
		if spec.ID == m.selectedState {
			return
		}
	}
	m.syncSelectedStateToCurrent()
}

func (m *model) maybeLoadSelectedLog() tea.Cmd {
	inst := m.selectedInstance()
	if inst == nil {
		m.logKey = ""
		m.logLoading = false
		m.logTruncated = false
		m.logStatus = ""
		m.logFollowTail = true
		m.setLogContent("no instances yet")
		return nil
	}
	if m.autoStateSelect {
		m.syncSelectedStateToCurrent()
	}
	m.ensureSelectedStateVisible()

	spec := m.selectedSimplifiedStateSpec()
	if len(spec.LogStreams) == 0 {
		m.logKey = logLoadKey(inst.Instance.InstanceID, string(spec.ID))
		m.logLoading = false
		m.logTruncated = false
		m.logStatus = ""
		m.logFollowTail = true
		m.setLogContent(spec.Placeholder)
		return nil
	}

	requests, key := resolveSelectedStateLogRequests(inst, spec)
	loadableCount := 0
	for _, request := range requests {
		if len(request.Targets) > 0 {
			loadableCount++
		}
	}
	if loadableCount == 0 {
		m.logKey = key
		m.logLoading = false
		m.logTruncated = false
		m.logStatus = ""
		m.logFollowTail = true
		m.setStateLogSections(buildPlaceholderStateLogSections(requests))
		return nil
	}
	if len(requests) == 1 {
		request := requests[0]
		if len(request.Targets) == 0 {
			m.logKey = key
			m.logLoading = false
			m.logTruncated = false
			m.logStatus = ""
			m.logFollowTail = true
			m.setLogContent(request.Placeholder)
			return nil
		}
		if key == m.logKey && !m.logLoading {
			if !m.terminal {
				return loadLogCmd(m.ctx, m.source, key, request.Stream, request.Targets, m.textPreviewLimit)
			}
			return nil
		}
		m.logKey = key
		m.logLoading = true
		m.logTruncated = false
		m.logStatus = "loading..."
		m.logFollowTail = true
		m.setLogContent("")
		return loadLogCmd(m.ctx, m.source, key, request.Stream, request.Targets, m.textPreviewLimit)
	}

	if key == m.logKey && !m.logLoading {
		if !m.terminal {
			return loadStateLogsCmd(m.ctx, m.source, key, requests, m.textPreviewLimit)
		}
		return nil
	}
	m.logKey = key
	m.logLoading = true
	m.logTruncated = false
	m.logStatus = "loading..."
	m.logFollowTail = true
	m.setLogContent("")
	return loadStateLogsCmd(m.ctx, m.source, key, requests, m.textPreviewLimit)
}

func resolveSelectedStateLogRequests(inst *runnerapi.InstanceSnapshot, spec simplifiedStateSpec) ([]stateLogRequestSection, string) {
	if inst == nil {
		return nil, ""
	}
	requests := make([]stateLogRequestSection, 0, len(spec.LogStreams))
	keyParts := []string{inst.Instance.InstanceID, string(spec.ID)}
	for _, streamID := range spec.LogStreams {
		stream := logStreamSpecByID(streamID)
		targets, targetKey, placeholder := resolveLogTargets(inst, stream)
		requests = append(requests, stateLogRequestSection{
			Title:       stream.Label,
			Stream:      stream,
			Targets:     targets,
			Key:         targetKey,
			Placeholder: placeholder,
		})
		keyParts = append(keyParts, targetKey)
	}
	return requests, logLoadKey(keyParts...)
}

func buildPlaceholderStateLogSections(requests []stateLogRequestSection) []stateLogSection {
	sections := make([]stateLogSection, 0, len(requests))
	for _, request := range requests {
		emptyMessage := request.Placeholder
		if emptyMessage == "" {
			emptyMessage = "No log content"
		}
		sections = append(sections, stateLogSection{
			Title:        request.Title,
			Render:       request.Stream.Render,
			EmptyMessage: emptyMessage,
		})
	}
	return sections
}

func loadStateLogsCmd(ctx context.Context, src Source, key string, requests []stateLogRequestSection, maxBytes int64) tea.Cmd {
	return func() tea.Msg {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		sections := make([]loadedStateLogSection, 0, len(requests))
		for _, request := range requests {
			loaded := loadedStateLogSection{
				Title:       request.Title,
				Stream:      request.Stream,
				Placeholder: request.Placeholder,
			}
			if len(request.Targets) == 0 {
				sections = append(sections, loaded)
				continue
			}
			if request.Stream.ID == logStreamTestOutput {
				content, status, err := loadCombinedLogContent(readCtx, src, request.Targets, maxBytes)
				loaded.Content = content
				loaded.Status = status
				loaded.Err = err
				sections = append(sections, loaded)
				continue
			}
			content, err := src.ReadArtifactText(readCtx, request.Targets[0].Artifact, maxBytes)
			loaded.Content = content
			loaded.Err = err
			sections = append(sections, loaded)
		}
		return stateLogsLoadedMsg{key: key, sections: sections}
	}
}

func logStreamSpecByID(id logStream) logStreamSpec {
	for _, spec := range logStreams {
		if spec.ID == id {
			return spec
		}
	}
	return logStreamSpec{ID: id, Label: "Logs", Render: logRenderRaw}
}

func resolveLogTargets(inst *runnerapi.InstanceSnapshot, stream logStreamSpec) ([]logArtifactTarget, string, string) {
	if inst == nil {
		return nil, "", ""
	}
	if stream.ID == logStreamTestOutput {
		return resolveTestOutputLogArtifacts(inst, stream)
	}
	placeholder := strings.ToLower(stream.Label) + " is not available yet"
	artifact, artifactKey := findLogArtifact(inst, stream.Roles, "")
	if artifact != nil {
		key := logLoadKey(inst.Instance.InstanceID, string(stream.ID), artifactKey)
		return []logArtifactTarget{{Artifact: *artifact}}, key, ""
	}

	state := inst.Instance.State
	if state.IsTerminal() {
		placeholder = strings.ToLower(stream.Label) + " was not captured"
	}
	return nil, logLoadKey(inst.Instance.InstanceID, string(stream.ID), artifactKey), placeholder
}

func resolveTestOutputLogArtifacts(inst *runnerapi.InstanceSnapshot, stream logStreamSpec) ([]logArtifactTarget, string, string) {
	stdoutRef := ""
	stderrRef := ""
	if inst.Result != nil {
		stdoutRef = strings.TrimSpace(inst.Result.TestStdoutRef)
		stderrRef = strings.TrimSpace(inst.Result.TestStderrRef)
	}
	stdoutArtifact, stdoutKey := findLogArtifact(inst, []string{store.ArtifactRoleTestStdout}, stdoutRef)
	stderrArtifact, stderrKey := findLogArtifact(inst, []string{store.ArtifactRoleTestStderr}, stderrRef)

	targets := make([]logArtifactTarget, 0, 2)
	if stdoutArtifact != nil {
		targets = append(targets, logArtifactTarget{
			Section:  "stdout",
			Artifact: *stdoutArtifact,
		})
	}
	if stderrArtifact != nil {
		targets = append(targets, logArtifactTarget{
			Section:  "stderr",
			Artifact: *stderrArtifact,
		})
	}

	key := logLoadKey(inst.Instance.InstanceID, string(stream.ID), stdoutKey, stderrKey)
	if len(targets) > 0 {
		return targets, key, ""
	}

	placeholder := strings.ToLower(stream.Label) + " is not available yet"
	if inst.Instance.State.IsTerminal() {
		placeholder = strings.ToLower(stream.Label) + " was not captured"
	}
	return nil, key, placeholder
}

func findLogArtifact(inst *runnerapi.InstanceSnapshot, roles []string, ref string) (*store.Artifact, string) {
	if inst == nil {
		return nil, logLoadKey("", ref, "")
	}
	for _, role := range roles {
		for i := range inst.Artifacts {
			item := inst.Artifacts[i]
			if strings.EqualFold(item.Role, role) {
				return &item, logLoadKey(item.ArtifactID, item.StoreKey, item.URI)
			}
		}
	}
	if ref != "" {
		for i := range inst.Artifacts {
			item := inst.Artifacts[i]
			if item.StoreKey == ref || item.ArtifactID == ref {
				return &item, logLoadKey(item.ArtifactID, item.StoreKey, item.URI)
			}
		}
	}
	return nil, logLoadKey("", ref, "")
}

func logLoadKey(parts ...string) string {
	return strings.Join(parts, "|")
}

func loadLogCmd(ctx context.Context, src Source, key string, stream logStreamSpec, targets []logArtifactTarget, maxBytes int64) tea.Cmd {
	return func() tea.Msg {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if stream.ID == logStreamTestOutput {
			content, status, err := loadCombinedLogContent(readCtx, src, targets, maxBytes)
			return logLoadedMsg{key: key, stream: stream, content: content, err: err, status: status}
		}
		if len(targets) == 0 {
			return logLoadedMsg{key: key, stream: stream, err: fmt.Errorf("no log artifacts resolved")}
		}
		content, err := src.ReadArtifactText(readCtx, targets[0].Artifact, maxBytes)
		return logLoadedMsg{key: key, stream: stream, content: content, err: err}
	}
}

func loadCombinedLogContent(ctx context.Context, src Source, targets []logArtifactTarget, maxBytes int64) (ArtifactText, string, error) {
	if len(targets) == 0 {
		return ArtifactText{}, "", fmt.Errorf("no test output artifacts resolved")
	}

	sections := make([]string, 0, len(targets))
	errors := make([]string, 0, len(targets))
	anyTruncated := false
	anyLoaded := false

	for _, target := range targets {
		content, err := src.ReadArtifactText(ctx, target.Artifact, maxBytes)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s failed to load: %v", target.Section, err))
			continue
		}
		if strings.TrimSpace(content.Text) == "" && !content.Truncated {
			continue
		}
		header := "=== " + target.Section
		if content.Truncated {
			header += " (truncated)"
			anyTruncated = true
		}
		header += " ==="
		body := normalizeLogText(content.Text)
		sections = append(sections, header+"\n"+body)
		anyLoaded = true
	}

	status := strings.Join(errors, "; ")
	if !anyLoaded {
		if status != "" {
			return ArtifactText{}, "", fmt.Errorf(status)
		}
		return ArtifactText{}, "", nil
	}

	var combined strings.Builder
	for idx, section := range sections {
		if idx > 0 {
			if !strings.HasSuffix(combined.String(), "\n") {
				combined.WriteString("\n")
			}
			combined.WriteString("\n")
		}
		combined.WriteString(section)
	}
	return ArtifactText{
		Text:      combined.String(),
		Truncated: anyTruncated,
	}, status, nil
}

func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return pollTickMsg{}
	})
}

func waitForCtxDoneCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		<-ctx.Done()
		return ctxDoneMsg{err: ctx.Err()}
	}
}

func (m *model) fetchSnapshotCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		snapshot, err := m.source.GetRunSnapshot(ctx, m.runID)
		return snapshotLoadedMsg{snapshot: snapshot, err: err}
	}
}

func (m *model) handleKeyMsg(msg tea.KeyMsg) (tea.KeyMsg, tea.Cmd, bool) {
	switch msg.String() {
	case "g":
		m.logViewport.GotoTop()
		m.logFollowTail = false
		return tea.KeyMsg{}, nil, true
	case "G":
		m.logViewport.GotoBottom()
		m.logFollowTail = true
		return tea.KeyMsg{}, nil, true
	}

	switch strings.ToLower(msg.String()) {
	case "q", "ctrl+c":
		if m.terminal {
			return tea.KeyMsg{}, tea.Quit, true
		}
		m.confirmQuit = true
		return tea.KeyMsg{}, nil, true
	case "tab":
		m.toggleFocusedPane()
		return tea.KeyMsg{}, nil, true
	case "up":
		if m.focusedPane == paneLeft {
			return tea.KeyMsg{}, m.moveSelectedInstance(-1), true
		}
	case "down":
		if m.focusedPane == paneLeft {
			return tea.KeyMsg{}, m.moveSelectedInstance(1), true
		}
	case "left":
		if m.focusedPane == paneRight {
			return tea.KeyMsg{}, m.cycleSelectedState(-1), true
		}
		return tea.KeyMsg{}, nil, true
	case "right":
		if m.focusedPane == paneRight {
			return tea.KeyMsg{}, m.cycleSelectedState(1), true
		}
		return tea.KeyMsg{}, nil, true
	case "pgup", "pgdown", "home", "end":
		return tea.KeyMsg{}, nil, true
	}

	return msg, nil, false
}

func (m *model) toggleFocusedPane() {
	if m.focusedPane == paneLeft {
		m.focusedPane = paneRight
		return
	}
	m.focusedPane = paneLeft
}

func (m *model) moveSelectedInstance(delta int) tea.Cmd {
	if len(m.snapshot.Instances) == 0 || delta == 0 {
		return nil
	}

	m.selectedIdx = maxInt(0, minInt(m.selectedIdx, len(m.snapshot.Instances)-1))
	nextIdx := maxInt(0, minInt(m.selectedIdx+delta, len(m.snapshot.Instances)-1))
	if nextIdx == m.selectedIdx {
		return nil
	}

	m.selectedIdx = nextIdx
	m.autoStateSelect = true
	return m.maybeLoadSelectedLog()
}

func (m *model) setLogContent(value string) {
	m.setTextLogContent(value, logRenderRaw)
}

func (m *model) setTextLogContent(value string, render logRenderMode) {
	m.logSections = nil
	m.logText = value
	m.logRecords = nil
	m.logActiveRender = render
	m.invalidateLogViewportContent()
}

func (m *model) invalidateLogViewportContent() {
	m.logWrappedText = ""
	m.logWrappedWidth = 0
	m.refreshLogViewportContent(maxInt(1, m.logViewport.Width))
}

func (m *model) setStructuredLogContent(records []structuredLogRecord) {
	m.logSections = nil
	m.logRecords = records
	m.logText = ""
	m.logActiveRender = logRenderStructuredKV
	m.invalidateLogViewportContent()
}

func (m *model) setStateLogSections(sections []stateLogSection) {
	m.logSections = sections
	m.logText = ""
	m.logRecords = nil
	m.logActiveRender = logRenderRaw
	m.invalidateLogViewportContent()
}

func (m *model) refreshLogViewportContent(width int) {
	if width <= 0 {
		width = 1
	}
	if len(m.logSections) > 0 {
		if m.logWrappedWidth == width && m.logWrappedText != "" {
			return
		}
		rendered := renderStateLogSections(m.logSections, width)
		m.logWrappedText = rendered
		m.logWrappedWidth = width
		m.logViewport.SetContent(rendered)
		if m.logFollowTail {
			m.logViewport.GotoBottom()
		}
		return
	}
	if m.logActiveRender == logRenderStructuredKV {
		if len(m.logRecords) == 0 {
			m.logWrappedText = ""
			m.logWrappedWidth = width
			m.logViewport.SetContent(mutedStyle.Render("No log content"))
			return
		}
		if m.logWrappedWidth == width && m.logWrappedText != "" {
			return
		}
		rendered := renderStructuredLogCards(m.logRecords, width)
		m.logWrappedText = rendered
		m.logWrappedWidth = width
		m.logViewport.SetContent(rendered)
		if m.logFollowTail {
			m.logViewport.GotoBottom()
		}
		return
	}
	if m.logText == "" {
		m.logWrappedText = ""
		m.logWrappedWidth = width
		m.logViewport.SetContent(mutedStyle.Render("No log content"))
		return
	}
	if m.logWrappedWidth == width && m.logWrappedText != "" {
		return
	}
	wrapped := wrapTextHardWithLineNumbers(m.logText, width)
	m.logWrappedText = wrapped
	m.logWrappedWidth = width
	m.logViewport.SetContent(wrapped)
	if m.logFollowTail {
		m.logViewport.GotoBottom()
	}
}

func stripANSISequences(input string) string {
	if strings.IndexByte(input, 0x1b) < 0 {
		return input
	}
	out := ansiOSCRegex.ReplaceAllString(input, "")
	out = ansiCSIRegex.ReplaceAllString(out, "")
	out = ansiESCRegex.ReplaceAllString(out, "")
	return out
}

const structuredLogVersion = 1

type structuredLogRecord struct {
	V       int               `json:"v"`
	Kind    string            `json:"kind"`
	TS      string            `json:"ts"`
	Message string            `json:"message"`
	Step    string            `json:"step,omitempty"`
	Status  string            `json:"status,omitempty"`
	Source  string            `json:"source,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

func normalizeLogText(input string) string {
	cleaned := input
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	return cleaned
}

func completeLogLines(input string, truncated bool) []string {
	cleaned := normalizeLogText(input)
	if cleaned == "" {
		return nil
	}
	if truncated && !strings.HasSuffix(cleaned, "\n") {
		lastNewline := strings.LastIndex(cleaned, "\n")
		if lastNewline < 0 {
			return nil
		}
		cleaned = cleaned[:lastNewline+1]
	}
	return strings.Split(cleaned, "\n")
}

type parsedLogContent struct {
	Text    string
	Records []structuredLogRecord
}

func parseLogContent(render logRenderMode, content ArtifactText) (parsedLogContent, error) {
	switch render {
	case logRenderRaw:
		return parsedLogContent{Text: normalizeLogText(content.Text)}, nil
	case logRenderStructuredKV:
		records, err := parseStructuredLogRecords(content.Text, content.Truncated)
		if err != nil {
			return parsedLogContent{}, err
		}
		return parsedLogContent{Records: records}, nil
	case logRenderJSONL:
		text, err := formatJSONLText(content.Text, content.Truncated)
		if err != nil {
			return parsedLogContent{}, err
		}
		return parsedLogContent{Text: text}, nil
	default:
		return parsedLogContent{}, fmt.Errorf("unsupported log render mode %d", render)
	}
}

func sanitizeStructuredInlineText(input string) string {
	cleaned := stripANSISequences(normalizeLogText(input))
	cleaned = strings.ReplaceAll(cleaned, "\n", " ")
	cleaned = strings.ReplaceAll(cleaned, "\t", " ")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, cleaned)
}

func sanitizeStructuredBodyText(input string) string {
	cleaned := stripANSISequences(normalizeLogText(input))
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, cleaned)
}

func sanitizeStructuredLogRecord(record structuredLogRecord) structuredLogRecord {
	record.Step = sanitizeStructuredInlineText(record.Step)
	record.Status = sanitizeStructuredInlineText(record.Status)
	record.Source = sanitizeStructuredInlineText(record.Source)
	record.Message = sanitizeStructuredBodyText(record.Message)

	if len(record.Details) == 0 {
		return record
	}

	sanitizedDetails := make(map[string]string, len(record.Details))
	for key, value := range record.Details {
		sanitizedDetails[sanitizeStructuredInlineText(key)] = sanitizeStructuredInlineText(value)
	}
	record.Details = sanitizedDetails
	return record
}

func parseStructuredLogRecords(input string, truncated bool) ([]structuredLogRecord, error) {
	lines := completeLogLines(input, truncated)
	records := make([]structuredLogRecord, 0, len(lines))
	for idx, line := range lines {
		if line == "" {
			continue
		}
		var record structuredLogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("structured log parse error at line %d: %w", idx+1, err)
		}
		if record.V != structuredLogVersion {
			return nil, fmt.Errorf("structured log parse error at line %d: unsupported version %d", idx+1, record.V)
		}
		record = sanitizeStructuredLogRecord(record)
		records = append(records, record)
	}
	return records, nil
}

func (m *model) cycleSelectedState(delta int) tea.Cmd {
	visible := m.visibleSimplifiedStateSpecs()
	if len(visible) == 0 || delta == 0 {
		return nil
	}

	idx := 0
	for i, spec := range visible {
		if spec.ID == m.selectedState {
			idx = i
			break
		}
	}

	idx = (idx + delta) % len(visible)
	if idx < 0 {
		idx += len(visible)
	}

	m.autoStateSelect = false
	m.selectedState = visible[idx].ID
	return m.maybeLoadSelectedLog()
}

func (m *model) updateLogViewport(msg tea.Msg) tea.Cmd {
	prevYOffset := m.logViewport.YOffset
	var cmd tea.Cmd
	m.logViewport, cmd = m.logViewport.Update(msg)
	if m.logViewport.YOffset != prevYOffset {
		m.logFollowTail = m.logViewport.AtBottom()
	}
	return cmd
}

func (m *model) outcome() Outcome {
	finalRun := m.snapshot.Run
	if strings.TrimSpace(finalRun.RunID) == "" {
		finalRun.RunID = m.runID
	}
	return Outcome{
		FinalRun: finalRun,
		Aborted:  m.aborted,
	}
}
