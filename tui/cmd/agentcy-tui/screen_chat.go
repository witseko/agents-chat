package main

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/witseko/agentcy/internal/wire"
)

const (
	inputPlaceholder = "Message everyone. @Name addresses one member, ? asks a question."
	maxBriefLines    = 2
	scrollPageSize   = 10
	// autoPageThreshold loads the next transcript page when scrolling comes within this many
	// lines of the top or bottom edge of the loaded window.
	autoPageThreshold = 12
	// sendButtonWidth is the width of the Send button and the gap before it.
	sendButtonWidth = 10
	// composerLines is the fixed row count of the bottom composer area.
	composerLines = 3
	// staleMemberIdleSeconds dims the active dot when a member has not contacted the bridge for 10 minutes.
	staleMemberIdleSeconds = 600
	// messageIndent lines a message body up under its author, after the time column.
	messageIndent = "       "
)

// chatFocus names the part of the conversation screen that receives keys.
type chatFocus int

const (
	focusInput chatFocus = iota
	focusTeam
	focusList
)

// roleOrder sorts members in the team panel: leads first, the human last.
var roleOrder = map[string]int{"lead": 0, "co-lead": 1, "worker": 2, "human": 3}

// cachedMessageLines holds the wrapped lines of one message for a given width and author role.
type cachedMessageLines struct {
	role  string
	lines []string
}

// transcriptLinesCache stores the wrapped transcript lines for one width and state version,
// caching individual messages so page loads and new posts do not re-wrap the whole conversation.
type transcriptLinesCache struct {
	valid        bool
	width        int
	highlight    int64
	version      uint64
	hasOlder     bool
	loadingOlder bool
	lines        []string
	perMessage   map[int64]cachedMessageLines
}

// chatState holds the open conversation and the transcript read so far.
type chatState struct {
	id             string
	view           ConversationView
	loaded         bool
	messages       []TranscriptMessage
	cursor         int64
	polling        bool
	refreshPending bool
	tailPending    bool
	loadingOlder   bool
	hasOlder       bool
	hasNewer       bool
	version        uint64
	linesCache     *transcriptLinesCache
	input          textinput.Model
	summary        textinput.Model
	closing        bool
	// deprecateOnClose makes the close also end the unfinished tasks as deprecated.
	deprecateOnClose bool
	retiring         string
	focus            chatFocus
	selected         int
	// scroll counts transcript lines hidden below the view. Zero follows the tail.
	scroll int
	// jumping asks the view to scroll to jumpTarget once the initial page has loaded.
	// A jumpTarget of zero is the brief, at the start of the transcript.
	jumping    bool
	jumpTarget int64
	// highlight is the ID of the message a search opened the chat at, or zero.
	highlight int64
	// tab selects between the Chat transcript and the Tasks view.
	tab   chatTab
	tasks tasksState
}

// humanPost is a parsed input line.
type humanPost struct {
	body string
	kind string
	to   string
}

// newChat returns the state for a conversation that has not been read yet.
func newChat(conversation string, width int) chatState {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = inputPlaceholder
	input.CharLimit = maxBriefLength
	summary := textinput.New()
	summary.Prompt = ""
	summary.Placeholder = "Summary for the team (optional)"
	summary.CharLimit = maxBriefLength
	chat := chatState{
		id:         conversation,
		input:      input,
		summary:    summary,
		linesCache: &transcriptLinesCache{},
	}
	chat.resize(width)
	return chat
}

// resize fits the input fields to the center column.
func (c *chatState) resize(width int) {
	c.input.SetWidth(max(width-6-sendButtonWidth, 10))
}

// updateInput sends a message to the focused input field.
func (c chatState) updateInput(msg tea.Msg) (chatState, tea.Cmd) {
	var command tea.Cmd
	if c.closing {
		c.summary, command = c.summary.Update(msg)
	} else if c.focus == focusInput {
		c.input, command = c.input.Update(msg)
	}
	return c, command
}

// openChat shows a conversation and reads its newest page first.
func (m model) openChat(conversation string) (tea.Model, tea.Cmd) {
	return m.openChatMode(conversation, showTail, 0)
}

// openChatAt opens a conversation at a search target: target 0 opens at the brief (first page),
// and a positive target opens at the page ending at that message ID.
func (m model) openChatAt(conversation string, target int64) (tea.Model, tea.Cmd) {
	mode := showStart
	if target > 0 {
		mode = showJump
	}
	return m.openChatMode(conversation, mode, target)
}

// openChatMode initializes the chat view and fetches its first transcript page.
func (m model) openChatMode(conversation string, mode showMode, target int64) (tea.Model, tea.Cmd) {
	m.screen = screenChat
	_, center, _ := m.columnWidths()
	m.chat = newChat(conversation, center)
	if mode == showStart || mode == showJump {
		m.chat.jumping = true
		m.chat.jumpTarget = target
	}
	m.inviteWindow = inviteState{}
	m.status = ""
	for index, item := range m.list.items {
		if item.Conversation == conversation {
			m.selectEntry(index)
			// Opening a closed chat shows it in the CLOSED section.
			m.list.closedExpanded = m.list.closedExpanded || !item.Open
		}
	}
	focus := m.chat.input.Focus()
	var load tea.Cmd
	switch mode {
	case showStart:
		m, load = m.requestShow(showStart, ShowRequest{Directory: m.directory, Conversation: conversation})
	case showJump:
		before := target + 1
		m, load = m.requestShow(showJump, ShowRequest{Directory: m.directory, Conversation: conversation, Before: &before})
	default:
		m, load = m.loadTail()
	}
	return m, tea.Batch(focus, load)
}

// loadTail requests the newest transcript page of the open conversation.
func (m model) loadTail() (model, tea.Cmd) {
	return m.requestShow(showTail, ShowRequest{
		Directory: m.directory, Conversation: m.chat.id, Tail: true,
	})
}

// loadTailRefresh jumps to the newest transcript page, queuing the jump if a poll is in flight.
func (m model) loadTailRefresh() (model, tea.Cmd) {
	if m.chat.polling {
		m.chat.tailPending = true
		return m, nil
	}
	return m.loadTail()
}

// pollChat requests the transcript after the cursor unless a request is in flight.
func (m model) pollChat() (model, tea.Cmd) {
	return m.requestShow(showAfter, ShowRequest{
		Directory: m.directory, Conversation: m.chat.id, After: m.chat.cursor,
	})
}

// pollStatus refreshes header and team status while viewing an older search page without
// disturbing the loaded transcript window.
func (m model) pollStatus() (model, tea.Cmd) {
	return m.requestShow(showStatus, ShowRequest{
		Directory: m.directory, Conversation: m.chat.id, Tail: true,
	})
}

// refreshChat requests fresh state after a mutation or screen return, queuing one follow-up
// if another Show request is already in flight.
func (m model) refreshChat() (model, tea.Cmd) {
	if m.chat.id == "" {
		return m, nil
	}
	if m.chat.polling {
		m.chat.refreshPending = true
		return m, nil
	}
	if !m.chat.loaded {
		return m.loadTail()
	}
	if m.chat.hasNewer {
		return m.pollStatus()
	}
	return m.pollChat()
}

// requestShow sends a forward or initial Show request unless one is already in flight.
func (m model) requestShow(mode showMode, request ShowRequest) (model, tea.Cmd) {
	if m.chat.polling {
		return m, nil
	}
	m.chat.polling = true
	before := int64(0)
	if request.Before != nil {
		before = *request.Before
	}
	client := m.client
	return m, func() tea.Msg {
		view, err := client.Show(request)
		return showMsg{
			conversation: request.Conversation,
			mode:         mode,
			after:        request.After,
			before:       before,
			view:         view,
			err:          err,
		}
	}
}

// loadOlderMessages fetches the page immediately preceding the oldest loaded message.
func (m model) loadOlderMessages() (model, tea.Cmd) {
	if m.chat.loadingOlder || !m.chat.hasOlder || len(m.chat.messages) == 0 {
		return m, nil
	}
	m.chat.loadingOlder = true
	before := m.chat.messages[0].ID
	request := ShowRequest{Directory: m.directory, Conversation: m.chat.id, Before: &before}
	client := m.client
	return m, func() tea.Msg {
		view, err := client.Show(request)
		return showMsg{
			conversation: request.Conversation,
			mode:         showOlder,
			before:       before,
			view:         view,
			err:          err,
		}
	}
}

// onShow routes one transcript page response by its pagination mode and drains any queued refresh.
func (m model) onShow(msg showMsg) (tea.Model, tea.Cmd) {
	if msg.conversation != m.chat.id {
		return m, nil
	}
	if msg.mode == showOlder {
		return m.onShowOlder(msg)
	}
	m.chat.polling = false
	if msg.err != nil {
		return m.flushPendingShow(m.fail(msg.err), nil)
	}
	var next tea.Model
	var cmd tea.Cmd
	switch msg.mode {
	case showAfter:
		next, cmd = m.onShowAfter(msg)
	case showStatus:
		next, cmd = m.onShowStatus(msg)
	default:
		next, cmd = m.onShowWindow(msg)
	}
	return next.(model).flushPendingShow(next.(model), cmd)
}

// flushPendingShow runs a queued tail jump or refresh once the in-flight Show finishes.
func (m model) flushPendingShow(next model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if next.chat.polling {
		return next, cmd
	}
	if next.chat.tailPending {
		next.chat.tailPending = false
		next.chat.refreshPending = false
		var tailCmd tea.Cmd
		next, tailCmd = next.loadTail()
		return next, tea.Batch(cmd, tailCmd)
	}
	if next.chat.refreshPending {
		next.chat.refreshPending = false
		var refreshCmd tea.Cmd
		next, refreshCmd = next.refreshChat()
		return next, tea.Batch(cmd, refreshCmd)
	}
	return next, cmd
}

// applyChatLifecycle drops modal prompts and cached codes that are no longer valid after
// an external close or retirement.
func (m *model) applyChatLifecycle() {
	if !m.chat.loaded || len(m.chat.view.Status.Members) == 0 {
		return
	}
	if !m.chat.view.Status.Open {
		delete(m.openInvites, m.chat.id)
		delete(m.invitations, m.chat.id)
		m.inviteWindow.open = false
		m.chat.closing = false
		m.chat.deprecateOnClose = false
		m.chat.retiring = ""
		return
	}
	if m.chat.retiring != "" {
		if member, ok := m.chat.view.Status.Members[m.chat.retiring]; !ok || member.State == "retired" {
			m.chat.retiring = ""
		}
	}
}

