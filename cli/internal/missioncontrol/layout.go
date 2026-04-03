package missioncontrol

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

func (r rect) contains(x, y int) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

func (r rect) isZero() bool {
	return r.Width <= 0 || r.Height <= 0
}

func (r rect) center() (int, int) {
	return r.X + r.Width/2, r.Y + r.Height/2
}

type paneLayout struct {
	Outer rect
	Inner rect
}

type instanceRowLayout struct {
	Rect          rect
	InstanceIndex int
}

type stateBoxLayout struct {
	Rect  rect
	State simplifiedState
}

type rightPaneLayout struct {
	Pane       paneLayout
	StateBoxes []stateBoxLayout
	LogRect    rect
}

type screenLayout struct {
	Width        int
	Height       int
	HeaderHeight int
	FooterHeight int
	BodyRect     rect
	LeftPane     paneLayout
	RightPane    rightPaneLayout
	InstanceRows []instanceRowLayout
}

type stateBreadcrumbLayout struct {
	Rendered string
	Boxes    []stateBoxLayout
	Height   int
}

type breadcrumbRenderItem struct {
	State    simplifiedState
	Rendered string
	Width    int
	Height   int
}

func (m *model) screenDimensions() (int, int) {
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 40
	}
	return width, height
}

func (m *model) computeScreenLayout() screenLayout {
	width, height := m.screenDimensions()
	headerHeight := lipgloss.Height(m.renderHeader(width))
	footerHeight := lipgloss.Height(m.renderFooter(width))
	usedHeight := headerHeight + footerHeight
	paneHeight := maxInt(10, height-usedHeight)
	bodyY := headerHeight

	paneGapWidth := lipgloss.Width("  ")
	availableWidth := width - paneGapWidth
	leftWidth := maxInt(30, availableWidth/4)
	if leftWidth > availableWidth-20 {
		leftWidth = maxInt(30, availableWidth/2)
	}
	rightWidth := maxInt(20, availableWidth-leftWidth)

	leftOuter := rect{X: 0, Y: bodyY, Width: leftWidth, Height: paneHeight}
	rightOuter := rect{X: leftWidth + paneGapWidth, Y: bodyY, Width: rightWidth, Height: paneHeight}

	leftInner := paneContentRect(leftOuter)
	instanceRows := m.computeInstanceRowLayouts(leftInner)
	rightPane := m.computeRightPaneLayout(rightOuter)

	return screenLayout{
		Width:        width,
		Height:       height,
		HeaderHeight: headerHeight,
		FooterHeight: footerHeight,
		BodyRect: rect{
			X:      0,
			Y:      bodyY,
			Width:  width,
			Height: paneHeight,
		},
		LeftPane: paneLayout{
			Outer: leftOuter,
			Inner: leftInner,
		},
		RightPane:    rightPane,
		InstanceRows: instanceRows,
	}
}

func paneContentRect(outer rect) rect {
	return rect{
		X:      outer.X + 2,
		Y:      outer.Y + 1,
		Width:  maxInt(1, outer.Width-4),
		Height: maxInt(1, outer.Height-4),
	}
}

func (m *model) computeInstanceRowLayouts(content rect) []instanceRowLayout {
	if content.isZero() {
		return nil
	}

	visibleRows := maxInt(0, content.Height-2)
	if visibleRows == 0 {
		return nil
	}
	m.clampInstanceOffset(visibleRows)

	end := minInt(m.instancesOffset+visibleRows, len(m.snapshot.Instances))
	rows := make([]instanceRowLayout, 0, maxInt(0, end-m.instancesOffset))
	rowY := content.Y + 2
	for i := m.instancesOffset; i < end; i++ {
		rows = append(rows, instanceRowLayout{
			Rect: rect{
				X:      content.X,
				Y:      rowY,
				Width:  content.Width,
				Height: 1,
			},
			InstanceIndex: i,
		})
		rowY++
	}
	return rows
}

