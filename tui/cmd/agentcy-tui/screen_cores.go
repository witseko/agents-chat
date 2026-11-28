package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// coresState is the window that lists the running agentcy cores, if open.
type coresState struct {
	open     bool
	loaded   bool
	items    []RunningCore
	selected int
	// confirming asks the user to confirm the stop of the selected core.
	confirming bool
	// stopping is the process ID of the core being stopped, or 0.
	stopping int
}

// Messages that the cores window commands send back to Update.
type (
	coresMsg struct {
		items []RunningCore
		err   error
	}
	coreStoppedMsg struct {
		pid int
		err error
	}
)

// openCores opens the cores window and starts looking for running cores.
func (m model) openCores() (tea.Model, tea.Cmd) {
	m.cores = coresState{open: true}
	if m.chat.id != "" {
		m.chat.input.Blur()
	}
	return m, m.loadCores()
}

// closeCores dismisses the cores window and returns keys to the screen under it.
func (m model) closeCores() (tea.Model, tea.Cmd) {
	m.cores = coresState{}
	if m.screen == screenChat {
		return m.setChatFocus(m.chat.focus)
	}
	return m, nil
}

// loadCores lists the running cores in the background.
func (m model) loadCores() tea.Cmd {
	processes := m.processes
	return func() tea.Msg {
		items, err := processes.List()
		return coresMsg{items: items, err: err}
	}
}

// stopCore stops one core in the background and waits until it exits.
func (m model) stopCore(pid int) tea.Cmd {
	processes := m.processes
	return func() tea.Msg {
		return coreStoppedMsg{pid: pid, err: processes.Stop(pid)}
	}
}

// onCores shows the found cores and keeps the selection in range.
func (m model) onCores(msg coresMsg) model {
	if !m.cores.open {
		return m
	}
	m.cores.loaded = true
	if msg.err != nil {
		return m.showError(msg.err.Error())
	}
	m.cores.items = msg.items
	if len(m.cores.items) == 0 {
		m.cores.confirming = false
	}
	m.cores.selected = min(max(m.cores.selected, 0), max(len(m.cores.items)-1, 0))
	return m
}

// onCoreStopped reports the stop and lists the cores again.
func (m model) onCoreStopped(msg coreStoppedMsg) (tea.Model, tea.Cmd) {
	m.cores.stopping = 0
	if msg.err != nil {
		return m.showError(msg.err.Error()), nil
	}
	m = m.showSuccess(fmt.Sprintf("Core stopped (process %d).", msg.pid))
	if !m.cores.open {
		return m, nil
	}
	return m, m.loadCores()
}

// coresKey handles a key while the cores window shows.
func (m model) coresKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.cores.confirming {
			m.cores.confirming = false
			return m, nil
		}
		return m.closeCores()
	case "up", "k":
		m.cores.selected = max(m.cores.selected-1, 0)
		m.cores.confirming = false
	case "down", "j":
		m.cores.selected = min(m.cores.selected+1, max(len(m.cores.items)-1, 0))
		m.cores.confirming = false
	case "r":
		m.cores.confirming = false
		return m, m.loadCores()
	case "s":
		if len(m.cores.items) > 0 && m.cores.stopping == 0 {
			m.cores.confirming = true
		}
	case "enter":
		if m.cores.confirming && len(m.cores.items) > 0 {
			pid := m.cores.items[m.cores.selected].PID
			m.cores.confirming = false
			m.cores.stopping = pid
			return m, m.stopCore(pid)
		}
	}
	return m, nil
}

// coresHelp lists the keys of the cores window.
func (m model) coresHelp() []hint {
	if m.cores.confirming {
		return []hint{{"⏎", "stop core"}, {"esc", "cancel"}}
	}
	return []hint{{"↑↓", "choose"}, {"s", "stop"}, {"r", "refresh"}, {"esc", "close"}}
}

// coresLines draws the content of the cores window: one row per core, then the
// confirmation or progress text.
func (m model) coresLines(width int) []string {
	var lines []string
	switch {
	case !m.cores.loaded:
		lines = append(lines, dimStyle.Render("Looking for running cores."))
	case len(m.cores.items) == 0:
		lines = append(lines, dimStyle.Render("No agentcy core runs."))
	}
	for index, core := range m.cores.items {
		lines = append(lines, m.coreRow(index, core))
	}
	if m.cores.stopping != 0 {
		lines = append(lines, "", warnStyle.Render(fmt.Sprintf("Stopping process %d.", m.cores.stopping)))
	}
	if m.cores.confirming {
		core := m.cores.items[m.cores.selected]
		description := "Running requests get 5 seconds to finish. Chats stay in the store, and the next tool call starts a core again."
		if core.DataDir != "" && core.DataDir == m.dataDir {
			description += " This UI shows the core as not reachable until then."
		}
		lines = append(lines, "", warnStyle.Render(fmt.Sprintf("Stop process %d?", core.PID)))
		for _, line := range wrapLines(description, width-4, "") {
			lines = append(lines, textStyle.Render(line))
		}
	}
	return append(lines, "", keyHints(hintPairs(m.coresHelp())...))
}

// coreRow draws one core: its process ID, uptime and data directory.
func (m model) coreRow(index int, core RunningCore) string {
	dataDir := core.DataDir
	if dataDir == "" {
		dataDir = "unknown data directory"
	}
	text := textStyle.Render(fmt.Sprintf("process %d", core.PID)) +
		dimStyle.Render(" · up "+cleanLine(core.Uptime)+" · "+cleanLine(dataDir))
	if core.DataDir != "" && core.DataDir == m.dataDir {
		text += accentStyle.Render(" (this UI)")
	}
	if index == m.cores.selected {
		return accentStyle.Render("▎") + text
	}
	return " " + text
}

// hintPairs flattens hints into the key and action pairs that keyHints takes.
func hintPairs(hints []hint) []string {
	pairs := make([]string, 0, len(hints)*2)
	for _, item := range hints {
		pairs = append(pairs, item.key, item.action)
	}
	return pairs
}

// coresLayout returns the cores window box and its top row and left column in the body.
func (m model) coresLayout(bodyHeight int) ([]string, int, int, int) {
	width := popupWidth(m.width, popupMaxWidth)
	box := framedBox(" Running cores ", accentStyle, m.coresLines(width), width)
	return box, max((bodyHeight-len(box))/2, 0), max((m.width-width)/2, 0), width
}

// drawCores places the cores window in the middle of the body.
func (m model) drawCores(body []string) []string {
	box, row, col, _ := m.coresLayout(len(body))
	return overlay(body, box, row, col, m.width)
}

// clickCores selects the core row under a click inside the window. The rows start after
// the top border and the blank line under it.
func (m model) clickCores(x int, row int, bodyHeight int) (tea.Model, tea.Cmd) {
	box, top, left, width := m.coresLayout(bodyHeight)
	if x < left || x >= left+width || row < top || row >= top+len(box) {
		return m, nil
	}
	index := row - top - 2
	if index >= 0 && index < len(m.cores.items) && m.cores.stopping == 0 {
		m.cores.selected = index
		m.cores.confirming = false
	}
	return m, nil
}
