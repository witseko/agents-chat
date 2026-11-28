package main

import (
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// listState holds the active or archived conversations of the checkout, newest first.
type listState struct {
	items    []ConversationSummary
	selected int
	// onClosedHeader puts the selection on the CLOSED section header instead of a chat.
	onClosedHeader bool
	// closedExpanded shows the closed chats under the CLOSED header of the active view.
	closedExpanded bool
	loaded         bool
	archived       bool
}

// closedHeader stands for the CLOSED section header in the list of sidebar entries.
const closedHeader = -1

// closedHoverID is the hover id of the CLOSED section header in the chat list.
const closedHoverID = "chats-closed"

// homeKey handles a key while no conversation is open.
func (m model) homeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "n":
		return m.openForm()
	case "c":
		return m.openCores()
	case "/", "a", "v", "e":
		return m.listAction(msg.String())
	case "r":
		return m.inform("Refreshing."), m.loadList()
	case "q":
		return m, tea.Quit
	}
	return m.listNavigate(msg.String())
}

// listNavigate moves the chat selection or opens the selected chat.
func (m model) listNavigate(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.moveListSelection(-1)
	case "down", "j":
		m.moveListSelection(1)
	case "enter":
		if m.list.onClosedHeader {
			return m.toggleClosed(), nil
		}
		item, found := m.selectedChat()
		if !found {
			return m, nil
		}
		return m.openChat(item.Conversation)
	}
	return m, nil
}

// listEntries returns the sidebar entries in drawing order: the open chats, then the CLOSED
// header and, while that section is expanded, the closed chats. The archive draws every chat
// without a section. An entry is an index into m.list.items or closedHeader.
func (m model) listEntries() []int {
	var entries, closed []int
	for index, item := range m.list.items {
		if item.Open || m.list.archived {
			entries = append(entries, index)
		} else {
			closed = append(closed, index)
		}
	}
	if len(closed) == 0 {
		return entries
	}
	entries = append(entries, closedHeader)
	if m.list.closedExpanded {
		entries = append(entries, closed...)
	}
	return entries
}

// selectedEntry returns the position of the selection in entries.
func (m model) selectedEntry(entries []int) int {
	target := m.list.selected
	if m.list.onClosedHeader {
		target = closedHeader
	}
	for position, entry := range entries {
		if entry == target {
			return position
		}
	}
	return 0
}

// selectEntry puts the selection on a chat or on the CLOSED header.
func (m *model) selectEntry(entry int) {
	m.list.onClosedHeader = entry == closedHeader
	if entry != closedHeader {
		m.list.selected = entry
	}
}

// moveListSelection moves the selection by step entries and stops at both ends.
func (m *model) moveListSelection(step int) {
	entries := m.listEntries()
	if len(entries) == 0 {
		return
	}
	position := min(max(m.selectedEntry(entries)+step, 0), len(entries)-1)
	m.selectEntry(entries[position])
}

// selectedChat returns the selected chat, or false when the selection is on the CLOSED header
// or the list is empty.
func (m model) selectedChat() (ConversationSummary, bool) {
	if m.list.onClosedHeader || m.list.selected < 0 || m.list.selected >= len(m.list.items) {
		return ConversationSummary{}, false
	}
	return m.list.items[m.list.selected], true
}

// toggleClosed collapses or expands the CLOSED section and selects its header.
func (m model) toggleClosed() model {
	m.list.closedExpanded = !m.list.closedExpanded
	m.list.onClosedHeader = true
	return m
}

// hideClosedSelection moves the selection to the CLOSED header when the selected chat sits in
// the collapsed section, and back to a chat when no closed chat is left.
func (m *model) hideClosedSelection() {
	entries := m.listEntries()
	hasHeader := slices.Contains(entries, closedHeader)
	chatVisible := slices.Contains(entries, m.list.selected)
	switch {
	case m.list.onClosedHeader && !hasHeader:
		m.list.onClosedHeader = false
		if !chatVisible && len(entries) > 0 {
			m.selectEntry(entries[len(entries)-1])
		}
	case !m.list.onClosedHeader && !chatVisible && hasHeader:
		m.list.onClosedHeader = true
	}
}

// listAction runs the search, archive, rename and view keys that work wherever the chat list has
// focus.
func (m model) listAction(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "/":
		return m.openSearch()
	case "a":
		return m.archiveSelected()
	case "e":
		return m.renameSelected()
	case "v":
		return m.showArchive(!m.list.archived)
	}
	return m, nil
}

// showArchive switches the chat list between active and archived chats.
func (m model) showArchive(archived bool) (tea.Model, tea.Cmd) {
	if m.list.archived == archived {
		return m, nil
	}
	m.list = listState{archived: archived}
	return m, m.loadList()
}

