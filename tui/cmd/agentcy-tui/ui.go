package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// screen names the view that receives keys.
type screen int

const (
	// screenHome shows the chat list with no conversation open.
	screenHome screen = iota
	screenNew
	screenChat
	// screenSearch shows the search box and its results in the center.
	screenSearch
)

const (
	sidebarWidth = 30
	teamWidth    = 32
	// Dragging a divider keeps each side column within these widths and the center wide
	// enough to read.
	minSideWidth   = 18
	maxSideWidth   = 60
	minCenterWidth = 40
	// Below these widths the side columns hide so the conversation keeps its room.
	sidebarMinWidth = 72
	teamMinWidth    = 104
)

const (
	// listRefreshTicks spaces list refreshes. Any request also keeps an idle core alive.
	listRefreshTicks = 2
	// statusDurationTicks clears transient notices so the top bar returns to live core status.
	statusDurationTicks = 6
	pollInterval        = time.Second
	offlineStatus       = "Core not reachable. Retrying every second. Run agentcy ui to start it again."
)

// showMode describes which page of the transcript a Show request asked for.
type showMode int

const (
	showAfter showMode = iota
	showTail
	showOlder
	showStart
	showJump
	showStatus
)

// Messages that commands send back to Update.
type (
	tickMsg    time.Time
	pluginsMsg struct {
		plugins PluginList
		err     error
	}
	listMsg struct {
		archived bool
		list     ConversationList
		err      error
	}
	showMsg struct {
		conversation string
		mode         showMode
		after        int64
		before       int64
		view         ConversationView
		err          error
	}
	createdMsg struct {
		created       CreatedConversation
		pluginSlots   []string
		launchFailure []string
		err           error
	}
	invitedMsg struct {
		conversation string
		invitation   JoinInvitation
		err          error
	}
	openInviteMsg struct {
		conversation string
		status       OpenInviteStatus
		err          error
	}
	launchedMsg struct {
		name     string
		relaunch bool
		queued   LaunchQueued
		err      error
	}
	pingedMsg struct {
		name   string
		result PingResult
		err    error
	}
	retiredMsg struct {
		participant string
		err         error
	}
	roleChangedMsg struct {
		result ParticipantRoleChanged
		err    error
	}
	budgetUpdatedMsg struct {
		result MemberBudgetUpdated
		err    error
	}
	postedMsg   struct{ err error }
	closedMsg   struct{ err error }
	reopenedMsg struct {
		conversation string
		err          error
	}
	archivedMsg struct {
		conversation string
		archived     bool
		err          error
	}
	searchedMsg struct {
		query   string
		results SearchResults
		err     error
	}
	copiedMsg struct {
		name string
		err  error
	}
)

// model is the whole UI state. Bubble Tea passes it by value through Update.
type model struct {
	client       Operator
	directory    string
	clipboard    func(string) error
	now          func() time.Time
	width        int
	height       int
	screen       screen
	status       string
	statusTicks  int
	offline      bool
	ticks        int
	plugins      []string
	list         listState
	form         formState
	chat         chatState
	search       searchState
	inviteWindow inviteState
	contextMenu  contextMenuState
	rename       renameState
	// cores is the window that lists and stops the running agentcy cores.
	cores coresState
	// processes finds and stops core processes; dataDir is the data directory of this UI's
	// core, used to mark it in the cores window.
	processes CoreProcesses
	dataDir   string
	// popup is the error or success notice drawn over the screen.
	popup popupState
	// taskAlert stops a close or an archive while tasks are unfinished.
	taskAlert taskAlertState
	// mouseOff hands the mouse back to the terminal so its own text selection works.
	mouseOff bool
	// sidebarCols and teamCols are the side column widths, changed by dragging a divider.
	sidebarCols int
	teamCols    int
	dragging    divider
	// archivedCollapsed hides the archived members of the team panel under their header.
	archivedCollapsed bool
	// hover is the id of the clickable part under the pointer, or "".
	hover string
	// invitations holds the join codes this UI session received, by conversation and
	// folded participant name. Codes stay in memory and appear only in the invite window.
	invitations map[string]map[string]JoinInvitation
	// openInvites holds reusable room-level join prompts this session received, by conversation.
	openInvites map[string]OpenInviteInvitation
}

