package missioncontrol

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
)

// ---------------------------------------------------------------------------
// Adaptive color palette (dark-theme-first, indigo + amber)
// ---------------------------------------------------------------------------

var (
	colorPrimary     = lipgloss.AdaptiveColor{Light: "63", Dark: "69"}
	colorAccent      = lipgloss.AdaptiveColor{Light: "208", Dark: "214"}
	colorSuccess     = lipgloss.AdaptiveColor{Light: "34", Dark: "42"}
	colorError       = lipgloss.AdaptiveColor{Light: "196", Dark: "203"}
	colorWarning     = lipgloss.AdaptiveColor{Light: "208", Dark: "214"}
	colorMuted       = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}
	colorBright      = lipgloss.AdaptiveColor{Light: "232", Dark: "255"}
	colorDim         = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
	colorBorder      = lipgloss.AdaptiveColor{Light: "245", Dark: "238"}
	colorBorderFocus = lipgloss.AdaptiveColor{Light: "63", Dark: "69"}
	colorSelectedBg  = lipgloss.AdaptiveColor{Light: "62", Dark: "236"}
	colorSelectedFg  = lipgloss.AdaptiveColor{Light: "255", Dark: "255"}
	colorModalBorder = lipgloss.AdaptiveColor{Light: "208", Dark: "214"}
)

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

var (
	paneBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)
	paneBorderFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorderFocus).
				Padding(0, 1)
	separatorStyle = lipgloss.NewStyle().Foreground(colorBorder)
)

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

var (
	headerTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorBright)
	headerRunIDStyle = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	countLabelStyle  = lipgloss.NewStyle().Foreground(colorMuted)

	badgeBaseStyle = lipgloss.NewStyle().Padding(0, 1).Bold(true)
	badgeRunning   = badgeBaseStyle.Background(colorPrimary).Foreground(lipgloss.Color("255"))
	badgeCompleted = badgeBaseStyle.Background(colorSuccess).Foreground(lipgloss.Color("255"))
	badgeFailed    = badgeBaseStyle.Background(colorError).Foreground(lipgloss.Color("255"))
	badgeCanceled  = badgeBaseStyle.Background(colorWarning).Foreground(lipgloss.Color("232"))
	badgeDefault   = badgeBaseStyle.Background(colorMuted).Foreground(lipgloss.Color("255"))
)

// ---------------------------------------------------------------------------
// Identity bar & error banner
// ---------------------------------------------------------------------------

var (
	errorBannerKeyStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorError)
	errorBannerValueStyle = lipgloss.NewStyle().Foreground(colorError)
)

// ---------------------------------------------------------------------------
// Instance list
// ---------------------------------------------------------------------------

var (
	instanceHeaderStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorBright)
	instanceSelectedStyle = lipgloss.NewStyle().
				Background(colorSelectedBg).
				Foreground(colorSelectedFg).
				Bold(true)
	retryBadgeMutedStyle = lipgloss.NewStyle().Foreground(colorMuted)
	retryBadgeUsedStyle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	retryBadgeSpentStyle = lipgloss.NewStyle().Foreground(colorError).Bold(true)
)

// ---------------------------------------------------------------------------
// State tab
// ---------------------------------------------------------------------------

var (
	stateSectionStyle = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	stateKeyStyle     = lipgloss.NewStyle().Foreground(colorMuted)
	stateValueStyle   = lipgloss.NewStyle().Foreground(colorBright)
	stateErrorValue   = lipgloss.NewStyle().Foreground(colorError).Bold(true)
)

// ---------------------------------------------------------------------------
// State breadcrumb
// ---------------------------------------------------------------------------

var (
	breadcrumbBoxSelectedStyle = lipgloss.NewStyle().
					Border(lipgloss.ThickBorder()).
					BorderForeground(colorBright).
					Foreground(colorBright).
					Bold(true).
					Padding(0, 1)
	breadcrumbBoxCurrentStyle = lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					BorderForeground(colorPrimary).
					Foreground(colorPrimary).
					Bold(true).
					Padding(0, 1)
	breadcrumbBoxCompletedStyle = lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					BorderForeground(colorSuccess).
					Foreground(colorSuccess).
					Padding(0, 1)
	breadcrumbBoxFutureStyle = lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					BorderForeground(colorDim).
					Foreground(colorMuted).
					Padding(0, 1)
	breadcrumbArrowStyle = lipgloss.NewStyle().Foreground(colorDim)
)

var (
	logStatusStyle  = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	logLineNumStyle = lipgloss.NewStyle().Foreground(colorDim)
)

// ---------------------------------------------------------------------------
// Structured log cards
// ---------------------------------------------------------------------------