// onShowStatus refreshes status metadata without replacing the loaded transcript window.
func (m model) onShowStatus(msg showMsg) (tea.Model, tea.Cmd) {
	selectedName := m.selectedMember()
	prevMembers := m.chat.view.Status.Members
	prevOpen := m.chat.view.Status.Open
	prevArchived := m.chat.view.Archived
	rolesChanged := memberRolesChanged(prevMembers, msg.view.Status.Members)
	listChanged := m.chat.loaded && (rosterChanged(prevMembers, msg.view.Status.Members) ||
		prevOpen != msg.view.Status.Open ||
		prevArchived != msg.view.Archived)
	m.chat.view.Title = msg.view.Title
	m.chat.view.Brief = msg.view.Brief
	m.chat.view.Created = msg.view.Created
	m.chat.view.Status = msg.view.Status
	m.chat.view.Archived = msg.view.Archived
	m.chat.view.TotalMessages = msg.view.TotalMessages
	if msg.view.Cursor <= m.chat.cursor {
		m.chat.hasNewer = false
	}
	if rolesChanged {
		m.chat.version++
	}
	m.applyChatLifecycle()
	m.restoreSelection(selectedName)
	m.syncOpenChatInList()
	m = m.succeed()
	if listChanged {
		return m, m.loadList()
	}
	return m, nil
}

// onShowWindow replaces the loaded window after an initial tail, start, or search-jump load.
func (m model) onShowWindow(msg showMsg) (tea.Model, tea.Cmd) {
	m.chat.messages = msg.view.Messages
	m.chat.cursor = msg.view.Cursor
	m.chat.view = msg.view
	m.chat.loaded = true
	m.chat.version++
	m.applyChatLifecycle()
	m.syncOpenChatInList()
	m = m.succeed()
	switch msg.mode {
	case showStart:
		m.chat.hasOlder = false
		m.chat.hasNewer = msg.view.More
	case showJump:
		m.chat.hasOlder = msg.view.More
		m.chat.hasNewer = jumpHasNewer(msg.view)
	default:
		m.chat.hasOlder = msg.view.More
		m.chat.hasNewer = false
		m.chat.scroll = 0
	}
	if m.chat.jumping {
		m = m.scrollToTarget()
	}
	m.clampChatScroll()
	if msg.mode == showTail && m.chat.hasOlder && !m.chat.loadingOlder {
		_, center, _ := m.columnWidths()
		if len(m.transcriptLines(center)) < m.transcriptHeight(center) {
			return m.loadOlderMessages()
		}
	}
	return m, nil
}

// jumpHasNewer reports whether messages after the jump page may exist.
func jumpHasNewer(view ConversationView) bool {
	if view.TotalMessages != nil && !view.More && len(view.Messages) >= *view.TotalMessages {
		return false
	}
	return len(view.Messages) > 0
}

// onShowOlder prepends an older page above the loaded messages without shifting the viewport.
func (m model) onShowOlder(msg showMsg) (tea.Model, tea.Cmd) {
	m.chat.loadingOlder = false
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	if len(m.chat.messages) == 0 || msg.before != m.chat.messages[0].ID {
		return m, nil
	}
	selectedName := m.selectedMember()
	rolesChanged := memberRolesChanged(m.chat.view.Status.Members, msg.view.Status.Members)
	m.chat.view.Status = msg.view.Status
	m.chat.view.TotalMessages = msg.view.TotalMessages
	m.chat.hasOlder = msg.view.More
	m.applyChatLifecycle()
	m.restoreSelection(selectedName)
	m.syncOpenChatInList()
	older := filterBefore(msg.view.Messages, m.chat.messages[0].ID)
	if len(older) > 0 {
		combined := make([]TranscriptMessage, 0, len(older)+len(m.chat.messages))
		combined = append(combined, older...)
		combined = append(combined, m.chat.messages...)
		m.chat.messages = combined
		m.chat.version++
	} else if rolesChanged {
		m.chat.version++
	}
	m.clampChatScroll()
	return m.succeed(), nil
}

// onShowAfter appends newer messages, preserving scroll only when genuinely scrolled up.
func (m model) onShowAfter(msg showMsg) (tea.Model, tea.Cmd) {
	if msg.after != m.chat.cursor {
		return m, nil
	}
	_, center, _ := m.columnWidths()
	maxBefore := m.maxTranscriptScroll()
	wasScrolledUp := m.chat.scroll > 0 && maxBefore > 0
	wasLoaded := m.chat.loaded
	selectedName := m.selectedMember()
	prevMembers := m.chat.view.Status.Members
	prevOpen := m.chat.view.Status.Open
	prevArchived := m.chat.view.Archived
	rolesChanged := memberRolesChanged(prevMembers, msg.view.Status.Members)
	listChanged := wasLoaded && (rosterChanged(prevMembers, msg.view.Status.Members) ||
		prevOpen != msg.view.Status.Open ||
		prevArchived != msg.view.Archived)
	added := msg.view.Messages
	if len(m.chat.messages) > 0 {
		added = filterAfter(added, m.chat.messages[len(m.chat.messages)-1].ID)
	}
	if len(added) > 0 {
		m.chat.messages = append(m.chat.messages, added...)
		m.chat.version++
	} else if rolesChanged {
		m.chat.version++
	}
	m.chat.cursor = msg.view.Cursor
	m.chat.view = msg.view
	m.chat.loaded = true
	m.applyChatLifecycle()
	m.restoreSelection(selectedName)
	m.syncOpenChatInList()
	if wasScrolledUp && len(added) > 0 {
		m.chat.scroll += len(messageLines(added, msg.view.Status.Members, center, 0))
	}
	m.clampChatScroll()
	m = m.succeed()
	var commands []tea.Cmd
	if listChanged {
		commands = append(commands, m.loadList())
	}
	if m.chat.hasNewer {
		m.chat.hasNewer = msg.view.More
		return m, tea.Batch(commands...)
	}
	if msg.view.More && m.chat.scroll == 0 {
		var poll tea.Cmd
		m, poll = m.pollChat()
		commands = append(commands, poll)
	}
	return m, tea.Batch(commands...)
}

// filterBefore returns messages strictly older than limit.
func filterBefore(messages []TranscriptMessage, limit int64) []TranscriptMessage {
	var out []TranscriptMessage
	for _, message := range messages {
		if message.ID < limit {
			out = append(out, message)
		}
	}
	return out
}

// filterAfter returns messages strictly newer than limit.
func filterAfter(messages []TranscriptMessage, limit int64) []TranscriptMessage {
	var out []TranscriptMessage
	for _, message := range messages {
		if message.ID > limit {
			out = append(out, message)
		}
	}
	return out
}

// memberRolesChanged reports whether any participant role was added or changed.
func memberRolesChanged(before map[string]MemberStatus, after map[string]MemberStatus) bool {
	if len(before) != len(after) {
		return true
	}
	for name, member := range after {
		if prev, found := before[name]; !found || prev.Role != member.Role {
			return true
		}
	}
	return false
}

// rosterChanged reports whether membership, roles, joined state, or archived state changed.
func rosterChanged(before map[string]MemberStatus, after map[string]MemberStatus) bool {
	if memberRolesChanged(before, after) {
		return true
	}
	for name, member := range after {
		prev := before[name]
		if (prev.State == "retired") != (member.State == "retired") ||
			(prev.State == "invited") != (member.State == "invited") {
			return true
		}
	}
	return false
}

// restoreSelection keeps the team selection on the same member after roster updates.
func (m *model) restoreSelection(name string) {
	names := m.slotNames()
	if len(names) == 0 {
		m.chat.selected = 0
		return
	}
	if name != "" {
		if index := slices.Index(names, name); index >= 0 {
			m.chat.selected = index
			return
		}
	}
	m.chat.selected = min(max(m.chat.selected, 0), len(names)-1)
}

// scrollToTarget scrolls the transcript so the target message starts at the top of the
// view and marks it. A target that is not in the transcript, such as the brief, scrolls to
// the start.
func (m model) scrollToTarget() model {
	m.chat.jumping = false
	_, center, _ := m.columnWidths()
	for _, message := range m.chat.messages {
		if message.ID == m.chat.jumpTarget {
			m.chat.highlight = message.ID
			break
		}
	}
	lines := m.transcriptLines(center)
	total := len(lines)
	top := 0
	cursor := 0
	if m.chat.hasOlder {
		cursor = 1
	}
	cache := m.chat.linesCache
	for _, message := range m.chat.messages {
		if message.ID == m.chat.jumpTarget {
			top = cursor
			break
		}
		if cache != nil {
			if cached, ok := cache.perMessage[message.ID]; ok {
				cursor += len(cached.lines)
				continue
			}
		}
		cursor += len(singleMessageLines(message, m.chat.view.Status.Members, center, m.chat.highlight))
	}
	height := m.transcriptHeight(center)
	m.chat.scroll = max(min(total-top-height, total-height), 0)
	return m
}

// transcriptHeight returns the rows the transcript gets between the header and the
// message line.
func (m model) transcriptHeight(width int) int {
	bodyHeight := max(m.height-2, 1)
	return max(bodyHeight-m.chatHeaderHeight(width)-composerLines, 1)
}

// chatKey handles a key in the conversation view. Keys that work everywhere come first,
// then the keys of the focused part.
func (m model) chatKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.chat.closing {
		return m.closingKey(msg)
	}
	if m.chat.retiring != "" {
		return m.retiringKey(msg)
	}
	switch msg.String() {
	case "tab":
		return m.setChatFocus(m.chat.focus + 1)
	case "shift+tab":
		return m.setChatFocus(m.chat.focus + 2)
	case "ctrl+x":
		return m.startClosing()
	case "ctrl+p":
		return m.openInvite(""), nil
	case "ctrl+l":
		return m.prefillLaunch()
	case "ctrl+r":
		return m.reissueSelected()
	case "ctrl+t":
		if m.chat.tab == tabTasks {
			return m.setChatTab(tabChat)
		}
		return m.setChatTab(tabTasks)
	case "pgup", "pgdown":
		if m.chat.tab == tabTasks && m.chat.focus == focusInput {
			return m.tasksKey(msg)
		}
		return m.scrollTranscript(msg.String())
	}
	switch m.chat.focus {
	case focusTeam:
		return m.teamKey(msg)
	case focusList:
		return m.chatListKey(msg)
	}
	if m.chat.tab == tabTasks {
		return m.tasksKey(msg)
	}
	return m.inputKey(msg)
}