func (m *model) computeRightPaneLayout(outer rect) rightPaneLayout {
	layout := rightPaneLayout{
		Pane: paneLayout{
			Outer: outer,
			Inner: paneContentRect(outer),
		},
	}
	if layout.Pane.Inner.isZero() {
		return layout
	}

	inst := m.selectedInstance()
	if inst == nil {
		return layout
	}

	current := simplifiedStateForInstanceState(inst.Instance.State)
	identity := m.renderIdentityBar(inst, layout.Pane.Inner.Width)
	identityParts := []string{identity}
	if eb := renderErrorBanner(inst, layout.Pane.Inner.Width); eb != "" {
		identityParts = append(identityParts, eb)
	}
	identityBlock := strings.Join(identityParts, "\n")
	identityHeight := lipgloss.Height(identityBlock)
	breadcrumbOriginY := layout.Pane.Inner.Y + identityHeight + 3
	breadcrumb := m.buildStateBreadcrumbLayout(layout.Pane.Inner.Width, current, layout.Pane.Inner.X, breadcrumbOriginY)
	layout.StateBoxes = breadcrumb.Boxes

	topSections := []string{
		identityBlock,
		strings.Join([]string{
			renderSectionHeader("States", layout.Pane.Inner.Width),
			breadcrumb.Rendered,
		}, "\n"),
		renderSectionHeader("Logs", layout.Pane.Inner.Width),
	}
	top := strings.Join(topSections, "\n\n")
	topHeight := lipgloss.Height(top)
	logHeight := layout.Pane.Inner.Height - topHeight - 1
	if logHeight > 0 {
		layout.LogRect = rect{
			X:      layout.Pane.Inner.X,
			Y:      layout.Pane.Inner.Y + topHeight + 1,
			Width:  layout.Pane.Inner.Width,
			Height: logHeight,
		}
	}
	return layout
}

func (m *model) buildStateBreadcrumbLayout(width int, current simplifiedState, originX, originY int) stateBreadcrumbLayout {
	specs := m.visibleSimplifiedStateSpecs()
	currentIdx := simplifiedStateIndexByID(current)
	items := make([]breadcrumbRenderItem, 0, len(specs))
	for _, spec := range specs {
		specIdx := simplifiedStateIndexByID(spec.ID)

		var icon string
		var boxStyle lipgloss.Style
		var contentStyle lipgloss.Style

		switch {
		case spec.ID == current:
			icon = "●"
			contentStyle = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
		case specIdx < currentIdx:
			icon = "✓"
			contentStyle = okStyle
		default:
			icon = "○"
			contentStyle = mutedStyle
		}

		switch {
		case spec.ID == m.selectedState:
			boxStyle = breadcrumbBoxSelectedStyle
		case spec.ID == current:
			boxStyle = breadcrumbBoxCurrentStyle
		case specIdx < currentIdx:
			boxStyle = breadcrumbBoxCompletedStyle
		default:
			boxStyle = breadcrumbBoxFutureStyle
		}

		label := icon + " " + spec.ShortLabel
		rendered := boxStyle.Render(contentStyle.Render(label))
		items = append(items, breadcrumbRenderItem{
			State:    spec.ID,
			Rendered: rendered,
			Width:    lipgloss.Width(rendered),
			Height:   lipgloss.Height(rendered),
		})
	}

	if len(items) == 0 {
		return stateBreadcrumbLayout{}
	}

	arrow := breadcrumbArrowStyle.Render(" → ")
	arrowWidth := lipgloss.Width(arrow)

	type breadcrumbRow struct {
		parts  []string
		boxes  []stateBoxLayout
		width  int
		height int
	}

	rows := make([]breadcrumbRow, 0, 2)
	currentRow := breadcrumbRow{}
	rowY := originY

	appendRow := func() {
		if len(currentRow.parts) == 0 {
			return
		}
		rows = append(rows, currentRow)
		rowY += currentRow.height
		currentRow = breadcrumbRow{}
	}

	for _, item := range items {
		if len(currentRow.parts) > 0 && currentRow.width+arrowWidth+item.Width > width {
			appendRow()
		}

		x := originX + currentRow.width
		if len(currentRow.parts) > 0 {
			currentRow.parts = append(currentRow.parts, arrow)
			currentRow.width += arrowWidth
			x = originX + currentRow.width
		}

		currentRow.parts = append(currentRow.parts, item.Rendered)
		currentRow.boxes = append(currentRow.boxes, stateBoxLayout{
			Rect: rect{
				X:      x,
				Y:      rowY,
				Width:  item.Width,
				Height: item.Height,
			},
			State: item.State,
		})
		currentRow.width += item.Width
		if item.Height > currentRow.height {
			currentRow.height = item.Height
		}
	}
	appendRow()

	renderedRows := make([]string, 0, len(rows))
	boxes := make([]stateBoxLayout, 0, len(items))
	totalHeight := 0
	for _, row := range rows {
		renderedRows = append(renderedRows, lipgloss.JoinHorizontal(lipgloss.Center, row.parts...))
		boxes = append(boxes, row.boxes...)
		totalHeight += row.height
	}

	return stateBreadcrumbLayout{
		Rendered: strings.Join(renderedRows, "\n"),
		Boxes:    boxes,
		Height:   totalHeight,
	}
}