var (
	cardBorderStepStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPrimary).
				Padding(0, 1)
	cardBorderOutputStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorMuted).
				Padding(0, 1)

	cardKindStepBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("255")).
				Background(colorPrimary).
				Padding(0, 1)
	cardKindOutputBadge = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("255")).
				Background(colorDim).
				Padding(0, 1)

	cardStepNameStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorBright)
	cardSourceNameStyle = lipgloss.NewStyle().Foreground(colorAccent)
	cardTimestampStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	cardMessageStyle    = lipgloss.NewStyle().Foreground(colorBright)
	cardDetailKeyStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	cardDetailValStyle  = lipgloss.NewStyle().Foreground(colorBright)
)

func cardStatusBadge(status string) string {
	switch status {
	case "completed":
		return lipgloss.NewStyle().Foreground(colorSuccess).Bold(true).Render(status)
	case "info":
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(status)
	case "error":
		return lipgloss.NewStyle().Foreground(colorError).Bold(true).Render(status)
	case "fatal":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(colorError).Bold(true).Padding(0, 1).Render(status)
	case "running":
		return lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render(status)
	case "failed":
		return lipgloss.NewStyle().Foreground(colorError).Bold(true).Render(status)
	case "skipped":
		return lipgloss.NewStyle().Foreground(colorWarning).Render(status)
	default:
		return lipgloss.NewStyle().Foreground(colorMuted).Render(status)
	}
}

func cardBorderStyleForKind(kind string) lipgloss.Style {
	if kind == "step" {
		return cardBorderStepStyle
	}
	return cardBorderOutputStyle
}

// Footer
// ---------------------------------------------------------------------------

var (
	footerStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	footerKeyStyle = lipgloss.NewStyle().Foreground(colorBright).Bold(true)
	footerSepStyle = lipgloss.NewStyle().Foreground(colorDim)
)

// ---------------------------------------------------------------------------
// Modal
// ---------------------------------------------------------------------------

var (
	modalOverlayStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorModalBorder).
				Padding(1, 3).
				Align(lipgloss.Center)
	modalTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWarning)
	modalTextStyle  = lipgloss.NewStyle().Foreground(colorBright)
)

// ---------------------------------------------------------------------------
// Shared semantic styles (used in both view and theme helpers)
// ---------------------------------------------------------------------------

var (
	primaryStyle = lipgloss.NewStyle().Foreground(colorPrimary)
	errorStyle   = lipgloss.NewStyle().Foreground(colorError)
	mutedStyle   = lipgloss.NewStyle().Foreground(colorMuted)
	okStyle      = lipgloss.NewStyle().Foreground(colorSuccess)
	warnStyle    = lipgloss.NewStyle().Foreground(colorWarning)
	badStyle     = lipgloss.NewStyle().Foreground(colorError)
)

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func instanceStateIcon(state domain.InstanceState) string {
	switch simplifiedStateForInstanceState(state) {
	case simplifiedStatePending:
		return mutedStyle.Render("○")
	case simplifiedStateBuildingImage, simplifiedStateProvisioningAgent:
		return warnStyle.Render("◐")
	case simplifiedStateRunningAgent, simplifiedStateTestingAgent:
		return lipgloss.NewStyle().Foreground(colorPrimary).Render("●")
	case simplifiedStateSucceeded:
		return okStyle.Render("✓")
	case simplifiedStateTestFailed, simplifiedStateInfraFailed:
		return badStyle.Render("✗")
	case simplifiedStateCanceled:
		return warnStyle.Render("⊘")
	default:
		return mutedStyle.Render("?")
	}
}

func simplifiedStateStyle(state simplifiedState) lipgloss.Style {
	switch state {
	case simplifiedStateSucceeded:
		return okStyle
	case simplifiedStateTestFailed, simplifiedStateInfraFailed:
		return badStyle
	case simplifiedStateCanceled:
		return warnStyle
	case simplifiedStateBuildingImage, simplifiedStateProvisioningAgent:
		return lipgloss.NewStyle().Foreground(colorPrimary)
	case simplifiedStateRunningAgent, simplifiedStateTestingAgent:
		return primaryStyle
	default:
		return mutedStyle
	}
}

func renderSimplifiedStateLabel(state simplifiedState) string {
	return simplifiedStateStyle(state).Render(simplifiedStateSpecByID(state).Label)
}

func retryBadgeStyle(summary retrySummary, state domain.InstanceState) lipgloss.Style {
	switch {
	case summary.Used <= 0:
		return retryBadgeMutedStyle
	case state == domain.InstanceStateInfraFailed && summary.Budget > 0 && summary.Used >= summary.Budget:
		return retryBadgeSpentStyle
	default:
		return retryBadgeUsedStyle
	}
}

func runStateBadge(state string) string {
	switch state {
	case "running":
		return badgeRunning.Render(state)
	case "completed":
		return badgeCompleted.Render(state)
	case "failed":
		return badgeFailed.Render(state)
	case "canceled", "canceling":
		return badgeCanceled.Render(state)
	default:
		return badgeDefault.Render(state)
	}
}