// newModel returns the UI on the chat list for one checkout directory.
func newModel(client Operator, directory string) model {
	return model{
		client:            client,
		directory:         directory,
		clipboard:         systemClipboard(),
		now:               time.Now,
		width:             80,
		height:            24,
		sidebarCols:       sidebarWidth,
		teamCols:          teamWidth,
		archivedCollapsed: true,
		search:            newSearch(),
		processes:         systemCores{},
		invitations:       map[string]map[string]JoinInvitation{},
		openInvites:       map[string]OpenInviteInvitation{},
	}
}

// Init loads plugins and the chat list and starts the poll clock.
func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadPlugins(), m.loadList(), tick())
}

// Update routes one message to the handler for its type.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeInputs()
		m.clampChatScroll()
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+o":
			return m.toggleMouse(), nil
		}
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		// A click dismisses an error and stops there; a success notice lets the click through.
		dismissedError := m.popup.kind == popupError
		m.popup = popupState{}
		if dismissedError {
			return m, nil
		}
		return m.onClick(msg.Mouse())
	case tea.MouseMotionMsg:
		if m.dragging != noDivider {
			return m.onDrag(msg.Mouse()), nil
		}
		m.hover = m.hoverAt(msg.Mouse().X, msg.Mouse().Y)
		return m, nil
	case tea.MouseReleaseMsg:
		m.dragging = noDivider
		return m, nil
	case tea.MouseWheelMsg:
		return m.onWheel(msg.Mouse())
	case tickMsg:
		return m.onTick()
	case pluginsMsg:
		return m.onPlugins(msg), nil
	case listMsg:
		return m.onList(msg), nil
	case showMsg:
		return m.onShow(msg)
	case tasksMsg:
		return m.onTasks(msg), nil
	case createdMsg:
		return m.onCreated(msg)
	case invitedMsg:
		return m.onInvited(msg)
	case openInviteMsg:
		return m.onOpenInvite(msg)
	case launchedMsg:
		return m.onLaunched(msg)
	case pingedMsg:
		return m.onPinged(msg)
	case retiredMsg:
		return m.onRetired(msg)
	case roleChangedMsg:
		return m.onRoleChanged(msg)
	case budgetUpdatedMsg:
		return m.onBudgetUpdated(msg)
	case postedMsg:
		return m.onPosted(msg)
	case closedMsg:
		return m.onClosed(msg)
	case reopenedMsg:
		return m.onReopened(msg)
	case archiveCheckMsg:
		return m.onArchiveCheck(msg)
	case archivedMsg:
		return m.onArchived(msg)
	case renamedMsg:
		return m.onRenamed(msg)
	case searchedMsg:
		return m.onSearched(msg), nil
	case coresMsg:
		return m.onCores(msg), nil
	case coreStoppedMsg:
		return m.onCoreStopped(msg)
	case copiedMsg:
		return m.onCopied(msg), nil
	case textCopiedMsg:
		return m.onTextCopied(msg), nil
	}
	return m.updateFocusedInput(msg)
}

// handleKey sends a key press to the active screen.
func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.popup.kind != popupNone {
		return m.popupKey(msg)
	}
	if m.cores.open {
		return m.coresKey(msg)
	}
	if m.taskAlert.open {
		return m.taskAlertKey(msg)
	}
	if m.contextMenu.open {
		return m.contextMenuKey(msg)
	}
	if m.rename.open {
		return m.renameKey(msg)
	}
	switch m.screen {
	case screenNew:
		return m.formKey(msg)
	case screenSearch:
		return m.searchKey(msg)
	case screenChat:
		if m.inviteWindow.open {
			return m.inviteKey(msg)
		}
		return m.chatKey(msg)
	default:
		return m.homeKey(msg)
	}
}

// updateFocusedInput forwards other messages, such as cursor blinks and pastes, to the
// text field that has focus.
func (m model) updateFocusedInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var command tea.Cmd
	if m.cores.open {
		return m, nil
	}
	if m.rename.open {
		m.rename.input, command = m.rename.input.Update(msg)
		return m, command
	}
	switch m.screen {
	case screenNew:
		m.form, command = m.form.updateFocused(msg)
	case screenChat:
		m.chat, command = m.chat.updateInput(msg)
	case screenSearch:
		m.search.input, command = m.search.input.Update(msg)
	}
	return m, command
}