// setChatFocus moves keys to the input, the team panel or the chat list, in that cycle.
func (m model) setChatFocus(focus chatFocus) (tea.Model, tea.Cmd) {
	m.chat.focus = focus % 3
	if m.chat.focus == focusInput && m.chat.tab == tabChat {
		return m, m.chat.input.Focus()
	}
	m.chat.input.Blur()
	return m, nil
}

// inputKey handles a key while the message line has focus.
func (m model) inputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.submitInput()
	case "esc":
		return m.setChatFocus(focusList)
	case "up", "down":
		return m.scrollTranscript(msg.String())
	}
	var command tea.Cmd
	m.chat, command = m.chat.updateInput(msg)
	return m, command
}

// teamKey handles a key while the team panel has focus.
func (m model) teamKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.chat.selected = max(m.chat.selected-1, 0)
	case "down", "j":
		m.chat.selected = min(m.chat.selected+1, max(len(m.slotNames())-1, 0))
	case "c":
		return m.copyMemberPrompt()
	case "r":
		return m.reissueSelected()
	case "p":
		return m.pingSelected()
	case "l":
		return m.prefillLaunch()
	case "m":
		return m.makeLeadSelected()
	case "d":
		return m.retireSelected()
	case "a":
		return m.toggleArchived(), nil
	case "i":
		m.chat.input.SetValue("/invite ")
		m.chat.input.CursorEnd()
		next, command := m.setChatFocus(focusInput)
		return next.(model).inform("Type the new member's name and press enter."), command
	case "x":
		return m.startClosing()
	case "esc":
		return m.setChatFocus(focusInput)
	}
	return m, nil
}

// chatListKey handles a key while the chat list has focus inside a conversation.
func (m model) chatListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "n":
		return m.openForm()
	case "c":
		return m.openCores()
	case "/", "a", "v", "e":
		return m.listAction(msg.String())
	case "esc":
		return m.setChatFocus(focusInput)
	case "q":
		return m, tea.Quit
	}
	return m.listNavigate(msg.String())
}

// copyMemberPrompt copies the selected member's join prompt, or asks for a new code when this
// session has none for that member.
func (m model) copyMemberPrompt() (tea.Model, tea.Cmd) {
	name := m.selectedMember()
	if name == "" {
		return m, nil
	}
	member := m.chat.view.Status.Members[name]
	if member.State != "invited" || member.LaunchState != "" {
		return m.showError(cleanLine(name) + " has no join prompt to copy."), nil
	}
	if _, found := m.knownInvitation(name); !found {
		return m.inform("Getting a new code for " + cleanLine(name) + "."), m.invite(name)
	}
	return m.copyPrompt(name)
}

// scrollTranscript moves the transcript for a scroll key and triggers auto-pagination near edges.
func (m model) scrollTranscript(key string) (model, tea.Cmd) {
	switch key {
	case "up":
		return m.scrollTranscriptBy(1)
	case "down":
		return m.scrollTranscriptBy(-1)
	case "pgup":
		return m.scrollTranscriptBy(scrollPageSize)
	case "pgdown":
		return m.scrollTranscriptBy(-scrollPageSize)
	}
	return m, nil
}

// scrollTranscriptBy shifts the transcript offset by delta lines (positive scrolls up),
// clamps it to the real scroll bounds, and auto-loads older or newer pages near the edges.
func (m model) scrollTranscriptBy(delta int) (model, tea.Cmd) {
	maxScroll := m.maxTranscriptScroll()
	m.chat.scroll = min(max(m.chat.scroll+delta, 0), maxScroll)
	if delta > 0 && m.chat.loaded && m.chat.hasOlder && !m.chat.loadingOlder &&
		maxScroll-m.chat.scroll <= autoPageThreshold {
		return m.loadOlderMessages()
	}
	if delta < 0 && m.chat.loaded && m.chat.hasNewer && !m.chat.polling &&
		m.chat.scroll <= autoPageThreshold {
		return m.pollChat()
	}
	return m, nil
}

// maxTranscriptScroll returns the largest valid scroll offset for the current viewport.
func (m model) maxTranscriptScroll() int {
	if m.chat.id == "" {
		return 0
	}
	_, center, _ := m.columnWidths()
	return max(len(m.transcriptLines(center))-m.transcriptHeight(center), 0)
}

// clampChatScroll keeps m.chat.scroll within [0, maxTranscriptScroll] so resizing or
// short chats never leave a stale non-zero offset that hides new messages.
func (m *model) clampChatScroll() {
	if m.chat.id == "" {
		return
	}
	m.chat.scroll = min(max(m.chat.scroll, 0), m.maxTranscriptScroll())
}

// startClosing starts the close of the open chat. Unfinished tasks open the alert first.
func (m model) startClosing() (tea.Model, tea.Cmd) {
	if count := m.openTaskCount(); count > 0 {
		m.taskAlert = taskAlertState{open: true, conversation: m.chat.id, count: count}
		return m, nil
	}
	return m.askCloseSummary()
}

// askCloseSummary asks for an optional summary before the conversation closes.
func (m model) askCloseSummary() (tea.Model, tea.Cmd) {
	m.chat.closing = true
	m.resizeInputs()
	m.chat.input.Blur()
	return m, m.chat.summary.Focus()
}

// closingKey handles a key while the close confirmation shows.
func (m model) closingKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.chat.closing = false
		m.chat.deprecateOnClose = false
		m.chat.summary.Blur()
		m.chat.focus = focusInput
		return m, m.chat.input.Focus()
	case "enter":
		request := CloseRequest{
			Directory: m.directory, Conversation: m.chat.id,
			Summary:        strings.TrimSpace(m.chat.summary.Value()),
			DeprecateTasks: m.chat.deprecateOnClose,
		}
		m.chat.closing = false
		m.chat.deprecateOnClose = false
		m.chat.summary.Reset()
		m.chat.summary.Blur()
		m.chat.focus = focusInput
		client := m.client
		return m.inform("Closing."), tea.Batch(m.chat.input.Focus(), func() tea.Msg {
			_, err := client.Close(request)
			return closedMsg{err: err}
		})
	}
	var command tea.Cmd
	m.chat, command = m.chat.updateInput(msg)
	return m, command
}

// onClosed reports the result of a close request and reads the summary message.
func (m model) onClosed(msg closedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	delete(m.openInvites, m.chat.id)
	m = m.showSuccess("Chat closed.")
	m, poll := m.refreshChat()
	return m, tea.Batch(poll, m.loadList())
}

// reopenChat sends an operator_reopen request for a closed conversation.
func (m model) reopenChat(conversation string, note string) (tea.Model, tea.Cmd) {
	request := ReopenRequest{
		Directory:    m.directory,
		Conversation: conversation,
		Note:         strings.TrimSpace(note),
	}
	client := m.client
	return m.inform("Reopening."), func() tea.Msg {
		_, err := client.Reopen(request)
		return reopenedMsg{conversation: conversation, err: err}
	}
}

// onReopened reports the result of a reopen request and refreshes the chat and list.
func (m model) onReopened(msg reopenedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	m = m.showSuccess("Chat reopened.")
	if m.screen == screenChat && m.chat.id == msg.conversation {
		m, poll := m.refreshChat()
		return m, tea.Batch(poll, m.loadList())
	}
	return m, m.loadList()
}

// retireSelected asks for confirmation before retiring the selected member.
func (m model) retireSelected() (tea.Model, tea.Cmd) {
	name := m.selectedMember()
	if name == "" {
		return m.inform("Select a member in the team panel first."), nil
	}
	return m.startRetiring(name)
}

// startRetiring confirms retiring one active member from an open chat.
func (m model) startRetiring(name string) (tea.Model, tea.Cmd) {
	if m.chat.loaded && !m.chat.view.Status.Open {
		return m.showError("This chat is closed."), nil
	}
	member, found := m.chat.view.Status.Members[name]
	if found && member.State == "retired" {
		return m.showError(cleanLine(name) + " is already archived."), nil
	}
	m.chat.retiring = name
	m.chat.input.Blur()
	return m, nil
}

// retiringKey handles a key while the retire confirmation shows.
func (m model) retiringKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.chat.retiring = ""
		return m, nil
	case "enter":
		name := m.chat.retiring
		m.chat.retiring = ""
		return m.inform("Retiring " + cleanLine(name) + "."), m.retire(name)
	}
	return m, nil
}

// retire sends an operator_retire request for one participant.
func (m model) retire(name string) tea.Cmd {
	request := RetireRequest{Directory: m.directory, Conversation: m.chat.id, Name: name}
	client := m.client
	return func() tea.Msg {
		result, err := client.Retire(request)
		return retiredMsg{participant: result.Participant, err: err}
	}
}

// onRetired drops any cached invitation for the retired slot and refreshes the chat.
func (m model) onRetired(msg retiredMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	delete(m.invitations[m.chat.id], foldName(msg.participant))
	m = m.showSuccess("Retired " + cleanLine(msg.participant) + ".")
	m, poll := m.refreshChat()
	return m, tea.Batch(poll, m.loadList())
}

// makeLeadSelected promotes the selected active member to lead.
func (m model) makeLeadSelected() (tea.Model, tea.Cmd) {
	if m.chat.loaded && !m.chat.view.Status.Open {
		return m.showError("This chat is closed."), nil
	}
	name := m.selectedMember()
	if name == "" {
		return m.inform("Select a member in the team panel first."), nil
	}
	member := m.chat.view.Status.Members[name]
	if member.State == "retired" {
		return m.showError(cleanLine(name) + " is archived and cannot be made lead."), nil
	}
	if member.Role == "lead" {
		return m.showError(cleanLine(name) + " is already the lead."), nil
	}
	return m.inform("Making " + cleanLine(name) + " lead."), m.setRole(name, "lead")
}

// setRole sends an operator_set_role request for one participant.
func (m model) setRole(name string, role string) tea.Cmd {
	request := SetRoleRequest{
		Directory: m.directory, Conversation: m.chat.id, Name: name, Role: role,
	}
	client := m.client
	return func() tea.Msg {
		result, err := client.SetRole(request)
		return roleChangedMsg{result: result, err: err}
	}
}