// archiveSelected archives the selected closed chat, or returns it to the active list when
// the archive shows.
func (m model) archiveSelected() (tea.Model, tea.Cmd) {
	item, found := m.selectedChat()
	if !found {
		return m, nil
	}
	if item.Open {
		return m.showError("Close the chat before archiving it."), nil
	}
	return m.requestArchive(item.Conversation, !m.list.archived)
}

// archive moves a conversation to the archive or back to the active list. With
// deprecateTasks, the core ends the unfinished tasks as deprecated in the same step.
func (m model) archive(conversation string, archived bool, deprecateTasks bool) tea.Cmd {
	request := ArchiveRequest{
		Directory: m.directory, Conversation: conversation,
		Archived: archived, DeprecateTasks: deprecateTasks,
	}
	client := m.client
	return func() tea.Msg {
		result, err := client.Archive(request)
		return archivedMsg{conversation: conversation, archived: result.Archived, err: err}
	}
}

// onArchived reports the move, reloads the list and refreshes the open chat's header.
func (m model) onArchived(msg archivedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	notice := "Chat moved back to active chats."
	if msg.archived {
		notice = "Chat archived. Press v to see the archive."
	}
	m = m.showSuccess(notice)
	commands := []tea.Cmd{m.loadList()}
	if msg.conversation == m.chat.id {
		var poll tea.Cmd
		m, poll = m.refreshChat()
		commands = append(commands, poll)
	}
	return m, tea.Batch(commands...)
}

// onList replaces the list and keeps selection on the same conversation when it still exists.
// An answer for the other view arrives after a switch and is dropped. The first answer after
// a switch selects the open chat when it is in the list.
func (m model) onList(msg listMsg) model {
	if msg.err != nil {
		return m.fail(msg.err)
	}
	if msg.archived != m.list.archived {
		return m
	}
	firstLoad := !m.list.loaded
	selectedID := ""
	if item, found := m.selectedChat(); found && !firstLoad {
		selectedID = item.Conversation
	}
	m.list.items = msg.list.Conversations
	m.list.loaded = true
	m.restoreListSelection(firstLoad, selectedID)
	m.syncOpenChatInList()
	m.hideClosedSelection()
	if m.status == "Refreshing." {
		m.status = ""
		m.statusTicks = 0
	}
	return m.succeed()
}

// restoreListSelection keeps m.list.selected on the target conversation or clamps it in range.
func (m *model) restoreListSelection(firstLoad bool, previousID string) {
	target := previousID
	if firstLoad && m.chat.id != "" {
		target = m.chat.id
	}
	if target != "" {
		for index, item := range m.list.items {
			if item.Conversation == target {
				m.list.selected = index
				return
			}
		}
	}
	m.list.selected = min(max(m.list.selected, 0), max(len(m.list.items)-1, 0))
}

// syncOpenChatInList updates the sidebar summary of the open conversation from its latest
// loaded status so the sidebar and open view never disagree. A chat that closes moves into the
// CLOSED section.
func (m *model) syncOpenChatInList() {
	if m.chat.id == "" || !m.chat.loaded {
		return
	}
	members, joined := 0, 0
	for _, member := range m.chat.view.Status.Members {
		if member.Role == "human" || member.State == "retired" {
			continue
		}
		members++
		if member.State != "invited" {
			joined++
		}
	}
	for index := range m.list.items {
		if m.list.items[index].Conversation == m.chat.id {
			m.list.items[index].Open = m.chat.view.Status.Open
			m.list.items[index].Members = members
			m.list.items[index].Joined = joined
			m.hideClosedSelection()
			return
		}
	}
}

// listFocused reports whether keys go to the chat list.
func (m model) listFocused() bool {
	return m.screen == screenHome || m.chat.focus == focusList
}

