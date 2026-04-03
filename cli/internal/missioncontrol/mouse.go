package missioncontrol

import tea "github.com/charmbracelet/bubbletea"

func (m *model) handleMouseMsg(msg tea.MouseMsg) tea.Cmd {
	if m.confirmQuit {
		return nil
	}

	layout := m.computeScreenLayout()

	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		switch {
		case layout.LeftPane.Outer.contains(msg.X, msg.Y):
			m.focusPane(paneLeft)
			for _, row := range layout.InstanceRows {
				if row.Rect.contains(msg.X, msg.Y) {
					return m.selectInstance(row.InstanceIndex)
				}
			}
			return nil
		case layout.RightPane.Pane.Outer.contains(msg.X, msg.Y):
			m.focusPane(paneRight)
			for _, box := range layout.RightPane.StateBoxes {
				if box.Rect.contains(msg.X, msg.Y) {
					return m.selectState(box.State)
				}
			}
			return nil
		default:
			return nil
		}
	}

	if msg.Action == tea.MouseActionPress && (msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown) {
		if layout.RightPane.LogRect.contains(msg.X, msg.Y) {
			return m.updateLogViewport(msg)
		}
		return nil
	}

	return nil
}