// onRoleChanged reports the updated role and immediately refreshes the chat and sidebar.
func (m model) onRoleChanged(msg roleChangedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	m = m.showSuccess(fmt.Sprintf("%s is now %s.", cleanLine(msg.result.Participant), cleanLine(msg.result.Role)))
	m, poll := m.refreshChat()
	return m, tea.Batch(poll, m.loadList())
}

// submitInput runs a slash command or posts the input line as the human.
func (m model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.chat.input.Value())
	if text == "" {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		return m.runCommand(text)
	}
	post, err := parsePost(text, m.chat.view.Status.Members)
	if err != nil {
		return m.showError(err.Error()), nil
	}
	m.chat.input.Reset()
	request := PostRequest{
		Directory: m.directory, Conversation: m.chat.id, SubmissionID: submissionID(),
		Body: post.body, Kind: post.kind, To: post.to,
	}
	client := m.client
	return m, func() tea.Msg {
		_, err := client.Post(request)
		return postedMsg{err: err}
	}
}

// onPosted shows a failed post and otherwise jumps to the tail and reads the new message at once.
func (m model) onPosted(msg postedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	m.chat.scroll = 0
	if m.chat.hasNewer {
		return m.loadTailRefresh()
	}
	return m.refreshChat()
}

// parsePost reads an optional leading ? (a question) and an optional @Name addressee that
// must name a member other than the human. Both orders work: "?@Lead text" and "@Lead ?text".
func parsePost(text string, members map[string]MemberStatus) (humanPost, error) {
	post := humanPost{kind: "note"}
	text = post.takeQuestion(text)
	if strings.HasPrefix(text, "@") {
		name, rest, err := addressee(text[1:], members)
		if err != nil {
			return humanPost{}, err
		}
		post.to = name
		text = post.takeQuestion(rest)
	}
	post.body = strings.TrimSpace(text)
	if post.body == "" {
		return humanPost{}, errors.New("type a message after the name or ?")
	}
	return post, nil
}

// takeQuestion removes a leading ? and marks the post as a question.
func (p *humanPost) takeQuestion(text string) string {
	text = strings.TrimSpace(text)
	if rest, found := strings.CutPrefix(text, "?"); found {
		p.kind = "question"
		return rest
	}
	return text
}

// addressee matches the longest member name at the start of text, so names with spaces work.
func addressee(text string, members map[string]MemberStatus) (string, string, error) {
	best := ""
	for name, member := range members {
		if member.Role == "human" || len(name) > len(text) || len(name) <= len(best) {
			continue
		}
		if !strings.EqualFold(text[:len(name)], name) {
			continue
		}
		if len(text) == len(name) || text[len(name)] == ' ' {
			best = name
		}
	}
	if best == "" {
		word, _, _ := strings.Cut(text, " ")
		return "", "", fmt.Errorf("no member named @%s in this conversation", cleanLine(word))
	}
	return best, text[len(best):], nil
}

// runCommand handles /invite, /reinvite, /launch, /relaunch, /ping, /retire, /lead, /role, and /open-invite.
func (m model) runCommand(text string) (tea.Model, tea.Cmd) {
	command, argument, _ := strings.Cut(text, " ")
	argument = strings.TrimSpace(argument)
	switch {
	case command == "/invite" && argument != "":
		m.chat.input.Reset()
		return m, m.invite(argument)
	case command == "/reinvite" && argument != "":
		m.chat.input.Reset()
		return m, m.reinvite(argument)
	case command == "/launch" && argument != "":
		cut := strings.LastIndex(argument, " ")
		if cut < 0 {
			return m.showError("Usage: /launch Name plugin"), nil
		}
		m.chat.input.Reset()
		return m, m.launch(strings.TrimSpace(argument[:cut]), argument[cut+1:])
	case command == "/relaunch" && argument != "":
		return m.runRelaunchCommand(argument)
	case command == "/ping" && argument != "":
		m.chat.input.Reset()
		return m.inform("Pinging " + cleanLine(argument) + "."), m.pingMember(argument)
	case command == "/retire" && argument != "":
		m.chat.input.Reset()
		return m.inform("Retiring " + cleanLine(argument) + "."), m.retire(argument)
	case command == "/lead" && argument != "":
		m.chat.input.Reset()
		return m.inform("Making " + cleanLine(argument) + " lead."), m.setRole(argument, "lead")
	case command == "/role" && argument != "":
		cut := strings.LastIndex(argument, " ")
		if cut < 0 {
			return m.showError("Usage: /role Name lead|co-lead|worker"), nil
		}
		name := strings.TrimSpace(argument[:cut])
		role := strings.ToLower(strings.TrimSpace(argument[cut+1:]))
		if name == "" || (role != "lead" && role != "co-lead" && role != "worker") {
			return m.showError("Usage: /role Name lead|co-lead|worker"), nil
		}
		m.chat.input.Reset()
		return m.inform(fmt.Sprintf("Setting %s as %s.", cleanLine(name), role)), m.setRole(name, role)
	case command == "/open-invite" && (argument == "" || strings.EqualFold(argument, "on")):
		m.chat.input.Reset()
		return m.inform("Enabling open invite."), m.setOpenInvite(true)
	case command == "/open-invite" && strings.EqualFold(argument, "off"):
		m.chat.input.Reset()
		return m.inform("Disabling open invite."), m.setOpenInvite(false)
	case command == "/budget" && argument != "":
		return m.runBudgetCommand(argument)
	case command == "/reopen":
		m.chat.input.Reset()
		return m.reopenChat(m.chat.id, argument)
	}
	return m.inform("Commands: /invite Name, /reinvite Name, /launch Name plugin, /relaunch Name [plugin], /ping Name, /retire Name, /lead Name, /role Name role, /budget Name tokens [model], /open-invite [off], /reopen [note]"), nil
}

// runRelaunchCommand parses "/relaunch Name [plugin]" and dispatches a relaunch.
func (m model) runRelaunchCommand(argument string) (tea.Model, tea.Cmd) {
	if matched, found := m.matchMemberName(argument); found && len(m.plugins) == 1 {
		m.chat.input.Reset()
		return m.inform("Relaunching " + cleanLine(matched) + "."), m.relaunch(matched, m.plugins[0])
	}
	cut := strings.LastIndex(argument, " ")
	if cut < 0 {
		return m.showError("Usage: /relaunch Name plugin"), nil
	}
	name := strings.TrimSpace(argument[:cut])
	plugin := strings.TrimSpace(argument[cut+1:])
	if name == "" || plugin == "" {
		return m.showError("Usage: /relaunch Name plugin"), nil
	}
	m.chat.input.Reset()
	return m.inform("Relaunching " + cleanLine(name) + "."), m.relaunch(name, plugin)
}

// matchMemberName finds a non-human member of the open chat matching candidate case-insensitively.
func (m model) matchMemberName(candidate string) (string, bool) {
	for name, member := range m.chat.view.Status.Members {
		if member.Role != "human" && strings.EqualFold(name, candidate) {
			return name, true
		}
	}
	return "", false
}

// parseTokenAmount parses a positive integer token budget, supporting optional k/m suffixes.
func parseTokenAmount(raw string) (int64, error) {
	text := strings.ToLower(strings.TrimSpace(raw))
	multiplier := int64(1)
	if strings.HasSuffix(text, "k") {
		multiplier = 1000
		text = strings.TrimSuffix(text, "k")
	} else if strings.HasSuffix(text, "m") {
		multiplier = 1000000
		text = strings.TrimSuffix(text, "m")
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value <= 0 {
		return 0, errors.New("token budget must be a positive integer (e.g. 200000 or 200k)")
	}
	total := value * multiplier
	if total <= 0 || total > 1000000000 {
		return 0, errors.New("token budget must be between 1 and 1000000000")
	}
	return total, nil
}

// runBudgetCommand parses "/budget Name <tokens> [model]" and updates the participant's budget.
func (m model) runBudgetCommand(argument string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(argument)
	if len(parts) < 2 {
		return m.inform("Usage: /budget Name tokens [model]"), nil
	}
	budgetIndex := len(parts) - 1
	modelName := ""
	tokens, err := parseTokenAmount(parts[budgetIndex])
	if err != nil && len(parts) >= 3 {
		budgetIndex = len(parts) - 2
		modelName = parts[len(parts)-1]
		tokens, err = parseTokenAmount(parts[budgetIndex])
	}
	if err != nil {
		return m.inform("Usage: /budget Name tokens [model]"), nil
	}
	name := strings.Join(parts[:budgetIndex], " ")
	if name == "" {
		return m.inform("Usage: /budget Name tokens [model]"), nil
	}
	m.chat.input.Reset()
	return m.inform(fmt.Sprintf("Updating budget for %s.", cleanLine(name))), m.setMemberBudget(name, tokens, modelName)
}

// setMemberBudget sends an operator_set_member_budget request for one participant.
func (m model) setMemberBudget(name string, budget int64, modelName string) tea.Cmd {
	request := SetMemberBudgetRequest{
		Directory:    m.directory,
		Conversation: m.chat.id,
		Name:         name,
		Model:        modelName,
		TokenBudget:  &budget,
	}
	client := m.client
	return func() tea.Msg {
		result, err := client.SetMemberBudget(request)
		return budgetUpdatedMsg{result: result, err: err}
	}
}

// onBudgetUpdated reports the updated budget and refreshes the conversation.
func (m model) onBudgetUpdated(msg budgetUpdatedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	detail := cleanLine(msg.result.Participant)
	if msg.result.TokenBudget != nil {
		detail += " budget " + formatTokens(*msg.result.TokenBudget)
	}
	if msg.result.Model != "" {
		detail += " (" + cleanLine(msg.result.Model) + ")"
	}
	m = m.showSuccess("Updated " + detail + ".")
	return m.refreshChat()
}

// setOpenInvite enables, rotates, or disables the shared room-level invite code.
func (m model) setOpenInvite(enabled bool) tea.Cmd {
	request := OpenInviteRequest{
		Directory: m.directory, Conversation: m.chat.id, Enabled: enabled,
	}
	client := m.client
	return func() tea.Msg {
		status, err := client.OpenInvite(request)
		return openInviteMsg{conversation: request.Conversation, status: status, err: err}
	}
}

// onOpenInvite updates the cached shared invite prompt and refreshes the conversation.
func (m model) onOpenInvite(msg openInviteMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	if !msg.status.OpenInvite {
		delete(m.openInvites, msg.conversation)
		if msg.conversation == m.chat.id {
			delete(m.inviteWindow.copied, openInviteCard)
			m.chat.view.Status.OpenInvite = false
			m = m.showSuccess("Open invite disabled.")
		}
		return m.refreshChat()
	}
	m.openInvites[msg.conversation] = OpenInviteInvitation{
		Code:       msg.status.Code,
		JoinPrompt: msg.status.JoinPrompt,
	}
	if msg.conversation != m.chat.id {
		return m, nil
	}
	m.chat.view.Status.OpenInvite = true
	delete(m.inviteWindow.copied, openInviteCard)
	m = m.openInvite("")
	m = m.inform("Open invite ready. Anyone can join with this prompt while the chat is open.")
	return m.refreshChat()
}

// invite adds or reissues a slot and then shows its join prompt.
func (m model) invite(name string) tea.Cmd {
	return m.sendInvite(InviteRequest{Directory: m.directory, Conversation: m.chat.id, Name: name})
}

// reinvite gives a joined or retired member a new code and then shows its join prompt.
func (m model) reinvite(name string) tea.Cmd {
	return m.sendInvite(InviteRequest{
		Directory: m.directory, Conversation: m.chat.id, Name: name, Reinvite: true,
	})
}

// sendInvite runs one invitation request and reports its result as an invitedMsg.
func (m model) sendInvite(request InviteRequest) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		invitation, err := client.Invite(request)
		return invitedMsg{conversation: request.Conversation, invitation: invitation, err: err}
	}
}