// sidebarView draws the chat list, two lines per chat, and scrolls to keep the selection
// visible.
func (m model) sidebarView(width int, height int) []string {
	views, _ := m.viewButtons()
	lines := []string{" " + views, ""}
	switch {
	case !m.list.loaded:
		lines = append(lines, " "+dimStyle.Render("Loading."))
	case len(m.list.items) == 0 && m.list.archived:
		lines = append(lines, " "+dimStyle.Render("No archived chats."))
	case len(m.list.items) == 0:
		lines = append(lines, " "+dimStyle.Render("No chats yet."))
	}
	now := m.now()
	for _, row := range m.listLayout(height) {
		if row.entry == closedHeader {
			lines = append(lines, m.closedHeaderLine(width), "")
			continue
		}
		index := row.entry
		item := m.list.items[index]
		title := ansi.Truncate(cleanLine(item.Title), width-2, "…")
		detail := dimStyle.Render("closed · " + age(item.Created, now))
		if item.Open {
			glyph := okStyle.Render("●")
			if item.Joined < item.Members {
				glyph = dimStyle.Render("◌")
			}
			detail = glyph + dimStyle.Render(fmt.Sprintf(" %d/%d joined · %s", item.Joined, item.Members, age(item.Created, now)))
		}
		openHere := item.Conversation == m.chat.id && m.screen == screenChat
		switch {
		case index == m.list.selected && !m.list.onClosedHeader && m.listFocused():
			lines = append(lines,
				fill(accentStyle.Render("▎")+brightStyle.Render(title), width, colorSelect),
				fill(accentStyle.Render("▎")+detail, width, colorSelect))
		case m.hover == fmt.Sprintf("chat:%d", index):
			lines = append(lines,
				fill(dimStyle.Render("▎")+brightStyle.Render(title), width, colorBand),
				fill(dimStyle.Render("▎")+detail, width, colorBand))
		case openHere:
			lines = append(lines, dimStyle.Render("▎")+brightStyle.Render(title), dimStyle.Render("▎")+detail)
		case item.Open:
			lines = append(lines, " "+textStyle.Render(title), " "+detail)
		default:
			lines = append(lines, " "+dimStyle.Render(title), " "+detail)
		}
		lines = append(lines, "")
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	actions, _ := m.listButtons()
	return append(lines[:max(height-1, 0)], " "+actions)
}

// listRow places one sidebar entry, a chat index or closedHeader, at its first body row.
type listRow struct {
	entry int
	top   int
}

// listEntrySize returns the rows an entry takes with its blank separator: three for a chat,
// two for the CLOSED header.
func listEntrySize(entry int) int {
	if entry == closedHeader {
		return 2
	}
	return 3
}

// listLayout returns the entries sidebarView draws under its two header rows, scrolled so the
// selection is visible. The last row of the body holds the list buttons.
func (m model) listLayout(height int) []listRow {
	entries := m.listEntries()
	if !m.list.loaded || len(entries) == 0 {
		return nil
	}
	available := height - 4
	first := m.selectedEntry(entries)
	used := listEntrySize(entries[first])
	for first > 0 && used+listEntrySize(entries[first-1]) <= available {
		first--
		used += listEntrySize(entries[first])
	}
	var rows []listRow
	top := 2
	for _, entry := range entries[first:] {
		if len(rows) > 0 && top-2+listEntrySize(entry) > available {
			break
		}
		rows = append(rows, listRow{entry: entry, top: top})
		top += listEntrySize(entry)
	}
	return rows
}

// listEntryAt returns the sidebar entry drawn at a body row: a chat index or closedHeader.
func (m model) listEntryAt(row int, height int) (int, bool) {
	for _, placed := range m.listLayout(height) {
		lastRow := placed.top + listEntrySize(placed.entry) - 2
		if row >= placed.top && row <= lastRow {
			return placed.entry, true
		}
	}
	return 0, false
}

// closedHeaderLine draws the clickable CLOSED section header with its collapse marker.
func (m model) closedHeaderLine(width int) string {
	closed := 0
	for _, item := range m.list.items {
		if !item.Open {
			closed++
		}
	}
	marker := "▸"
	if m.list.closedExpanded {
		marker = "▾"
	}
	title := headStyle.Render(fmt.Sprintf("%s CLOSED (%d)", marker, closed))
	switch {
	case m.list.onClosedHeader && m.listFocused():
		return fill(accentStyle.Render("▎")+title, width, colorSelect)
	case m.hover == closedHoverID:
		return fill(dimStyle.Render("▎")+title, width, colorBand)
	}
	return " " + title
}

// viewButtons draws the switch between active and archived chats above the list.
func (m model) viewButtons() (string, []span) {
	return m.buttons(
		button{id: "view:active", label: "Active", primary: !m.list.archived},
		button{id: "view:archived", label: "Archived", primary: m.list.archived},
	)
}

// listButtons draws the buttons under the list.
func (m model) listButtons() (string, []span) {
	return m.buttons(button{id: "new", label: "+ New chat"}, button{id: "search", label: "Search"})
}

// homeView fills the center while no conversation is open.
func (m model) homeView(width int, height int) []string {
	lines := make([]string, max(height/3, 1))
	if m.list.loaded && len(m.list.items) == 0 && m.list.archived {
		return append(lines,
			"  "+brightStyle.Render("No archived chats."),
			"",
			"  "+dimStyle.Render("Select a closed chat and press ")+accentStyle.Render("a")+dimStyle.Render(" to archive it. ")+
				accentStyle.Render("v")+dimStyle.Render(" shows active chats."))
	}
	if m.list.loaded && len(m.list.items) == 0 {
		return append(lines,
			"  "+brightStyle.Render("No chats in this workspace yet."),
			"",
			"  "+dimStyle.Render("Press ")+accentStyle.Render("n")+dimStyle.Render(" to write a brief, name the team and start one."))
	}
	return append(lines,
		"  "+brightStyle.Render("No chat open."),
		"",
		"  "+accentStyle.Render("↑ ↓")+dimStyle.Render(" choose a chat   ")+accentStyle.Render("⏎")+dimStyle.Render(" open   ")+
			accentStyle.Render("n")+dimStyle.Render(" new chat   ")+accentStyle.Render("/")+dimStyle.Render(" search"))
}