// onTick polls the open conversation, expires transient notices, and refreshes the list.
func (m model) onTick() (tea.Model, tea.Cmd) {
	m.ticks++
	m = m.expirePopup()
	if m.statusTicks > 0 {
		m.statusTicks--
		if m.statusTicks == 0 && !m.offline {
			m.status = ""
		}
	}
	commands := []tea.Cmd{tick()}
	if m.chat.id != "" && m.screen == screenChat {
		var poll tea.Cmd
		switch {
		case !m.chat.loaded:
			m, poll = m.loadTail()
		case m.chat.hasNewer:
			m, poll = m.pollStatus()
		default:
			m, poll = m.pollChat()
		}
		commands = append(commands, poll)
		if m.chat.tab == tabTasks {
			var pollTasks tea.Cmd
			m, pollTasks = m.loadTasks()
			commands = append(commands, pollTasks)
		}
	}
	if m.ticks%listRefreshTicks == 0 {
		commands = append(commands, m.loadList())
	}
	return m, tea.Batch(commands...)
}

// onPlugins stores the configured plugin names for the source pickers.
func (m model) onPlugins(msg pluginsMsg) model {
	if msg.err != nil {
		return m.fail(msg.err)
	}
	m.plugins = nil
	for _, plugin := range msg.plugins.Plugins {
		m.plugins = append(m.plugins, plugin.Name)
	}
	return m.succeed()
}

// fail shows an error in a popup. A lost core shows in the top bar instead, because the
// poll retries every second. The program keeps running.
func (m model) fail(err error) model {
	if errors.Is(err, errCoreUnreachable) {
		m.offline = true
		m.status = offlineStatus
		m.statusTicks = 0
		return m
	}
	return m.showError(err.Error())
}

// succeed clears the offline notice after the core answers again.
func (m model) succeed() model {
	if m.offline {
		m.offline = false
		m.status = ""
		m.statusTicks = 0
	}
	return m
}

// inform shows a transient notice in the status line.
func (m model) inform(text string) model {
	m.status = text
	m.statusTicks = statusDurationTicks
	return m
}

// resizeInputs fits the text fields that exist to the terminal size. The form and the chat
// input are created when their screen first opens.
func (m *model) resizeInputs() {
	if m.screen == screenNew {
		m.form.resize(m.width, m.height)
	}
	_, center, _ := m.columnWidths()
	if m.chat.id != "" {
		m.chat.resize(center)
		// The summary field sits in the close dialog after its "Summary " label and a cursor.
		m.chat.summary.SetWidth(max(popupWidth(m.width, popupMaxWidth)-4-len("Summary ")-1, 10))
	}
	m.search.input.SetWidth(max(center-6, 10))
	if m.rename.open {
		m.rename.input.SetWidth(max(popupWidth(m.width, popupMaxWidth)-4-len("Title ")-1, 10))
	}
}

// columnWidths returns the widths of the chat list, the center and the team panel. A side
// column that does not fit has width zero.
func (m model) columnWidths() (int, int, int) {
	list, team := 0, 0
	if m.screen != screenNew && m.width >= sidebarMinWidth {
		list = m.sidebarCols
	}
	if m.screen == screenChat && m.width >= teamMinWidth {
		team = m.teamCols
	}
	center := m.width
	for _, side := range []int{list, team} {
		if side > 0 {
			center -= side + 1
		}
	}
	return list, max(center, 1), team
}

// View draws a top bar, the active screen and a key bar at the bottom.
func (m model) View() tea.View {
	if m.screen == screenChat && m.inviteWindow.open && m.inviteWindow.viewing {
		return m.promptView()
	}
	bodyHeight := max(m.height-2, 1)
	var body []string
	var help []hint
	switch m.screen {
	case screenNew:
		body, help = column(m.formView(bodyHeight), m.width, bodyHeight), m.formHelp()
	case screenSearch:
		body, help = m.mainView(bodyHeight), m.searchHelp()
	default:
		body, help = m.mainView(bodyHeight), m.mainHelp()
	}
	if m.screen == screenChat && (m.chat.closing || m.chat.retiring != "") {
		body = m.drawConfirm(body)
	}
	if m.rename.open {
		body = m.drawRename(body)
		help = []hint{{"⏎", "rename"}, {"esc", "cancel"}}
	}
	if m.cores.open {
		body = m.drawCores(body)
		help = m.coresHelp()
	}
	if m.contextMenu.open {
		body = m.drawContextMenu(body)
		help = m.contextMenuHelp()
	}
	if m.taskAlert.open {
		body = m.drawTaskAlert(body)
		help = m.taskAlertHelp()
	}
	if m.popup.kind != popupNone {
		body = m.drawPopup(body)
		help = []hint{{"esc", "dismiss"}}
	}
	lines := make([]string, 0, m.height)
	lines = append(lines, m.topBar())
	lines = append(lines, body...)
	bar, _ := m.hintBar(help)
	lines = append(lines, fill(" "+bar, m.width, colorBar))
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	if !m.mouseOff {
		view.MouseMode = tea.MouseModeAllMotion
	}
	return view
}