// launch fills a slot from a plugin.
func (m model) launch(name string, plugin string) tea.Cmd {
	return m.sendLaunch(name, plugin, false)
}

// relaunch starts a new fresh run for an existing slot from a plugin.
func (m model) relaunch(name string, plugin string) tea.Cmd {
	return m.sendLaunch(name, plugin, true)
}

// sendLaunch sends an operator_launch request and reports its result as a launchedMsg.
func (m model) sendLaunch(name, plugin string, relaunch bool) tea.Cmd {
	request := LaunchRequest{
		Directory: m.directory, Conversation: m.chat.id, Name: name, Plugin: plugin, Relaunch: relaunch,
	}
	client := m.client
	return func() tea.Msg {
		queued, err := client.Launch(request)
		return launchedMsg{name: name, relaunch: relaunch, queued: queued, err: err}
	}
}

// onLaunched reports the launch state, drops the slot's old join code, and refreshes the chat.
func (m model) onLaunched(msg launchedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	delete(m.invitations[m.chat.id], foldName(msg.name))
	noun := "Launch"
	if msg.relaunch {
		noun = "Relaunch"
	}
	m = m.inform(fmt.Sprintf("%s for %s is %s.", noun, cleanLine(msg.name), cleanLine(msg.queued.State)))
	m, poll := m.refreshChat()
	return m, tea.Batch(poll, m.loadList())
}

// pingSelected sends an out-of-band ping to the member selected in the team panel.
func (m model) pingSelected() (tea.Model, tea.Cmd) {
	if m.chat.loaded && !m.chat.view.Status.Open {
		return m.showError("This chat is closed."), nil
	}
	name := m.selectedMember()
	if name == "" {
		return m.inform("Select a member in the team panel first."), nil
	}
	if m.chat.view.Status.Members[name].State == "retired" {
		return m.showError(cleanLine(name) + " is archived."), nil
	}
	return m.inform("Pinging " + cleanLine(name) + "."), m.pingMember(name)
}

// pingMember sends an operator_ping request for name.
func (m model) pingMember(name string) tea.Cmd {
	request := PingRequest{Directory: m.directory, Conversation: m.chat.id, Name: name}
	client := m.client
	return func() tea.Msg {
		result, err := client.Ping(request)
		return pingedMsg{name: name, result: result, err: err}
	}
}

// onPinged displays the ping outcome and refreshes the conversation status.
func (m model) onPinged(msg pingedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	m = m.showPingNotice(msg.result)
	return m.refreshChat()
}

// showPingNotice formats one out-of-band ping result as a success or warning notice.
func (m model) showPingNotice(result PingResult) model {
	name := cleanLine(result.Participant)
	rtt := ""
	if result.PingRTTMs != nil {
		rtt = fmt.Sprintf(" (%dms)", *result.PingRTTMs)
	}
	switch result.Status {
	case wire.PingStatusWoken:
		return m.showSuccess(fmt.Sprintf("Woke %s from waiting read%s.", name, rtt))
	case wire.PingStatusConnected:
		return m.showSuccess(fmt.Sprintf("%s is connected%s, busy or between reads.", name, rtt))
	case wire.PingStatusUnresponsive:
		return m.showError(fmt.Sprintf("%s shim is open, but its MCP host did not respond to ping.", name))
	case wire.PingStatusNotJoined:
		return m.inform(fmt.Sprintf("%s has not joined yet.", name))
	default:
		detail := ""
		if result.IdleSeconds != nil {
			detail = " (last active " + idle(*result.IdleSeconds) + " ago)"
		}
		return m.inform(fmt.Sprintf("%s has no active bridge connection%s.", name, detail))
	}
}

// needsRelaunch reports whether member has a confirmed ended fresh run that can be
// relaunched from a plugin.
func needsRelaunch(member MemberStatus) bool {
	return isLaunchEnded(member.LaunchState) && member.LaunchState != "unknown"
}

// canLaunchFromPlugin reports whether member can be launched or relaunched from a plugin
// without cutting off a live manual chat or an active/unknown fresh run.
func canLaunchFromPlugin(member MemberStatus) bool {
	return (member.State == "invited" && member.LaunchState == "") || needsRelaunch(member)
}

// prefillLaunch puts a launch or relaunch command for the selected member into the message line.
func (m model) prefillLaunch() (tea.Model, tea.Cmd) {
	name := m.selectedMember()
	if name == "" {
		return m.inform("Select a member in the team panel first."), nil
	}
	return m.prefillLaunchFor(name)
}

// reissueSelected asks for a new join code for the selected member. A member who joined or
// retired is reinvited instead.
func (m model) reissueSelected() (tea.Model, tea.Cmd) {
	name := m.selectedMember()
	if name == "" {
		return m.inform("Select a member in the team panel first."), nil
	}
	if m.chat.view.Status.Members[name].State != "invited" {
		return m.inform("Reinviting " + cleanLine(name) + "."), m.reinvite(name)
	}
	return m.inform("Getting a new code for " + cleanLine(name) + "."), m.invite(name)
}

// onInvited keeps the new code in memory, shows its join prompt, and refreshes the chat.
func (m model) onInvited(msg invitedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err), nil
	}
	if m.invitations[msg.conversation] == nil {
		m.invitations[msg.conversation] = map[string]JoinInvitation{}
	}
	m.invitations[msg.conversation][foldName(msg.invitation.Participant)] = msg.invitation
	if msg.conversation != m.chat.id {
		return m, nil
	}
	if m.chat.view.Status.Members == nil {
		m.chat.view.Status.Members = map[string]MemberStatus{}
	}
	if _, exists := m.chat.view.Status.Members[msg.invitation.Participant]; !exists {
		m.chat.view.Status.Members[msg.invitation.Participant] = MemberStatus{
			Role:  msg.invitation.Role,
			State: "invited",
		}
	}
	m = m.openInvite(msg.invitation.Participant)
	delete(m.inviteWindow.copied, foldName(msg.invitation.Participant))
	m = m.inform("New code for " + cleanLine(msg.invitation.Participant) + ". It works once and expires in one hour.")
	m, poll := m.refreshChat()
	return m, tea.Batch(poll, m.loadList())
}

// slotNames returns the member names other than the human in team order.
func (m model) slotNames() []string {
	var names []string
	for _, name := range m.visibleMembers() {
		if m.chat.view.Status.Members[name].Role != "human" {
			names = append(names, name)
		}
	}
	return names
}

// visibleMembers returns the members the team panel draws, in team order. Archived members
// are left out while the ARCHIVED section is collapsed.
func (m model) visibleMembers() []string {
	active, archived := partitionMembers(m.chat.view.Status.Members)
	if m.archivedCollapsed {
		return active
	}
	return append(active, archived...)
}

// toggleArchived collapses or expands the ARCHIVED section and keeps the selected member.
func (m model) toggleArchived() model {
	selected := m.selectedMember()
	m.archivedCollapsed = !m.archivedCollapsed
	m.restoreSelection(selected)
	return m
}

// selectedMember returns the member selected in the team panel, or "" when there is none.
func (m model) selectedMember() string {
	names := m.slotNames()
	if len(names) == 0 {
		return ""
	}
	return names[min(max(m.chat.selected, 0), len(names)-1)]
}

// partitionMembers splits members into active and archived slices, each ordered by role then name.
func partitionMembers(members map[string]MemberStatus) ([]string, []string) {
	var active, archived []string
	for name, member := range members {
		if member.State == "retired" {
			archived = append(archived, name)
		} else {
			active = append(active, name)
		}
	}
	order := func(left, right string) int {
		if cmp := roleOrder[members[left].Role] - roleOrder[members[right].Role]; cmp != 0 {
			return cmp
		}
		return strings.Compare(foldName(left), foldName(right))
	}
	slices.SortFunc(active, order)
	slices.SortFunc(archived, order)
	return active, archived
}

// mainHelp returns the key hints for the bottom bar of the main screen.
func (m model) mainHelp() []hint {
	mouse := hint{"ctrl+o", m.mouseHint()}
	switch {
	case m.screen == screenHome:
		return append([]hint{{"↑↓", "choose"}, {"⏎", "open"}, {"n", "new chat"}}, append(m.listHints(), mouse, hint{"q", "quit"})...)
	case m.inviteWindow.open:
		return []hint{{"c", "copy and go to next"}, {"v", "view to select"}, {"↑↓", "select"}, {"r", "new code"}, {"esc", "done"}}
	case m.chat.closing:
		return []hint{{"⏎", "close the chat"}, {"esc", "cancel"}}
	case m.chat.retiring != "":
		return []hint{{"⏎", "retire member"}, {"esc", "cancel"}}
	case m.chat.focus == focusTeam:
		return []hint{{"↑↓", "member"}, {"p", "ping"}, {"c", "copy prompt"}, {"r", "new code"}, {"i", "invite"},
			{"l", "launch"}, {"m", "make lead"}, {"d", "retire"}, {"a", m.archivedHint()}, {"x", "close chat"}, {"tab", "chat list"}}
	case m.chat.focus == focusList:
		return append([]hint{{"↑↓", "choose"}, {"⏎", "open"}, {"n", "new chat"}}, append(m.listHints(), hint{"tab", "message"}, mouse)...)
	case m.chat.tab == tabTasks:
		return []hint{{"↑↓", "select"}, {"⏎", "jump to chat"}, {"f", "filter"}, {"pgdn", "scroll details"},
			{"ctrl+t", "chat"}, {"tab", "team"}, mouse}
	}
	return []hint{{"⏎", "send"}, {"ctrl+t", "tasks"}, {"tab", "team"}, {"↑↓", "scroll"}, {"ctrl+p", "join prompts"},
		{"ctrl+x", "close chat"}, mouse}
}

// archivedHint names what the a key does to the ARCHIVED section of the team panel.
func (m model) archivedHint() string {
	if m.archivedCollapsed {
		return "show archived"
	}
	return "hide archived"
}

// listHints returns the search, archive, rename and view hints of the chat list.
func (m model) listHints() []hint {
	if m.list.archived {
		return []hint{{"/", "search"}, {"a", "unarchive"}, {"e", "rename"}, {"v", "active chats"}, {"c", "cores"}}
	}
	return []hint{{"/", "search"}, {"a", "archive"}, {"e", "rename"}, {"v", "archived chats"}, {"c", "cores"}}
}

// mouseHint names what ctrl+o does now.
func (m model) mouseHint() string {
	if m.mouseOff {
		return "mouse on"
	}
	return "mouse off"
}

// chatView draws the header, the active tab (transcript or tasks), and the composer on Chat.
func (m model) chatView(width int, height int) []string {
	header := m.chatHeader(width)
	if m.chat.tab == tabTasks {
		contentHeight := max(height-len(header), 1)
		return append(header, m.tasksView(width, contentHeight)...)
	}
	footer := m.composer(width)
	transcriptHeight := max(height-len(header)-len(footer), 1)
	lines := append(header, m.transcriptWithScrollbar(width, transcriptHeight)...)
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	return append(lines, footer...)
}

// chatHeaderHeight returns the number of rows chatHeader occupies without rendering ANSI buttons or rules.
func (m model) chatHeaderHeight(width int) int {
	view := m.chat.view
	header := m.headerLayout(width)
	shown := header.title
	rest := view.Brief
	if first, after, found := strings.Cut(view.Brief, "\n"); found && shown == cleanLine(first) {
		rest = after
	}
	extra := 0
	if shown != strings.TrimSpace(clean(view.Brief)) && strings.TrimSpace(rest) != "" {
		extra = min(len(wrapLines(rest, width-2, " ")), maxBriefLines)
	}
	return 3 + extra
}

// chatHeader draws the title, a state badge and the start of the brief.
func (m model) chatHeader(width int) []string {
	view := m.chat.view
	header := m.headerLayout(width)
	left := " " + brightStyle.Render(header.title) + "  " + header.state
	if header.detail != "" {
		left += "  " + dimStyle.Render(header.detail)
	}
	actions := ""
	if header.actions {
		actions, _ = m.headerButtons()
	}
	gap := max(width-1-ansi.StringWidth(left)-ansi.StringWidth(actions), 1)
	lines := []string{left + strings.Repeat(" ", gap) + actions}
	shown := header.title
	// Under the title, show the part of the brief the title line does not already hold.
	rest := view.Brief
	if first, after, found := strings.Cut(view.Brief, "\n"); found && shown == cleanLine(first) {
		rest = after
	}
	if shown != strings.TrimSpace(clean(view.Brief)) && strings.TrimSpace(rest) != "" {
		brief := wrapLines(rest, width-2, " ")
		if len(brief) > maxBriefLines {
			brief = brief[:maxBriefLines]
			last := brief[maxBriefLines-1]
			brief[maxBriefLines-1] = ansi.Truncate(last, width-3, "") + "…"
		}
		for _, line := range brief {
			lines = append(lines, dimStyle.Render(line))
		}
	}
	tabs, _ := m.chatTabButtons()
	lines = append(lines, " "+tabs)
	return append(lines, rule(width))
}

// headerParts is what fits on the header line: the title as shown, the state badge, the
// detail text and whether the action buttons show.
type headerParts struct {
	title   string
	state   string
	detail  string
	actions bool
}

// minTitleWidth is the narrowest title the header keeps before it drops other parts.
const minTitleWidth = 20

// headerLayout decides what fits on the header line. The detail gives way first, then the
// action buttons, so the title stays readable.
func (m model) headerLayout(width int) headerParts {
	view := m.chat.view
	parts := headerParts{title: chatTitle(view), state: badge("OPEN", colorOK)}
	switch {
	case !m.chat.loaded:
		parts.state, parts.title = badge("LOADING", colorDim), "Loading the chat"
	case view.Archived:
		parts.state = badge("ARCHIVED", colorDim)
	case !view.Status.Open:
		parts.state = badge("CLOSED", colorDim)
	}
	count := len(m.chat.messages)
	if view.TotalMessages != nil {
		count = *view.TotalMessages
	}
	detail := fmt.Sprintf("%d messages", count)
	if view.Created != nil {
		detail = "started " + time.Unix(*view.Created, 0).Format("15:04") + " · " + detail
	}
	actions, _ := m.headerButtons()
	base := width - 6 - ansi.StringWidth(parts.state)
	actionsWidth := 0
	if actions != "" {
		actionsWidth = ansi.StringWidth(actions) + 2
	}
	room := base - actionsWidth - ansi.StringWidth(detail) - 2
	switch {
	case room >= minTitleWidth || (actions == "" && room >= 10):
		parts.detail, parts.actions = detail, actions != ""
	case base-actionsWidth >= minTitleWidth:
		room, parts.actions = base-actionsWidth, actions != ""
	default:
		room = base
	}
	parts.title = ansi.Truncate(parts.title, max(room, 10), "…")
	return parts
}

// chatTitle returns the title the core sent. A core from before titles sends none, so the
// first line of the brief stands in.
func chatTitle(view ConversationView) string {
	if view.Title != "" {
		return cleanLine(view.Title)
	}
	return cleanLine(strings.SplitN(view.Brief, "\n", 2)[0])
}

// headerButtons draws the chat actions at the right end of the header. A closed chat can
// be reopened or moved to the archive and back.
func (m model) headerButtons() (string, []span) {
	switch {
	case !m.chat.loaded:
		return "", nil
	case m.chat.view.Archived:
		return m.buttons(button{id: "reopen", label: "Reopen"}, button{id: "archive", label: "Unarchive"})
	case !m.chat.view.Status.Open:
		return m.buttons(button{id: "reopen", label: "Reopen"}, button{id: "archive", label: "Archive"})
	}
	return m.buttons(button{id: "prompts", label: "Join prompts"}, button{id: "close", label: "Close chat"})
}

// sendButton draws the button at the right end of the message line.
func (m model) sendButton() (string, []span) {
	return m.buttons(button{id: "send", label: "Send ⏎", primary: strings.TrimSpace(m.chat.input.Value()) != ""})
}

// composer draws the message line with a hint that says what enter will send.
func (m model) composer(width int) []string {
	marker := dimStyle.Render("›")
	if m.chat.focus == focusInput {
		marker = accentStyle.Render("›")
	}
	send, _ := m.sendButton()
	return []string{
		rule(width),
		pad(" "+marker+" "+m.chat.input.View(), width-1-ansi.StringWidth(send)) + send,
		"   " + dimStyle.Render(m.inputPreview()),
	}
}

// inputPreview describes what the current message line would do.
func (m model) inputPreview() string {
	text := strings.TrimSpace(m.chat.input.Value())
	if !m.chat.view.Status.Open && m.chat.loaded {
		return "This chat is closed. Click Reopen or type /reopen to continue it."
	}
	switch {
	case text == "":
		return "⏎ send · @ address one member · ? ask a question"
	case strings.HasPrefix(text, "/"):
		return "command · /invite Name · /launch Name plugin · /lead Name · /role Name role · /retire Name"
	}
	post, err := parsePost(text, m.chat.view.Status.Members)
	if err != nil {
		return err.Error()
	}
	target := "everyone"
	if post.to != "" {
		target = cleanLine(post.to)
	}
	return post.kind + " to " + target + " · ⏎ send"
}

// transcriptLines returns the cached wrapped lines for the loaded messages and any top banner.
// Per-message lines are retained across pagination and incoming posts so only newly loaded
// messages are wrapped.
func (m model) transcriptLines(width int) []string {
	cache := m.chat.linesCache
	if cache != nil && cache.valid &&
		cache.width == width &&
		cache.highlight == m.chat.highlight &&
		cache.version == m.chat.version &&
		cache.hasOlder == m.chat.hasOlder &&
		cache.loadingOlder == m.chat.loadingOlder {
		return cache.lines
	}
	var perMessage map[int64]cachedMessageLines
	if cache != nil && cache.width == width && cache.highlight == m.chat.highlight && cache.perMessage != nil {
		perMessage = cache.perMessage
	} else {
		perMessage = make(map[int64]cachedMessageLines, len(m.chat.messages))
	}
	members := m.chat.view.Status.Members
	var lines []string
	if m.chat.hasOlder {
		label := "  ↑ Scroll up for older messages"
		if m.chat.loadingOlder {
			label = "  ↑ Loading older messages…"
		}
		lines = append(lines, dimStyle.Render(label))
	}
	for _, message := range m.chat.messages {
		role := members[message.Label].Role
		cached, ok := perMessage[message.ID]
		if !ok || cached.role != role {
			cached = cachedMessageLines{
				role:  role,
				lines: singleMessageLines(message, members, width, m.chat.highlight),
			}
			perMessage[message.ID] = cached
		}
		lines = append(lines, cached.lines...)
	}
	if cache != nil {
		*cache = transcriptLinesCache{
			valid:        true,
			width:        width,
			highlight:    m.chat.highlight,
			version:      m.chat.version,
			hasOlder:     m.chat.hasOlder,
			loadingOlder: m.chat.loadingOlder,
			lines:        lines,
			perMessage:   perMessage,
		}
	}
	return lines
}