// mainView draws the chat list beside the open conversation and its team.
func (m model) mainView(height int) []string {
	listWidth, centerWidth, team := m.columnWidths()
	var center []string
	switch m.screen {
	case screenChat:
		center = m.chatView(centerWidth, height)
	case screenSearch:
		center = m.searchView(centerWidth, height)
	default:
		center = m.homeView(centerWidth, height)
	}
	columns := [][]string{}
	if listWidth > 0 {
		columns = append(columns, column(m.sidebarView(listWidth, height), listWidth, height))
	}
	columns = append(columns, column(center, centerWidth, height))
	if team > 0 {
		columns = append(columns, column(m.teamView(team, height), team, height))
	}
	body := joinColumns(m.dividers(listWidth > 0, team > 0), columns...)
	if m.inviteWindow.open {
		body = m.drawInvite(body)
	}
	return body
}

// dividers draws the column dividers, lit while the pointer is over one or drags it.
func (m model) dividers(hasList bool, hasTeam bool) []string {
	plain, lit := borderStyle.Render("│"), accentStyle.Render("┃")
	left, right := plain, plain
	if m.hover == "divider:left" || m.dragging == leftDivider {
		left = lit
	}
	if m.hover == "divider:right" || m.dragging == rightDivider {
		right = lit
	}
	switch {
	case hasList && hasTeam:
		return []string{left, right}
	case hasList:
		return []string{left}
	case hasTeam:
		return []string{right}
	}
	return nil
}

// topBar draws the top bar line.
func (m model) topBar() string {
	line, _ := m.topBarLayout()
	return line
}

// topBarLayout names the app and checkout on the left and shows the status and the Cores
// button on the right. It returns the line and the button's zone on row 0.
func (m model) topBarLayout() (string, []zone) {
	left := " " + brightStyle.Render("agentcy") + "  " + dimStyle.Render(cleanLine(m.directory))
	kind := "chats"
	if m.list.archived {
		kind = "archived chats"
	}
	right := okStyle.Render("●") + dimStyle.Render(fmt.Sprintf(" core running · %d %s ", len(m.list.items), kind))
	switch {
	case m.offline:
		right = warnStyle.Render("▲ core not reachable, retrying ")
	case m.status != "":
		right = textStyle.Render(cleanLine(m.status) + " ")
	}
	coresButton, spans := m.buttons(button{id: "cores", label: "Cores"})
	buttonStart := ansi.StringWidth(right)
	right += coresButton + " "
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return fill(right, m.width, colorBar), place(visibleSpans(spans, m.width-buttonStart), buttonStart, 0)
	}
	buttonStart += ansi.StringWidth(left) + gap
	return fill(left+strings.Repeat(" ", gap)+right, m.width, colorBar), place(spans, buttonStart, 0)
}

// tick schedules the next poll.
func tick() tea.Cmd {
	return tea.Tick(pollInterval, func(now time.Time) tea.Msg { return tickMsg(now) })
}

// loadPlugins asks the core for the configured plugins.
func (m model) loadPlugins() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		plugins, err := client.Plugins()
		return pluginsMsg{plugins: plugins, err: err}
	}
}

// loadList asks the core for this checkout's active or archived conversations.
func (m model) loadList() tea.Cmd {
	client, request := m.client, ListRequest{Directory: m.directory, Archived: m.list.archived}
	return func() tea.Msg {
		list, err := client.List(request)
		return listMsg{archived: request.Archived, list: list, err: err}
	}
}

// submissionID returns a random ID that protects one post against duplicate delivery.
func submissionID() string {
	random := make([]byte, 16)
	_, _ = rand.Read(random) // crypto/rand.Read never returns an error.
	return "ui-" + hex.EncodeToString(random)
}

// foldName returns the key under which a participant name is stored.
func foldName(name string) string {
	return strings.ToLower(name)
}