// visibleTranscript returns the transcript lines that fit, honoring the scroll offset.
func (m model) visibleTranscript(width int, height int) []string {
	lines := m.transcriptLines(width)
	if len(lines) == 0 && m.chat.loaded {
		lines = []string{"", " " + dimStyle.Render("No messages yet. The lead posts tasks here once it joins.")}
	}
	hidden := min(m.chat.scroll, max(len(lines)-height, 0))
	end := len(lines) - hidden
	start := max(end-height, 0)
	return lines[start:end]
}

// transcriptWithScrollbar returns the visible transcript with a scrollbar in the last
// column. A transcript that fits the view has no scrollbar.
func (m model) transcriptWithScrollbar(width int, height int) []string {
	visible := m.visibleTranscript(width, height)
	total := len(m.transcriptLines(width))
	if total <= height || width < 2 {
		return visible
	}
	hidden := min(m.chat.scroll, total-height)
	bar := scrollbar(total, height, total-height-hidden)
	out := make([]string, height)
	for row := range out {
		line := ""
		if row < len(visible) {
			line = visible[row]
		}
		out[row] = pad(line, width-1) + bar[row]
	}
	return out
}

// scrollbar draws a vertical bar of height rows for a view that shows rows
// [top, top+height) of total rows.
func scrollbar(total int, height int, top int) []string {
	thumbSize := max(height*height/total, 1)
	thumbTop := 0
	if hiddenRows := total - height; hiddenRows > 0 {
		thumbTop = (top*(height-thumbSize) + hiddenRows/2) / hiddenRows
	}
	bar := make([]string, height)
	for row := range bar {
		if row >= thumbTop && row < thumbTop+thumbSize {
			bar[row] = dimStyle.Render("┃")
		} else {
			bar[row] = borderStyle.Render("│")
		}
	}
	return bar
}

// singleMessageLines renders one message as a header line, wrapped body lines, and a trailing blank line.
func singleMessageLines(message TranscriptMessage, members map[string]MemberStatus, width int, highlight int64) []string {
	role := members[message.Label].Role
	author := cleanLine(message.Label)
	if role == "human" {
		author = "You"
	}
	header := " " + dimStyle.Render(time.Unix(message.Created, 0).Format("15:04")) + "  " +
		roleStyle(role).Render(author)
	switch {
	case message.ReplyTo != nil:
		header += dimStyle.Render(fmt.Sprintf(" ↳ re #%d", *message.ReplyTo))
	case message.To != "":
		header += dimStyle.Render(" → " + cleanLine(message.To))
	case role == "human":
		header += dimStyle.Render(" to everyone")
	}
	if label := kindLabel(message); label != "" {
		header += "  " + label
	}
	body := wrapLines(message.Body, width-1, messageIndent)
	lines := make([]string, 0, len(body)+2)
	switch {
	case message.ID == highlight:
		lines = append(lines, fill(accentStyle.Render("▎")+header[1:], width, colorSelect))
		for _, line := range body {
			lines = append(lines, fill(textStyle.Render(line), width, colorSelect))
		}
	case role == "human":
		lines = append(lines, fill(header, width, colorBand))
		for _, line := range body {
			lines = append(lines, fill(textStyle.Render(line), width, colorBand))
		}
	default:
		lines = append(lines, header)
		for _, line := range body {
			lines = append(lines, textStyle.Render(line))
		}
	}
	return append(lines, "")
}

// messageLines draws each message as a header line and its wrapped body, with a blank line
// between messages. The human's messages sit on a band, and the highlighted message on the
// selection color with a marker. Highlighting never changes the number of lines.
func messageLines(messages []TranscriptMessage, members map[string]MemberStatus, width int, highlight int64) []string {
	var lines []string
	for _, message := range messages {
		lines = append(lines, singleMessageLines(message, members, width, highlight)...)
	}
	return lines
}

// kindLabel draws the tag for a message kind. Plain notes have none.
func kindLabel(message TranscriptMessage) string {
	kind := cleanLine(message.Kind)
	switch kind {
	case "", "note":
		return ""
	case "task", "handoff":
		return tag(fmt.Sprintf("%s #%d", strings.ToUpper(kind), message.ID), colorAccent)
	case "blocker":
		return badge("BLOCKED", colorWarn)
	case "done", "accept":
		return badge(strings.ToUpper(kind), colorOK)
	}
	return tag(strings.ToUpper(kind), colorText)
}

// formatTokens shortens large token counts into compact k/M labels.
func formatTokens(count int64) string {
	switch {
	case count >= 1000000:
		return fmt.Sprintf("%.1fM", float64(count)/1000000.0)
	case count >= 1000:
		return fmt.Sprintf("%.1fk", float64(count)/1000.0)
	default:
		return fmt.Sprintf("%d", count)
	}
}

// usageBar renders an 8-cell visual meter for a percentage consumed.
func usageBar(usedPercent float64) string {
	clamped := max(0.0, min(100.0, usedPercent))
	filled := int(math.Round(clamped / 100.0 * 8.0))
	return strings.Repeat("▓", filled) + strings.Repeat("░", 8-filled)
}

// providerForModel returns a recognizable provider prefix for a model identifier, or "".
func providerForModel(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(lower, "claude"),
		strings.HasPrefix(lower, "opus"),
		strings.HasPrefix(lower, "sonnet"),
		strings.HasPrefix(lower, "haiku"),
		strings.HasPrefix(lower, "fable"):
		return "anthropic"
	case strings.HasPrefix(lower, "gemini"),
		strings.HasPrefix(lower, "neon"),
		strings.HasPrefix(lower, "argon"):
		return "google"
	case strings.HasPrefix(lower, "glm"):
		return "zai"
	case strings.HasPrefix(lower, "grok"):
		return "xai"
	case strings.HasPrefix(lower, "gpt"),
		strings.HasPrefix(lower, "codex"),
		strings.HasPrefix(lower, "o1"),
		strings.HasPrefix(lower, "o3"),
		strings.HasPrefix(lower, "o4"):
		return "openai"
	case strings.HasPrefix(lower, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(lower, "qwen"):
		return "alibaba"
	case strings.HasPrefix(lower, "mistral"),
		strings.HasPrefix(lower, "codestral"):
		return "mistral"
	}
	return ""
}

// formatProviderModel formats a declared model string with its provider prefix (e.g. "anthropic/claude-sonnet-4-6").
func formatProviderModel(raw string) string {
	cleaned := strings.TrimSpace(cleanLine(raw))
	if cleaned == "" {
		return ""
	}
	if strings.Contains(cleaned, "/") {
		return cleaned
	}
	if provider := providerForModel(cleaned); provider != "" {
		return provider + "/" + cleaned
	}
	return cleaned
}

// modelVariantTokens lists recognized model variant words that refine a family name in a slot label.
var modelVariantTokens = map[string]bool{
	"opus": true, "sonnet": true, "haiku": true, "fable": true,
	"flash": true, "pro": true, "ultra": true, "nano": true,
	"neon": true, "argon": true, "mini": true, "codex": true,
}

// inferProviderModelFromName infers a "provider/model" label when a participant's name identifies a known model family.
func inferProviderModelFromName(name string) string {
	words := strings.FieldsFunc(strings.ToLower(cleanLine(name)), func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '/'
	})
	for index, word := range words {
		provider := providerForModel(word)
		if provider == "" {
			continue
		}
		family := word
		switch word {
		case "opus", "sonnet", "haiku", "fable":
			family = "claude-" + word
		case "neon", "argon":
			family = "gemini-" + word
		}
		if index+1 < len(words) && modelVariantTokens[words[index+1]] && !strings.HasSuffix(family, words[index+1]) {
			family += "-" + words[index+1]
		}
		return provider + "/" + family
	}
	return ""
}

// memberProviderModel returns the declared or inferred "provider/model" context for a non-human member.
func memberProviderModel(name string, member MemberStatus) string {
	if member.Role == "human" {
		return ""
	}
	if member.Usage != nil && member.Usage.Model != "" {
		return formatProviderModel(member.Usage.Model)
	}
	return inferProviderModelFromName(name)
}

// memberUsageLine formats one compact budget or token-spend line for the team panel.
func memberUsageLine(usage *MemberUsage) string {
	if usage == nil {
		return ""
	}
	if usage.UsedPercent != nil && usage.RemainingTokens != nil {
		bar := usageBar(*usage.UsedPercent)
		text := fmt.Sprintf("%s %.0f%% · %s left", bar, *usage.UsedPercent, formatTokens(*usage.RemainingTokens))
		if usage.Status == "warning" || usage.Status == "exhausted" {
			return warnStyle.Render(text)
		}
		return okStyle.Render(text)
	}
	if usage.ActualTokens == nil {
		return dimStyle.Render(fmt.Sprintf("%s tok est", formatTokens(usage.EstimatedTokens)))
	}
	return dimStyle.Render(fmt.Sprintf("%s tok · est %s", formatTokens(*usage.ActualTokens), formatTokens(usage.EstimatedTokens)))
}

// conversationUsageLines formats the whole chat's token spend for the team panel footer.
func conversationUsageLines(usage *ConversationUsage) []string {
	if usage == nil {
		return nil
	}
	detail := fmt.Sprintf("%s actual", formatTokens(usage.ActualTokens))
	if usage.EstimatedMembers > 0 {
		detail += fmt.Sprintf(" · %s est (%d unreported)", formatTokens(usage.EstimatedTokens), usage.EstimatedMembers)
	}
	return []string{
		" " + headStyle.Render("CHAT TOKENS") + " " + formatTokens(usage.TotalTokens),
		" " + dimStyle.Render(detail),
		"",
	}
}

// memberBlock renders one member's lines with selection or hover highlight.
func (m model) memberBlock(name string, member MemberStatus, selected string, width int) []string {
	chatOpen := !m.chat.loaded || m.chat.view.Status.Open
	glyph, detail := memberStateForChat(member, chatOpen)
	label := cleanLine(name)
	if member.Role == "human" {
		label = "You"
	}
	styledName := roleStyle(member.Role).Render(label)
	if member.State == "retired" {
		styledName = dimStyle.Render(label)
	}
	block := []string{glyph + " " + styledName + " " + dimStyle.Render(member.Role)}
	if providerModel := memberProviderModel(name, member); providerModel != "" {
		block = append(block, "  "+dimStyle.Render(ansi.Truncate(providerModel, max(width-3, 1), "…")))
	}
	block = append(block, "  "+dimStyle.Render(detail))
	if usageLine := memberUsageLine(member.Usage); usageLine != "" {
		block = append(block, "  "+usageLine)
	}
	if chatOpen && member.State == "invited" && member.LaunchState == "" {
		copyButton, _ := m.buttons(button{id: "copy:" + name, label: "Copy join prompt"})
		block = append(block, "  "+copyButton)
	}
	switch {
	case name == selected && m.chat.focus == focusTeam:
		for index, line := range block {
			block[index] = fill(accentStyle.Render("▎")+line, width, colorSelect)
		}
	case m.hover == "member:"+name:
		for index, line := range block {
			block[index] = fill(dimStyle.Render("▎")+line, width, colorBand)
		}
	default:
		for index, line := range block {
			block[index] = " " + line
		}
	}
	return block
}

// memberTasks formats open task lines for one member.
func (m model) memberTasks(label string, member MemberStatus, width int) []string {
	var tasks []string
	for _, task := range member.OpenTasks {
		state := dimStyle.Render("open")
		if member.State == "retired" {
			state = dimStyle.Render("archived")
		} else if isLaunchEnded(member.LaunchState) {
			state = dimStyle.Render("run " + strings.ReplaceAll(member.LaunchState, "_", " "))
		} else if task.Blocked != nil && *task.Blocked {
			state = warnStyle.Render("blocked")
		} else if member.State == "invited" {
			state = dimStyle.Render("waiting to join")
		} else if task.Overdue {
			state = warnStyle.Render("overdue")
		}
		line := " " + accentStyle.Render(fmt.Sprintf("#%d", task.ID)) + " " + label + " " + state
		if m.hover == fmt.Sprintf("team-task:%d", task.ID) {
			line = fill(accentStyle.Render("▎")+line[1:], width, colorBand)
		}
		tasks = append(tasks, line)
	}
	return tasks
}

// archivedHoverID is the hover id of the ARCHIVED section header in the team panel.
const archivedHoverID = "team-archived"

// archivedHeader draws the clickable ARCHIVED section header with its collapse marker.
func (m model) archivedHeader(count int, width int) string {
	title := "▾ ARCHIVED"
	if m.archivedCollapsed {
		title = fmt.Sprintf("▸ ARCHIVED (%d)", count)
	}
	if m.hover == archivedHoverID {
		return fill(dimStyle.Render("▎")+headStyle.Render(title), width, colorBand)
	}
	return " " + headStyle.Render(title)
}

// teamView draws active members, a collapsible ARCHIVED section for retired members, and
// open tasks.
func (m model) teamView(width int, height int) []string {
	members := m.chat.view.Status.Members
	active, archived := partitionMembers(members)
	lines := []string{" " + headStyle.Render("TEAM"), ""}
	selected := m.selectedMember()
	var tasks []string
	appendGroup := func(names []string, drawMembers bool) {
		for _, name := range names {
			member := members[name]
			if drawMembers {
				lines = append(lines, m.memberBlock(name, member, selected, width)...)
				lines = append(lines, "")
			}
			label := cleanLine(name)
			if member.Role == "human" {
				label = "You"
			}
			tasks = append(tasks, m.memberTasks(label, member, width)...)
		}
	}
	appendGroup(active, true)
	if len(archived) > 0 {
		lines = append(lines, m.archivedHeader(len(archived), width), "")
		appendGroup(archived, !m.archivedCollapsed)
	}
	footer := conversationUsageLines(m.chat.view.Status.TokenUsage)
	if balance := m.chat.view.Status.TokenBalance; balance != nil && !balance.Balanced {
		summary := fmt.Sprintf("gap %.0f%%", balance.ImbalanceGapPercent)
		if len(balance.Preferred) > 0 {
			summary += " · prefer " + cleanLine(balance.Preferred[0])
		}
		footer = append(footer, " "+warnStyle.Render("▲ TOKEN BALANCE"), " "+dimStyle.Render(summary), "")
	}
	if len(tasks) > 0 {
		footer = append(footer, " "+headStyle.Render("OPEN TASKS"))
		footer = append(footer, tasks...)
	}
	if m.chat.view.Status.Open {
		invite, _ := m.buttons(button{id: "invite-member", label: "+ Invite member"})
		footer = append(footer, "", " "+invite)
	}
	for len(lines)+len(footer) < height {
		lines = append(lines, "")
	}
	return append(lines, footer...)
}

// teamRow places one member block in the team panel.
type teamRow struct {
	name string
	top  int
	size int
	// copyRow is the row of the "copy join prompt" line, or -1.
	copyRow int
}

// teamRows returns where teamView draws each member, so a click can find the member.
func (m model) teamRows() []teamRow {
	rows, _ := m.teamLayout()
	return rows
}

// teamLayout returns where teamView draws each member and the row of the ARCHIVED section
// header, or -1 when no member is archived.
func (m model) teamLayout() ([]teamRow, int) {
	members := m.chat.view.Status.Members
	chatOpen := !m.chat.loaded || m.chat.view.Status.Open
	active, archived := partitionMembers(members)
	var rows []teamRow
	top := 2
	addGroup := func(names []string) {
		for _, name := range names {
			member := members[name]
			size := 2
			if memberProviderModel(name, member) != "" {
				size++
			}
			if member.Usage != nil {
				size++
			}
			row := teamRow{name: name, top: top, size: size, copyRow: -1}
			if chatOpen && member.State == "invited" && member.LaunchState == "" {
				row.copyRow = top + size
				row.size++
			}
			rows = append(rows, row)
			top += row.size + 1
		}
	}
	addGroup(active)
	if len(archived) == 0 {
		return rows, -1
	}
	headerRow := top
	if !m.archivedCollapsed {
		top += 2
		addGroup(archived)
	}
	return rows, headerRow
}

// teamTaskRow places one open task line in the team panel footer.
type teamTaskRow struct {
	id  int64
	row int
}

// teamTaskRows returns the body row of each open task drawn in teamView.
func (m model) teamTaskRows(height int) []teamTaskRow {
	members := m.chat.view.Status.Members
	active, archived := partitionMembers(members)
	var taskIDs []int64
	for _, name := range append(active, archived...) {
		for _, task := range members[name].OpenTasks {
			taskIDs = append(taskIDs, task.ID)
		}
	}
	if len(taskIDs) == 0 {
		return nil
	}
	teamRows, archivedHeaderRow := m.teamLayout()
	memberLines := max(2, archivedHeaderRow+2)
	for _, row := range teamRows {
		memberLines = max(memberLines, row.top+row.size+1)
	}
	footerLen := 1 + len(taskIDs)
	tasksOffset := 1
	if balance := m.chat.view.Status.TokenBalance; balance != nil && !balance.Balanced {
		footerLen += 3
		tasksOffset += 3
	}
	if m.chat.view.Status.Open {
		footerLen += 2
	}
	footerStart := max(height-footerLen, memberLines)
	rows := make([]teamTaskRow, 0, len(taskIDs))
	for index, id := range taskIDs {
		rows = append(rows, teamTaskRow{id: id, row: footerStart + tasksOffset + index})
	}
	return rows
}

// isLaunchEnded reports whether a fresh-run assignment is no longer active.
func isLaunchEnded(launchState string) bool {
	return launchState != "" &&
		launchState != "queued" &&
		launchState != "starting" &&
		launchState != "running"
}

// memberState returns a glyph that reads without color and a one-line detail for an open chat.
func memberState(member MemberStatus) (string, string) {
	return memberStateForChat(member, true)
}

// memberStateForChat returns a glyph and one-line detail reflecting current launch and conversation state.
func memberStateForChat(member MemberStatus, chatOpen bool) (string, string) {
	blockedTask := int64(0)
	for _, task := range member.OpenTasks {
		if task.Blocked != nil && *task.Blocked {
			blockedTask = task.ID
		}
	}
	switch {
	case member.Role == "human":
		return roleStyle("human").Render("◆"), "you, through this window"
	case member.State == "retired":
		return dimStyle.Render("✕"), "archived"
	case isLaunchEnded(member.LaunchState):
		if member.LaunchState == "finished" {
			if member.State == "done" {
				return dimStyle.Render("○"), "done"
			}
			if member.State == "invited" {
				return dimStyle.Render("✕"), "launch finished"
			}
			return dimStyle.Render("○"), "run finished"
		}
		return dimStyle.Render("✕"), "launch " + strings.ReplaceAll(member.LaunchState, "_", " ")
	case !chatOpen:
		if member.State == "invited" {
			return dimStyle.Render("◌"), "never joined"
		}
		if member.State == "done" {
			return dimStyle.Render("○"), "done"
		}
		return dimStyle.Render("○"), member.State
	case blockedTask != 0:
		return warnStyle.Render("▲"), fmt.Sprintf("blocked on task #%d", blockedTask)
	case member.State == "blocked":
		return warnStyle.Render("▲"), "blocked"
	case member.State == "invited" && member.LaunchState != "":
		return accentStyle.Render("◐"), "plugin " + member.LaunchState
	case member.State == "invited":
		return dimStyle.Render("◌"), "not joined yet"
	}
	detail := member.State
	if len(member.OpenTasks) == 1 {
		detail = fmt.Sprintf("working on #%d", member.OpenTasks[0].ID)
	} else if len(member.OpenTasks) > 1 {
		detail = fmt.Sprintf("working on %d tasks", len(member.OpenTasks))
	} else if member.State == "done" {
		if member.Listening {
			return okStyle.Render("●"), "done · listening"
		}
		return dimStyle.Render("○"), "done"
	}
	if member.Listening {
		detail += " · listening"
	} else if member.Connected {
		detail += " · connected"
	}
	if member.IdleSeconds != nil {
		detail += " · active " + idle(*member.IdleSeconds) + " ago"
		if !member.Connected && !member.Listening && *member.IdleSeconds >= staleMemberIdleSeconds {
			return dimStyle.Render("●"), detail
		}
	}
	return okStyle.Render("●"), detail
}
