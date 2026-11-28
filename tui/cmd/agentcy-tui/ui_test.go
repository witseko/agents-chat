package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const testConversation = "0123456789abcdef01234567"

// fakeOperator records requests and returns canned results.
type fakeOperator struct {
	list        ConversationList
	archive     ConversationList
	results     SearchResults
	lists       []ListRequest
	archives    []ArchiveRequest
	renames     []RenameRequest
	searches    []SearchRequest
	view        ConversationView
	showFunc    func(ShowRequest) (ConversationView, error)
	showErr     error
	tasks       TaskList
	tasksErr    error
	taskCalls   []TasksRequest
	plugins     PluginList
	creates     []CreateRequest
	launches    []LaunchRequest
	pings       []PingRequest
	pingResult  PingResult
	retires     []RetireRequest
	roleChanges []SetRoleRequest
	budgets     []SetMemberBudgetRequest
	posts       []PostRequest
	closes      []CloseRequest
	reopens     []ReopenRequest
	invites     []InviteRequest
	openInvites []OpenInviteRequest
	shows       []ShowRequest
}

func (f *fakeOperator) Plugins() (PluginList, error) { return f.plugins, nil }

func (f *fakeOperator) List(request ListRequest) (ConversationList, error) {
	f.lists = append(f.lists, request)
	if request.Archived {
		return f.archive, nil
	}
	return f.list, nil
}

func (f *fakeOperator) Archive(request ArchiveRequest) (ArchivedConversation, error) {
	f.archives = append(f.archives, request)
	return ArchivedConversation{Archived: request.Archived}, nil
}

func (f *fakeOperator) Rename(request RenameRequest) (RenamedConversation, error) {
	f.renames = append(f.renames, request)
	if request.Title == nil {
		return RenamedConversation{Title: "Title from the brief"}, nil
	}
	return RenamedConversation{Title: *request.Title}, nil
}

func (f *fakeOperator) Search(request SearchRequest) (SearchResults, error) {
	f.searches = append(f.searches, request)
	return f.results, nil
}

func (f *fakeOperator) Show(request ShowRequest) (ConversationView, error) {
	f.shows = append(f.shows, request)
	if f.showFunc != nil {
		return f.showFunc(request)
	}
	return f.view, f.showErr
}

func (f *fakeOperator) Tasks(request TasksRequest) (TaskList, error) {
	f.taskCalls = append(f.taskCalls, request)
	return f.tasks, f.tasksErr
}

func (f *fakeOperator) Create(request CreateRequest) (CreatedConversation, error) {
	f.creates = append(f.creates, request)
	invitations := []JoinInvitation{{Participant: request.Lead, Role: "lead", Code: "LEAD-CODE", JoinPrompt: "join LEAD-CODE"}}
	for _, name := range request.Invitees {
		invitations = append(invitations, JoinInvitation{Participant: name, Role: "worker", Code: "W", JoinPrompt: "join W"})
	}
	created := CreatedConversation{Conversation: testConversation, Invitations: invitations}
	if request.OpenInvite {
		created.OpenInvite = &OpenInviteInvitation{Code: "OPEN-CODE", JoinPrompt: "join OPEN-CODE"}
	}
	return created, nil
}

func (f *fakeOperator) Invite(request InviteRequest) (JoinInvitation, error) {
	f.invites = append(f.invites, request)
	return JoinInvitation{Participant: request.Name, Role: "worker", Code: "NEW", JoinPrompt: "join NEW"}, nil
}

func (f *fakeOperator) OpenInvite(request OpenInviteRequest) (OpenInviteStatus, error) {
	f.openInvites = append(f.openInvites, request)
	if !request.Enabled {
		return OpenInviteStatus{OpenInvite: false}, nil
	}
	return OpenInviteStatus{OpenInvite: true, Code: "OPEN-ROTATED", JoinPrompt: "join OPEN-ROTATED"}, nil
}

func (f *fakeOperator) Launch(request LaunchRequest) (LaunchQueued, error) {
	f.launches = append(f.launches, request)
	return LaunchQueued{AssignmentID: "a1", State: "queued"}, nil
}

func (f *fakeOperator) Ping(request PingRequest) (PingResult, error) {
	f.pings = append(f.pings, request)
	if f.pingResult.Participant != "" {
		return f.pingResult, nil
	}
	rtt := int64(2)
	return PingResult{
		Participant: request.Name,
		Status:      "woken",
		Listening:   true,
		Connected:   true,
		Woke:        true,
		PingRTTMs:   &rtt,
	}, nil
}

func (f *fakeOperator) Retire(request RetireRequest) (RetiredParticipant, error) {
	f.retires = append(f.retires, request)
	return RetiredParticipant{Participant: request.Name, Retired: true}, nil
}

func (f *fakeOperator) SetRole(request SetRoleRequest) (ParticipantRoleChanged, error) {
	f.roleChanges = append(f.roleChanges, request)
	return ParticipantRoleChanged{Participant: request.Name, Role: request.Role}, nil
}

func (f *fakeOperator) SetMemberBudget(request SetMemberBudgetRequest) (MemberBudgetUpdated, error) {
	f.budgets = append(f.budgets, request)
	return MemberBudgetUpdated{
		Participant: request.Name,
		Model:       request.Model,
		TokenBudget: request.TokenBudget,
	}, nil
}

func (f *fakeOperator) Post(request PostRequest) (PostedMessage, error) {
	f.posts = append(f.posts, request)
	return PostedMessage{MessageID: 1}, nil
}

func (f *fakeOperator) Close(request CloseRequest) (ClosedConversation, error) {
	f.closes = append(f.closes, request)
	return ClosedConversation{Closed: true}, nil
}

func (f *fakeOperator) Reopen(request ReopenRequest) (ReopenedConversation, error) {
	f.reopens = append(f.reopens, request)
	return ReopenedConversation{Reopened: true}, nil
}

// press sends a named key, such as "enter", "tab" or "ctrl+x".
func press(t *testing.T, m model, key string) (model, tea.Cmd) {
	t.Helper()
	keys := map[string]tea.KeyPressMsg{
		"enter":     {Code: tea.KeyEnter},
		"tab":       {Code: tea.KeyTab},
		"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
		"esc":       {Code: tea.KeyEscape},
		"up":        {Code: tea.KeyUp},
		"down":      {Code: tea.KeyDown},
		"right":     {Code: tea.KeyRight},
		"ctrl+x":    {Code: 'x', Mod: tea.ModCtrl},
		"ctrl+p":    {Code: 'p', Mod: tea.ModCtrl},
		"ctrl+s":    {Code: 's', Mod: tea.ModCtrl},
		"ctrl+o":    {Code: 'o', Mod: tea.ModCtrl},
		"ctrl+d":    {Code: 'd', Mod: tea.ModCtrl},
		"space":     {Code: ' ', Text: " "},
	}
	msg, named := keys[key]
	if !named {
		runes := []rune(key)
		msg = tea.KeyPressMsg{Code: runes[0], Text: key}
	}
	next, command := m.Update(msg)
	return next.(model), command
}

// typeText sends each character of text as a key press.
func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, character := range text {
		next, _ := m.Update(tea.KeyPressMsg{Code: character, Text: string(character)})
		m = next.(model)
	}
	return m
}

// drive executes a command and its batched commands and feeds every message that arrives
// promptly back into the model. Timers and cursor blinks do not arrive in time and are
// dropped, so the poll clock does not run during a test.
func drive(t *testing.T, m model, command tea.Cmd) model {
	t.Helper()
	for _, msg := range collect(command) {
		next, more := m.Update(msg)
		m = drive(t, next.(model), more)
	}
	return m
}

// collect runs a command tree and returns the messages that arrive within a short wait.
func collect(command tea.Cmd) []tea.Msg {
	if command == nil {
		return nil
	}
	arrived := make(chan tea.Msg, 1)
	go func() { arrived <- command() }()
	select {
	case msg := <-arrived:
		if batch, isBatch := msg.(tea.BatchMsg); isBatch {
			var messages []tea.Msg
			for _, inner := range batch {
				messages = append(messages, collect(inner)...)
			}
			return messages
		}
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// chatModel returns a model showing a conversation with a lead, a worker and the human.
func chatModel(t *testing.T, fake *fakeOperator) model {
	t.Helper()
	fake.view = ConversationView{
		Conversation: testConversation,
		Brief:        "Review the retry rule.",
		Status: ConversationStatus{Open: true, Members: map[string]MemberStatus{
			"Lead":     {Role: "lead", State: "joined"},
			"Reviewer": {Role: "worker", State: "invited"},
			"Human":    {Role: "human", State: "human"},
		}},
	}
	m := newModel(fake, "/work")
	next, command := m.openChat(testConversation)
	return drive(t, next.(model), command)
}

func TestResizeBeforeAnyScreenOpens(t *testing.T) {
	m := newModel(&fakeOperator{}, "/work")

	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, command := press(t, next.(model), "n")
	m = drive(t, m, command)
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if lines := strings.Count(next.(model).View().Content, "\n") + 1; lines != 24 {
		t.Fatalf("view has %d lines, want 24", lines)
	}
}

func TestListNavigationOpensTheSelectedChat(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{
		{Conversation: "a", Title: "first", Open: true}, {Conversation: "b", Title: "second", Open: true},
		{Conversation: "c", Title: "third", Open: true},
	}}}
	m := drive(t, newModel(fake, "/work"), newModel(fake, "/work").loadList())

	m, _ = press(t, m, "down")
	m, _ = press(t, m, "j")
	m, _ = press(t, m, "up")
	m, command := press(t, m, "enter")
	m = drive(t, m, command)

	if m.screen != screenChat || m.chat.id != "b" {
		t.Fatalf("screen %v chat %q, want the chat view of b", m.screen, m.chat.id)
	}
	if len(fake.shows) != 1 || fake.shows[0].After != 0 {
		t.Fatalf("shows = %+v, want one read from cursor zero", fake.shows)
	}
}

func TestNewChatCreatesThenLaunchesPluginSlots(t *testing.T) {
	fake := &fakeOperator{plugins: PluginList{Plugins: []PluginSummary{{Name: "glm", Model: "glm-5"}}}}
	m := newModel(fake, "/work")
	m, command := press(t, m, "n")
	m = drive(t, m, command)
	m = typeText(t, m, "Check retries")
	for range 4 { // brief, lead name, open invite, worker name
		m, _ = press(t, m, "tab")
	}
	m, _ = press(t, m, "right") // worker source: manual -> glm
	m, _ = press(t, m, "tab")   // Create
	m, command = press(t, m, "enter")
	m = drive(t, m, command)

	if len(fake.creates) != 1 {
		t.Fatalf("creates = %+v", fake.creates)
	}
	created := fake.creates[0]
	if created.Brief != "Check retries" || created.Lead != "Lead" || len(created.Invitees) != 1 || created.Invitees[0] != "Worker" {
		t.Fatalf("create request = %+v", created)
	}
	if len(fake.launches) != 1 || fake.launches[0].Name != "Worker" || fake.launches[0].Plugin != "glm" || fake.launches[0].Conversation != testConversation {
		t.Fatalf("launches = %+v", fake.launches)
	}
	if m.screen != screenChat || m.chat.id != testConversation {
		t.Fatalf("screen %v chat %q, want the new chat", m.screen, m.chat.id)
	}
	codes := m.invitations[testConversation]
	if _, kept := codes["lead"]; !kept || len(codes) != 1 {
		t.Fatalf("kept codes for %v, want only the manual lead", codes)
	}
}

func TestPostAddressesAMemberAsAQuestion(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)

	m.chat.input.SetValue("@reviewer ?Which test fails?")
	m, command := press(t, m, "enter")
	m = drive(t, m, command)
	m.chat.input.SetValue("?@Lead Status?")
	m, command = press(t, m, "enter")
	m = drive(t, m, command)
	m.chat.input.SetValue("Plain note")
	m, command = press(t, m, "enter")
	drive(t, m, command)

	want := []PostRequest{
		{To: "Reviewer", Kind: "question", Body: "Which test fails?"},
		{To: "Lead", Kind: "question", Body: "Status?"},
		{Kind: "note", Body: "Plain note"},
	}
	if len(fake.posts) != len(want) {
		t.Fatalf("posts = %+v", fake.posts)
	}
	for index, post := range fake.posts {
		if post.To != want[index].To || post.Kind != want[index].Kind || post.Body != want[index].Body {
			t.Errorf("post %d = %+v, want %+v", index, post, want[index])
		}
		if !strings.HasPrefix(post.SubmissionID, "ui-") || post.Conversation != testConversation {
			t.Errorf("post %d has submission %q in %q", index, post.SubmissionID, post.Conversation)
		}
	}
}

func TestUnknownAddresseeIsRejected(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)

	m.chat.input.SetValue("@Nobody hello")
	m, command := press(t, m, "enter")
	m = drive(t, m, command)
	if m.popup.kind != popupError || !strings.Contains(m.popup.text, "no member named @Nobody") {
		t.Fatalf("popup = %+v", m.popup)
	}
	// Enter dismisses the error popup and does not post.
	m, _ = press(t, m, "enter")
	if m.popup.kind != popupNone || len(fake.posts) != 0 {
		t.Fatalf("popup %+v posts %+v", m.popup, fake.posts)
	}
	m.chat.input.SetValue("@Human hello")
	m, command = press(t, m, "enter")
	m = drive(t, m, command)

	if len(fake.posts) != 0 {
		t.Fatalf("posted %+v", fake.posts)
	}
	if !strings.Contains(m.popup.text, "no member named @Human") {
		t.Fatalf("popup text = %q", m.popup.text)
	}
	if m.chat.input.Value() != "@Human hello" {
		t.Fatalf("input lost: %q", m.chat.input.Value())
	}
}

func TestTranscriptDropsTerminalControls(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	hostile := ConversationView{
		Conversation: testConversation,
		Status:       fake.view.Status,
		Messages: []TranscriptMessage{{
			ID: 1, Label: "Le\x1b[2Jad", Created: 1795000000,
			Body: "\x1b]0;owned\x07hello\x00 \x9b31m world‮!",
		}},
		Cursor: 1,
	}

	next, _ := m.Update(showMsg{conversation: testConversation, after: 0, view: hostile})
	content := next.(model).View().Content

	for _, forbidden := range []string{"\x1b[2J", "\x1b]", "\x07", "\x00", "\x9b", "‮"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("view contains %q", forbidden)
		}
	}
	if !strings.Contains(content, "Le\uFFFD[2Jad") || !strings.Contains(content, "\uFFFD]0;owned\uFFFDhello") {
		t.Errorf("view lost the message text:\n%s", content)
	}
}

func TestCloseNeedsConfirmation(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)

	m, _ = press(t, m, "ctrl+x")
	m, _ = press(t, m, "esc")
	if len(fake.closes) != 0 || m.chat.closing {
		t.Fatalf("cancel closed the chat: %+v", fake.closes)
	}
	m, _ = press(t, m, "ctrl+x")
	m = typeText(t, m, "All done")
	m, command := press(t, m, "enter")
	drive(t, m, command)

	if len(fake.closes) != 1 || fake.closes[0].Summary != "All done" {
		t.Fatalf("closes = %+v", fake.closes)
	}
}

func TestPromptsCopyTheKnownJoinPrompt(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	var copied []string
	m.clipboard = func(text string) error {
		copied = append(copied, text)
		return nil
	}

	m, _ = press(t, m, "ctrl+p")
	m, command := press(t, m, "c")
	m = drive(t, m, command)
	if len(copied) != 0 {
		t.Fatalf("copied %v without a known code", copied)
	}
	m, command = press(t, m, "r")
	m = drive(t, m, command)
	m, command = press(t, m, "c")
	drive(t, m, command)

	if len(fake.invites) != 1 || fake.invites[0].Name != "Reviewer" {
		t.Fatalf("invites = %+v", fake.invites)
	}
	if len(copied) != 1 || copied[0] != "join NEW" {
		t.Fatalf("copied = %v", copied)
	}
}

func TestPromptsCopyThroughTheTerminalWithoutAClipboardTool(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	m.clipboard = nil

	m, _ = press(t, m, "ctrl+p")
	m, command := press(t, m, "r")
	m = drive(t, m, command)
	m, command = press(t, m, "c")

	var sent []string
	for _, msg := range collect(command) {
		if fmt.Sprintf("%T", msg) == "tea.setClipboardMsg" {
			sent = append(sent, fmt.Sprint(msg))
			continue
		}
		next, _ := m.Update(msg)
		m = next.(model)
	}
	if len(sent) != 1 || sent[0] != "join NEW" {
		t.Fatalf("terminal clipboard got %v", sent)
	}
	if m.popup.kind == popupError {
		t.Fatalf("copy showed an error: %+v", m.popup)
	}
}

func TestPromptsViewShowsTheWholePromptForSelection(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	m, _ = press(t, m, "ctrl+p")
	m, command := press(t, m, "r")
	m = drive(t, m, command)
	words := strings.Repeat("join with the code ", 20) + "CODE-abc.123"
	m.invitations[m.chat.id][foldName("Reviewer")] = JoinInvitation{JoinPrompt: words}

	m, _ = press(t, m, "v")
	view := m.View()
	if view.MouseMode != tea.MouseModeNone {
		t.Fatalf("mouse mode = %v, want no capture so the terminal can select", view.MouseMode)
	}
	plain := ansi.Strip(view.Content)
	if strings.Contains(plain, "│") {
		t.Fatalf("prompt view draws borders:\n%s", plain)
	}
	if got := strings.Join(strings.Fields(plain), " "); !strings.Contains(got, strings.TrimSpace(words)) {
		t.Fatalf("prompt view misses part of the prompt:\n%s", plain)
	}

	m, _ = press(t, m, "esc")
	if m.inviteWindow.viewing || !m.inviteWindow.open {
		t.Fatalf("esc should go back to the invite window: %+v", m.inviteWindow)
	}
}

func TestVanishedCoreShowsAStatusAndRetries(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	fake.showErr = errCoreUnreachable

	next, command := m.Update(tickMsg(time.Now()))
	m = drive(t, next.(model), command)
	if m.status != offlineStatus || m.chat.polling {
		t.Fatalf("status %q polling %v", m.status, m.chat.polling)
	}

	fake.showErr = nil
	next, command = m.Update(tickMsg(time.Now()))
	m = drive(t, next.(model), command)
	if m.status != "" || len(fake.shows) != 3 {
		t.Fatalf("status %q after %d reads", m.status, len(fake.shows))
	}
}

func TestCreateOpensTheInviteWindowForManualSlots(t *testing.T) {
	fake := &fakeOperator{}
	m := newModel(fake, "/work")
	m, command := press(t, m, "n")
	m = drive(t, m, command)
	m = typeText(t, m, "Check retries")
	m, command = press(t, m, "ctrl+s")
	m = drive(t, m, command)

	if len(fake.creates) != 1 || !m.inviteWindow.open || m.screen != screenChat {
		t.Fatalf("creates %d, invite window %v, screen %v", len(fake.creates), m.inviteWindow.open, m.screen)
	}
	m, _ = press(t, m, "esc")
	if m.inviteWindow.open {
		t.Fatal("esc left the invite window open")
	}
}

func TestTeamPanelCopiesAMemberPrompt(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	var copied []string
	m.clipboard = func(text string) error {
		copied = append(copied, text)
		return nil
	}

	m, _ = press(t, m, "tab") // team panel
	m, _ = press(t, m, "down")
	if m.selectedMember() != "Reviewer" {
		t.Fatalf("selected %q, want Reviewer", m.selectedMember())
	}
	m, command := press(t, m, "c") // no code yet: asks for one and opens the invite window
	m = drive(t, m, command)
	if len(fake.invites) != 1 || !m.inviteWindow.open {
		t.Fatalf("invites %+v, invite window %v", fake.invites, m.inviteWindow.open)
	}
	m, command = press(t, m, "c")
	drive(t, m, command)
	if len(copied) != 1 || copied[0] != "join NEW" {
		t.Fatalf("copied = %v", copied)
	}
}

func TestViewLinesFitTheTerminal(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	for _, size := range [][2]int{{60, 20}, {90, 30}, {140, 40}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(next.(model).View().Content, "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for index, line := range lines {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Errorf("%dx%d: line %d is %d columns", size[0], size[1], index, width)
			}
		}
	}
}

// click sends a left click at a screen cell.
func click(t *testing.T, m model, x int, y int) (model, tea.Cmd) {
	t.Helper()
	next, command := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(model), command
}

// sized returns the model after a window resize.
func sized(m model, width int, height int) model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(model)
}

func TestClickingAChatInTheListOpensIt(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{
		{Conversation: "first", Title: "First", Open: true}, {Conversation: testConversation, Title: "Second", Open: true},
	}}}
	m := sized(newModel(fake, "/work"), 120, 30)
	m = drive(t, m, m.loadList())

	// Body rows start under the top bar; chats start two rows into the list, three rows each.
	m, command := click(t, m, 4, 1+2+3)
	m = drive(t, m, command)
	if m.screen != screenChat || m.chat.id != testConversation {
		t.Fatalf("screen %v chat %q, want the second chat", m.screen, m.chat.id)
	}
}

func TestClosedChatsStayInACollapsedClosedSection(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{
		{Conversation: "old", Title: "Old work"}, {Conversation: "live", Title: "Live work", Open: true},
	}}}
	m := sized(newModel(fake, "/work"), 120, 30)
	m = drive(t, m, m.loadList())

	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "▸ CLOSED (1)") || strings.Contains(content, "Old work") {
		t.Fatalf("closed chat is not collapsed under its header:\n%s", content)
	}
	if strings.Index(content, "Live work") > strings.Index(content, "CLOSED") {
		t.Fatalf("open chat is not above the CLOSED section:\n%s", content)
	}

	// The header sits under the open chat: rows 2-4 hold the chat, row 5 the header.
	m, _ = click(t, m, 4, 1+2+3)
	content = ansi.Strip(m.View().Content)
	if !strings.Contains(content, "▾ CLOSED (1)") || !strings.Contains(content, "Old work") {
		t.Fatalf("clicking the header did not expand the section:\n%s", content)
	}
	m, command := click(t, m, 4, 1+2+3+2)
	m = drive(t, m, command)
	if m.screen != screenChat || m.chat.id != "old" {
		t.Fatalf("screen %v chat %q, want the closed chat", m.screen, m.chat.id)
	}
}

func TestTopBarCountsOnlyOpenChats(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{
		{Conversation: "old", Title: "Old work"}, {Conversation: "live", Title: "Live work", Open: true},
	}}}
	m := sized(newModel(fake, "/work"), 120, 30)
	m = drive(t, m, m.loadList())

	if bar := ansi.Strip(m.topBar()); !strings.Contains(bar, "· 1 chats") {
		t.Fatalf("top bar counts closed chats: %q", bar)
	}
}

func TestClosingTheOpenChatMovesItIntoTheCollapsedSection(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{
		{Conversation: testConversation, Title: "Review the retry rule.", Open: true},
	}}}
	m := sized(chatModel(t, fake), 140, 30)
	m = drive(t, m, m.loadList())

	fake.view.Status.Open = false
	fake.list.Conversations[0].Open = false
	m, command := m.pollChat()
	m = drive(t, m, command)
	m = drive(t, m, m.loadList())

	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "▸ CLOSED (1)") || !m.list.onClosedHeader {
		t.Fatalf("closed chat did not move into the collapsed section (header selected %v):\n%s", m.list.onClosedHeader, content)
	}
}

func TestClickingTheTeamCopyLineCopiesThePrompt(t *testing.T) {
	fake := &fakeOperator{}
	m := sized(chatModel(t, fake), 140, 30)
	var copied []string
	m.clipboard = func(text string) error {
		copied = append(copied, text)
		return nil
	}
	m.invitations[testConversation] = map[string]JoinInvitation{"reviewer": {Participant: "Reviewer", JoinPrompt: "join R"}}

	var reviewer teamRow
	for _, row := range m.teamRows() {
		if row.name == "Reviewer" {
			reviewer = row
		}
	}
	m, command := click(t, m, 140-5, 1+reviewer.copyRow)
	drive(t, m, command)
	if m.chat.focus != focusTeam || m.selectedMember() != "Reviewer" || len(copied) != 1 || copied[0] != "join R" {
		t.Fatalf("focus %v selected %q copied %v", m.chat.focus, m.selectedMember(), copied)
	}
}

func TestDraggingADividerResizesTheColumn(t *testing.T) {
	m := sized(chatModel(t, &fakeOperator{}), 140, 30)
	m, _ = click(t, m, sidebarWidth, 5)
	next, _ := m.Update(tea.MouseMotionMsg{X: sidebarWidth + 10, Y: 5, Button: tea.MouseLeft})
	m = next.(model)
	next, _ = m.Update(tea.MouseReleaseMsg{X: sidebarWidth + 10, Y: 5})
	m = next.(model)
	next, _ = m.Update(tea.MouseMotionMsg{X: sidebarWidth + 20, Y: 5})
	m = next.(model)
	if m.sidebarCols != sidebarWidth+10 {
		t.Fatalf("sidebar width %d, want %d", m.sidebarCols, sidebarWidth+10)
	}
}

func TestWheelScrollsTheTranscriptAndCtrlOReleasesTheMouse(t *testing.T) {
	fake := &fakeOperator{view: longChat()}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, command := m.openChat(testConversation)
	m = drive(t, next.(model), command)
	next, _ = m.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelUp})
	m = next.(model)
	if m.chat.scroll != wheelLines {
		t.Fatalf("scroll %d, want %d", m.chat.scroll, wheelLines)
	}
	if m.View().MouseMode != tea.MouseModeAllMotion {
		t.Fatal("mouse capture is off by default")
	}
	m, _ = press(t, m, "ctrl+o")
	if m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("ctrl+o left mouse capture on")
	}
}

func TestTranscriptScrollbarThumbFollowsScroll(t *testing.T) {
	fake := &fakeOperator{view: longChat()}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, command := m.openChat(testConversation)
	m = drive(t, next.(model), command)
	_, center, _ := m.columnWidths()
	height := m.transcriptHeight(center)

	thumbRows := func(m model) []int {
		var rows []int
		for row, line := range m.transcriptWithScrollbar(center, height) {
			plain := ansi.Strip(line)
			if ansi.StringWidth(plain) != center {
				t.Fatalf("row %d is %d columns, want %d", row, ansi.StringWidth(plain), center)
			}
			if strings.HasSuffix(plain, "┃") {
				rows = append(rows, row)
			}
		}
		return rows
	}

	atTail := thumbRows(m)
	if len(atTail) == 0 || atTail[len(atTail)-1] != height-1 {
		t.Fatalf("thumb rows at the tail %v, want them to end at row %d", atTail, height-1)
	}
	m.chat.scroll = m.maxTranscriptScroll()
	if atTop := thumbRows(m); len(atTop) == 0 || atTop[0] != 0 {
		t.Fatalf("thumb rows at the top %v, want them to start at row 0", atTop)
	}
}

func TestShortTranscriptHasNoScrollbar(t *testing.T) {
	m := sized(chatModel(t, &fakeOperator{}), 140, 30)
	_, center, _ := m.columnWidths()
	for _, line := range m.transcriptWithScrollbar(center, m.transcriptHeight(center)) {
		if strings.ContainsAny(ansi.Strip(line), "┃│") {
			t.Fatalf("a short transcript shows a scrollbar: %q", ansi.Strip(line))
		}
	}
}

func TestClickingAWorkerSourceChangesIt(t *testing.T) {
	fake := &fakeOperator{plugins: PluginList{Plugins: []PluginSummary{{Name: "glm"}}}}
	m := sized(newModel(fake, "/work"), 120, 40)
	m, command := press(t, m, "n")
	m = drive(t, m, command)
	_, fieldOf, focusLine := m.formLayout()
	workerLine := -1
	for line, field := range fieldOf {
		if field == leadFields {
			workerLine = line
		}
	}
	row := workerLine - scrollFirst(len(fieldOf), focusLine, 38)
	m, _ = click(t, m, formLeft(120)+sourceColumn+2, 1+row)
	if m.form.workers[0].source != 1 || m.form.focus != leadFields+1 {
		t.Fatalf("source %d focus %d", m.form.workers[0].source, m.form.focus)
	}
}

func TestButtonsAndKeyHintsAreClickable(t *testing.T) {
	fake := &fakeOperator{}
	m := sized(chatModel(t, fake), 140, 30)
	find := func(m model, id string) zone {
		t.Helper()
		for _, item := range m.zones() {
			if item.id == id {
				return item
			}
		}
		t.Fatalf("no zone %q in %+v", id, m.zones())
		return zone{}
	}

	join := find(m, "prompts")
	m, _ = click(t, m, join.x0, join.y)
	if !m.inviteWindow.open {
		t.Fatal("Join prompts button did not open the invite window")
	}
	done := find(m, "done")
	m, _ = click(t, m, done.x0, done.y)
	if m.inviteWindow.open {
		t.Fatal("Done button left the invite window open")
	}

	m.chat.input.SetValue("hello")
	send := find(m, "send")
	m, command := click(t, m, send.x0, send.y)
	m = drive(t, m, command)
	if len(fake.posts) != 1 || fake.posts[0].Body != "hello" {
		t.Fatalf("posts = %+v", fake.posts)
	}

	closeHint := find(m, "key:ctrl+x")
	m, _ = click(t, m, closeHint.x0, closeHint.y)
	if !m.chat.closing {
		t.Fatal("clicking the ctrl+x hint did not start closing")
	}
}

func TestHoverLightsTheDividerUnderThePointer(t *testing.T) {
	m := sized(chatModel(t, &fakeOperator{}), 140, 30)
	next, _ := m.Update(tea.MouseMotionMsg{X: sidebarWidth, Y: 5})
	m = next.(model)
	if m.hover != "divider:left" || !strings.Contains(m.View().Content, "┃") {
		t.Fatalf("hover %q", m.hover)
	}
}

func TestArrowsMoveBetweenFormRows(t *testing.T) {
	fake := &fakeOperator{plugins: PluginList{Plugins: []PluginSummary{{Name: "glm"}}}}
	m := sized(newModel(fake, "/work"), 120, 40)
	m, command := press(t, m, "n")
	m = drive(t, m, command)
	m, _ = press(t, m, "ctrl+n") // second worker, focused on its name
	m, _ = press(t, m, "up")
	m, _ = press(t, m, "up")
	m, _ = press(t, m, "up")
	m, _ = press(t, m, "up") // worker 2 -> worker 1 -> open invite -> lead -> brief
	if kind, _ := m.form.field(m.form.focus); kind != fieldBrief {
		t.Fatalf("focus %d, want the brief", m.form.focus)
	}
	m = typeText(t, m, "Check retries")
	steps := []struct {
		key  string
		want int
	}{
		{"down", int(fieldLeadName)},
		{"down", int(fieldOpenInvite)},
		{"down", leadFields},     // worker 1 name
		{"tab", leadFields + 1},  // worker 1 source
		{"down", leadFields + 3}, // worker 2 source keeps the column
		{"down", leadFields + 4}, // Create
		{"up", leadFields + 2},   // worker 2 name
	}
	for _, step := range steps {
		m, _ = press(t, m, step.key)
		if m.form.focus != step.want {
			t.Fatalf("after %s focus %d, want %d", step.key, m.form.focus, step.want)
		}
	}
	if m.form.brief.Value() != "Check retries" {
		t.Fatalf("brief = %q", m.form.brief.Value())
	}
}

func TestRenameKeySendsTheEditedTitleAndUpdatesTheList(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{
		{Conversation: "open", Title: "Open", Open: true}, {Conversation: "done", Title: "Done"},
	}}}
	m := sized(newModel(fake, "/work"), 120, 30)
	m = drive(t, m, m.loadList())

	m, _ = press(t, m, "down")
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "e")
	if !m.rename.open || m.rename.input.Value() != "Done" {
		t.Fatalf("rename dialog open %v with %q", m.rename.open, m.rename.input.Value())
	}
	if !strings.Contains(m.View().Content, "Rename this chat") {
		t.Fatal("the rename dialog is not drawn")
	}
	// Keys go to the title field, so n and q neither open the form nor quit.
	m = typeText(t, m, " nq")
	if m.screen != screenHome || m.rename.input.Value() != "Done nq" {
		t.Fatalf("screen %v, title %q", m.screen, m.rename.input.Value())
	}
	fake.list.Conversations[1].Title = "Done nq"
	m, command := press(t, m, "enter")
	m = drive(t, m, command)

	if len(fake.renames) != 1 || fake.renames[0].Conversation != "done" ||
		fake.renames[0].Title == nil || *fake.renames[0].Title != "Done nq" {
		t.Fatalf("renames = %+v", fake.renames)
	}
	if m.rename.open || m.list.items[1].Title != "Done nq" || !strings.Contains(m.popup.text, "renamed") {
		t.Fatalf("open %v, items %+v, popup %q", m.rename.open, m.list.items, m.popup.text)
	}
}

func TestRenameEscCancelsAndAnEmptyTitleClearsIt(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{
		{Conversation: "done", Title: "Done"},
	}}}
	m := sized(newModel(fake, "/work"), 120, 30)
	m = drive(t, m, m.loadList())

	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "e")
	m = typeText(t, m, "x")
	m, command := press(t, m, "esc")
	m = drive(t, m, command)
	if m.rename.open || len(fake.renames) != 0 {
		t.Fatalf("esc left the dialog open %v or renamed: %+v", m.rename.open, fake.renames)
	}

	m, _ = press(t, m, "e")
	m.rename.input.SetValue("   ")
	m, command = press(t, m, "enter")
	m = drive(t, m, command)
	if len(fake.renames) != 1 || fake.renames[0].Title != nil {
		t.Fatalf("renames = %+v, want one request with a nil title", fake.renames)
	}
	if m.list.items[0].Title != "Title from the brief" {
		t.Fatalf("title = %q", m.list.items[0].Title)
	}
}

func TestArchiveKeyMovesAClosedChatAndVSwitchesTheList(t *testing.T) {
	fake := &fakeOperator{
		list: ConversationList{Conversations: []ConversationSummary{
			{Conversation: "open", Title: "Open", Open: true}, {Conversation: "done", Title: "Done"},
		}},
		archive: ConversationList{Conversations: []ConversationSummary{{Conversation: "old", Title: "Old"}}},
	}
	m := sized(newModel(fake, "/work"), 120, 30)
	m = drive(t, m, m.loadList())

	m, command := press(t, m, "a")
	m = drive(t, m, command)
	if len(fake.archives) != 0 || !strings.Contains(m.popup.text, "Close the chat") {
		t.Fatalf("archived an open chat: %+v, status %q", fake.archives, m.popup.text)
	}
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "down")
	m, command = press(t, m, "a")
	m = drive(t, m, command)
	if len(fake.archives) != 1 || fake.archives[0] != (ArchiveRequest{Directory: "/work", Conversation: "done", Archived: true}) {
		t.Fatalf("archives = %+v", fake.archives)
	}

	m, command = press(t, m, "v")
	m = drive(t, m, command)
	if !m.list.archived || len(m.list.items) != 1 || m.list.items[0].Conversation != "old" {
		t.Fatalf("archived view %v items %+v", m.list.archived, m.list.items)
	}
	if !strings.Contains(m.View().Content, "Archived") {
		t.Fatal("the list does not show the view switch")
	}
	m, command = press(t, m, "a")
	drive(t, m, command)
	if last := fake.archives[len(fake.archives)-1]; last.Conversation != "old" || last.Archived {
		t.Fatalf("unarchive request = %+v", last)
	}
}

func TestLateListAnswerForTheOtherViewIsDropped(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{{Conversation: "a", Title: "Active"}}}}
	m := sized(newModel(fake, "/work"), 120, 30)
	stale := m.loadList()
	m, _ = press(t, m, "v")
	m = drive(t, m, stale)
	if m.list.loaded || len(m.list.items) != 0 {
		t.Fatalf("archive shows the active list: %+v", m.list.items)
	}
}

func TestViewButtonsAndArchiveButtonAreClickable(t *testing.T) {
	fake := &fakeOperator{}
	m := sized(chatModel(t, fake), 140, 30)
	find := func(m model, id string) zone {
		t.Helper()
		for _, item := range m.zones() {
			if item.id == id {
				return item
			}
		}
		t.Fatalf("no zone %q in %+v", id, m.zones())
		return zone{}
	}
	archived := find(m, "view:archived")
	m, command := click(t, m, archived.x0, archived.y)
	m = drive(t, m, command)
	if !m.list.archived || fake.lists[len(fake.lists)-1] != (ListRequest{Directory: "/work", Archived: true}) {
		t.Fatalf("archived %v lists %+v", m.list.archived, fake.lists)
	}

	fake.view.Status.Open = false
	m, command = m.pollChat()
	m = drive(t, m, command)
	archive := find(m, "archive")
	m, command = click(t, m, archive.x0, archive.y)
	drive(t, m, command)
	if len(fake.archives) != 1 || fake.archives[0].Conversation != testConversation || !fake.archives[0].Archived {
		t.Fatalf("archives = %+v", fake.archives)
	}
}

// longChat returns a closed conversation with enough messages to scroll.
func longChat() ConversationView {
	var messages []TranscriptMessage
	for id := int64(1); id <= 40; id++ {
		messages = append(messages, TranscriptMessage{ID: id, Label: "Lead", Body: fmt.Sprintf("Message %d", id), Created: 1795000000})
	}
	return ConversationView{
		Conversation: testConversation,
		Brief:        "Old review",
		Archived:     true,
		Status:       ConversationStatus{Members: map[string]MemberStatus{"Lead": {Role: "lead", State: "done"}}},
		Messages:     messages,
		Cursor:       40,
	}
}

func TestSearchOpensTheChatAtTheMatchingMessage(t *testing.T) {
	target := int64(12)
	author := "Lead"
	fake := &fakeOperator{
		view: longChat(),
		results: SearchResults{Hits: []SearchHit{
			{Conversation: testConversation, Title: "Old review", Archived: true, MessageID: &target, Author: &author, Snippet: "Message 12"},
		}},
	}
	m := sized(newModel(fake, "/work"), 120, 30)

	m, command := press(t, m, "/")
	m = drive(t, m, command)
	if m.screen != screenSearch {
		t.Fatalf("screen %v, want search", m.screen)
	}
	m = typeText(t, m, "message 12")
	m, command = press(t, m, "enter")
	m = drive(t, m, command)
	if len(fake.searches) != 1 || fake.searches[0] != (SearchRequest{Directory: "/work", Query: "message 12"}) {
		t.Fatalf("searches = %+v", fake.searches)
	}
	if !strings.Contains(m.View().Content, "ARCHIVED") {
		t.Fatal("the result does not show that its chat is archived")
	}

	m, command = press(t, m, "enter")
	m = drive(t, m, command)
	if m.screen != screenChat || m.chat.id != testConversation || m.chat.highlight != target {
		t.Fatalf("screen %v chat %q highlight %d", m.screen, m.chat.id, m.chat.highlight)
	}
	if !m.list.archived {
		t.Fatal("the chat list did not switch to the archive for an archived result")
	}
	visible := visibleTranscriptText(m)
	if !strings.Contains(visible, "Message 12") || strings.Contains(visible, "Message 40") {
		t.Fatalf("transcript is not scrolled to message 12:\n%s", visible)
	}
}

// visibleTranscriptText returns the transcript rows on screen without styles.
func visibleTranscriptText(m model) string {
	_, center, _ := m.columnWidths()
	return ansi.Strip(strings.Join(m.visibleTranscript(center, m.transcriptHeight(center)), "\n"))
}

func TestSearchResultsAreClickableAndEscGoesBack(t *testing.T) {
	fake := &fakeOperator{results: SearchResults{Hits: []SearchHit{
		{Conversation: "other", Title: "Other", Snippet: "first"},
		{Conversation: testConversation, Title: "Old review", Snippet: "in the brief"},
	}}}
	m := sized(chatModel(t, fake), 140, 30)
	fake.view = longChat()
	m, _ = press(t, m, "tab")
	m, _ = press(t, m, "tab") // chat list focus
	m, command := press(t, m, "/")
	m = drive(t, m, command)
	m, _ = press(t, m, "esc")
	if m.screen != screenChat {
		t.Fatalf("esc went to screen %v, want the chat", m.screen)
	}

	search := zone{}
	for _, item := range m.zones() {
		if item.id == "search" {
			search = item
		}
	}
	m, command = click(t, m, search.x0, search.y)
	m = drive(t, m, command)
	m = typeText(t, m, "brief")
	m, command = press(t, m, "enter")
	m = drive(t, m, command)

	// The second result's title row: body rows start at 1, four header rows, four rows a result.
	m, command = click(t, m, sidebarWidth+5, 1+searchHeaderRows+searchHitRows)
	m = drive(t, m, command)
	if m.screen != screenChat || m.chat.id != testConversation || m.chat.highlight != 0 {
		t.Fatalf("screen %v chat %q highlight %d", m.screen, m.chat.id, m.chat.highlight)
	}
	if visible := visibleTranscriptText(m); !strings.Contains(visible, "Message 1\n") || strings.Contains(visible, "Message 40") {
		t.Fatalf("a brief match does not open at the start:\n%s", visible)
	}
}

func TestHighlightTermsMarksWordsThatStartWithATerm(t *testing.T) {
	got := highlightTerms("Retry the retrying job; skip jobs.", queryTerms("RETRY job"))
	if ansi.Strip(got) != "Retry the retrying job; skip jobs." {
		t.Fatalf("text changed: %q", ansi.Strip(got))
	}
	for _, word := range []string{"Retry", "retrying", "job", "jobs"} {
		if !strings.Contains(got, accentStyle.Render(word)) {
			t.Errorf("%q is not highlighted in %q", word, got)
		}
	}
	if strings.Contains(got, accentStyle.Render("skip")) {
		t.Error("skip is highlighted")
	}
}

func TestMemberStateShowsOpenTasksAsWorking(t *testing.T) {
	idle := int64(12)
	glyph, detail := memberState(MemberStatus{
		Role:        "worker",
		State:       "done",
		IdleSeconds: &idle,
		OpenTasks:   []OpenTask{{ID: 42}},
	})
	if ansi.Strip(glyph) != "●" || detail != "working on #42 · active 12s ago" {
		t.Fatalf("got (%q, %q), want active working on #42", ansi.Strip(glyph), detail)
	}

	glyph, detail = memberState(MemberStatus{
		Role:      "worker",
		State:     "replied",
		OpenTasks: []OpenTask{{ID: 42}, {ID: 45}},
	})
	if ansi.Strip(glyph) != "●" || detail != "working on 2 tasks" {
		t.Fatalf("got (%q, %q), want working on 2 tasks", ansi.Strip(glyph), detail)
	}

	glyph, detail = memberState(MemberStatus{
		Role:  "worker",
		State: "done",
	})
	if ansi.Strip(glyph) != "○" || detail != "done" {
		t.Fatalf("got (%q, %q), want done", ansi.Strip(glyph), detail)
	}

	glyph, detail = memberState(MemberStatus{
		Role:  "worker",
		State: "retired",
	})
	if ansi.Strip(glyph) != "✕" || detail != "archived" {
		t.Fatalf("got (%q, %q), want archived", ansi.Strip(glyph), detail)
	}
}

func TestRetireFromTeamPanelConfirmsAndCancels(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	m.invitations[testConversation] = map[string]JoinInvitation{
		foldName("Reviewer"): {Participant: "Reviewer", Code: "W", JoinPrompt: "join W"},
	}

	// Tab to team panel, move down to Reviewer, press d to start retiring, esc to cancel.
	m, _ = press(t, m, "tab")
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "d")
	if m.chat.retiring != "Reviewer" {
		t.Fatalf("retiring = %q, want Reviewer", m.chat.retiring)
	}
	m, _ = press(t, m, "esc")
	if m.chat.retiring != "" || len(fake.retires) != 0 {
		t.Fatalf("esc did not cancel retire: retiring=%q retires=%+v", m.chat.retiring, fake.retires)
	}

	// Confirm retirement with enter.
	m, _ = press(t, m, "d")
	var command tea.Cmd
	m, command = press(t, m, "enter")
	m = drive(t, m, command)
	if len(fake.retires) != 1 || fake.retires[0].Name != "Reviewer" {
		t.Fatalf("retires = %+v, want Reviewer", fake.retires)
	}
	if _, found := m.knownInvitation("Reviewer"); found {
		t.Fatal("cached invitation was not cleared after retirement")
	}
	if !strings.Contains(m.popup.text, "Retired Reviewer.") {
		t.Fatalf("unexpected status: %q", m.popup.text)
	}
}

func TestRetireSlashCommandSendsRequest(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	m = typeText(t, m, "/retire Reviewer")
	var command tea.Cmd
	m, command = press(t, m, "enter")
	m = drive(t, m, command)
	if len(fake.retires) != 1 || fake.retires[0].Name != "Reviewer" {
		t.Fatalf("retires = %+v, want Reviewer", fake.retires)
	}
}

func TestNewChatWithOpenInviteAndZeroWorkersCopiesAndRotatesPrompt(t *testing.T) {
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Open room",
			Status: ConversationStatus{
				Open:       true,
				OpenInvite: true,
				Members: map[string]MemberStatus{
					"Lead":  {Role: "lead", State: "invited"},
					"Human": {Role: "human", State: "human"},
				},
			},
		},
	}
	var copied string
	m := newModel(fake, "/work")
	m.clipboard = func(text string) error { copied = text; return nil }
	m, command := press(t, m, "n")
	m = drive(t, m, command)
	m = typeText(t, m, "Open room")
	m, _ = press(t, m, "tab")    // lead name
	m, _ = press(t, m, "tab")    // open invite
	m, _ = press(t, m, "space")  // turn open invite on
	m, _ = press(t, m, "tab")    // default worker name
	m, _ = press(t, m, "ctrl+d") // remove the only worker (allowed when open invite is on)
	m, command = press(t, m, "ctrl+s")
	m = drive(t, m, command)

	if len(fake.creates) != 1 || !fake.creates[0].OpenInvite || len(fake.creates[0].Invitees) != 0 {
		t.Fatalf("creates = %+v, want open_invite=true with zero invitees", fake.creates)
	}
	if !m.inviteWindow.open {
		t.Fatal("invite window did not open")
	}
	// First card is the shared open invite. Copy it, then rotate it, then disable it.
	m, command = press(t, m, "c")
	m = drive(t, m, command)
	if copied != "join OPEN-CODE" {
		t.Fatalf("copied = %q, want join OPEN-CODE", copied)
	}
	m, _ = press(t, m, "up")
	m, command = press(t, m, "r")
	m = drive(t, m, command)
	if len(fake.openInvites) != 1 || !fake.openInvites[0].Enabled {
		t.Fatalf("openInvites = %+v, want one rotate request", fake.openInvites)
	}
	if got := m.openInvites[testConversation].Code; got != "OPEN-ROTATED" {
		t.Fatalf("open invite code = %q, want OPEN-ROTATED", got)
	}
	m, command = press(t, m, "o")
	m = drive(t, m, command)
	if len(fake.openInvites) != 2 || fake.openInvites[1].Enabled {
		t.Fatalf("openInvites = %+v, want disable request", fake.openInvites)
	}
	if _, found := m.knownOpenInvite(); found {
		t.Fatal("cached open invite was not cleared after disabling")
	}
}

func TestOpenInviteSlashCommandEnablesAndDisables(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	m = typeText(t, m, "/open-invite")
	var command tea.Cmd
	m, command = press(t, m, "enter")
	m = drive(t, m, command)
	if len(fake.openInvites) != 1 || !fake.openInvites[0].Enabled {
		t.Fatalf("openInvites = %+v, want enable", fake.openInvites)
	}
	m, _ = press(t, m, "esc") // close invite window
	m = typeText(t, m, "/open-invite off")
	m, command = press(t, m, "enter")
	m = drive(t, m, command)
	if len(fake.openInvites) != 2 || fake.openInvites[1].Enabled {
		t.Fatalf("openInvites = %+v, want disable", fake.openInvites)
	}
}

func TestShortChatScrollUpDoesNotHideArrivingMessages(t *testing.T) {
	fake := &fakeOperator{}
	m := sized(chatModel(t, fake), 140, 30)

	// Scrolling up when everything already fits must keep scroll at 0 (following the tail).
	next, _ := m.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelUp})
	m = next.(model)
	m, _ = press(t, m, "up")
	if m.chat.scroll != 0 {
		t.Fatalf("scroll = %d on a short chat, want 0", m.chat.scroll)
	}

	// Subsequent arriving messages must remain visible at the bottom.
	var incoming []TranscriptMessage
	for id := int64(1); id <= 25; id++ {
		incoming = append(incoming, TranscriptMessage{
			ID: id, Label: "Lead", Body: fmt.Sprintf("Fresh %d", id), Created: 1795000000,
		})
	}
	fake.view.Messages = incoming
	fake.view.Cursor = 25
	var cmd tea.Cmd
	m, cmd = m.pollChat()
	m = drive(t, m, cmd)

	if m.chat.scroll != 0 {
		t.Fatalf("scroll drifted to %d after new messages arrived", m.chat.scroll)
	}
	visible := visibleTranscriptText(m)
	if !strings.Contains(visible, "Fresh 25") {
		t.Fatalf("newest message disappeared from view:\n%s", visible)
	}
}

func TestOpenChatLoadsTailAndAutoPaginatesOlderOnScroll(t *testing.T) {
	const total = 80
	const pageSize = 40
	totalCount := total
	all := make([]TranscriptMessage, 0, total)
	for id := int64(1); id <= total; id++ {
		all = append(all, TranscriptMessage{
			ID: id, Label: "Lead", Body: fmt.Sprintf("Msg %d", id), Created: 1795000000,
		})
	}
	baseView := func(messages []TranscriptMessage, more bool, cursor int64) ConversationView {
		return ConversationView{
			Conversation:  testConversation,
			Brief:         "Long chat",
			Status:        ConversationStatus{Open: true, Members: map[string]MemberStatus{"Lead": {Role: "lead", State: "joined"}}},
			Messages:      messages,
			More:          more,
			Cursor:        cursor,
			TotalMessages: &totalCount,
		}
	}
	fake := &fakeOperator{
		showFunc: func(req ShowRequest) (ConversationView, error) {
			if req.Tail {
				return baseView(all[total-pageSize:], true, total), nil
			}
			if req.Before != nil {
				end := min(int(*req.Before-1), len(all))
				start := max(end-pageSize, 0)
				return baseView(all[start:end], start > 0, all[end-1].ID), nil
			}
			return baseView(nil, false, req.After), nil
		},
	}

	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd)

	if len(fake.shows) != 1 || !fake.shows[0].Tail {
		t.Fatalf("initial shows = %+v, want one tail request", fake.shows)
	}
	if len(m.chat.messages) != pageSize || m.chat.messages[0].ID != 41 || !m.chat.hasOlder {
		t.Fatalf("loaded %d messages starting at %d hasOlder=%v", len(m.chat.messages), m.chat.messages[0].ID, m.chat.hasOlder)
	}
	if !strings.Contains(m.View().Content, "80 messages") {
		t.Fatal("header does not show total_messages count")
	}

	// Scroll to the top of the loaded page to trigger auto-pagination of older messages.
	for range 20 {
		var scrollCmd tea.Cmd
		m, scrollCmd = m.scrollTranscript("pgup")
		m = drive(t, m, scrollCmd)
	}
	if len(fake.shows) < 2 || fake.shows[1].Before == nil || *fake.shows[1].Before != 41 {
		t.Fatalf("shows after scroll up = %+v, want before=41", fake.shows)
	}
	if len(m.chat.messages) != total || m.chat.messages[0].ID != 1 || m.chat.hasOlder {
		t.Fatalf("after auto-pagination: len=%d first=%d hasOlder=%v", len(m.chat.messages), m.chat.messages[0].ID, m.chat.hasOlder)
	}
}

func TestSearchStartAutoPaginatesNewerOnScrollDown(t *testing.T) {
	const total = 60
	const pageSize = 30
	totalCount := total
	all := make([]TranscriptMessage, 0, total)
	for id := int64(1); id <= total; id++ {
		all = append(all, TranscriptMessage{
			ID: id, Label: "Lead", Body: fmt.Sprintf("Entry %d", id), Created: 1795000000,
		})
	}
	fake := &fakeOperator{
		showFunc: func(req ShowRequest) (ConversationView, error) {
			start := int(req.After)
			end := min(start+pageSize, len(all))
			return ConversationView{
				Conversation:  testConversation,
				Brief:         "Long chat",
				Status:        ConversationStatus{Open: true, Members: map[string]MemberStatus{"Lead": {Role: "lead", State: "joined"}}},
				Messages:      all[start:end],
				More:          end < len(all),
				Cursor:        int64(end),
				TotalMessages: &totalCount,
			}, nil
		},
	}

	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChatAt(testConversation, 0)
	m = drive(t, next.(model), cmd)

	// Only the first page should be loaded, not eagerly drained.
	if len(fake.shows) != 1 || len(m.chat.messages) != pageSize || !m.chat.hasNewer {
		t.Fatalf("shows=%d len=%d hasNewer=%v, want 1 page with hasNewer=true", len(fake.shows), len(m.chat.messages), m.chat.hasNewer)
	}

	// Scroll down near the bottom to auto-load the next page.
	for range 15 {
		var scrollCmd tea.Cmd
		m, scrollCmd = m.scrollTranscript("pgdown")
		m = drive(t, m, scrollCmd)
	}
	if len(fake.shows) != 2 || fake.shows[1].After != pageSize {
		t.Fatalf("shows after scroll down = %+v, want second call with after=%d", fake.shows, pageSize)
	}
	if len(m.chat.messages) != total || m.chat.hasNewer {
		t.Fatalf("len=%d hasNewer=%v, want %d and false", len(m.chat.messages), m.chat.hasNewer, total)
	}
}

func TestTeamPanelShowsArchivedSectionAndPreservesOldParticipants(t *testing.T) {
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Team lifecycle",
			Status: ConversationStatus{
				Open: true,
				Members: map[string]MemberStatus{
					"Lead":      {Role: "lead", State: "replied"},
					"Reviewer":  {Role: "worker", State: "replied"},
					"OldWorker": {Role: "worker", State: "retired"},
					"Human":     {Role: "human", State: "human"},
				},
			},
		},
	}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd).toggleArchived()

	panel := ansi.Strip(strings.Join(m.teamView(teamWidth, 28), "\n"))
	teamIndex := strings.Index(panel, "TEAM")
	archivedIndex := strings.Index(panel, "ARCHIVED")
	oldIndex := strings.Index(panel, "OldWorker")
	if teamIndex < 0 || archivedIndex < 0 || oldIndex < 0 {
		t.Fatalf("missing TEAM/ARCHIVED/OldWorker in panel:\n%s", panel)
	}
	if !(teamIndex < archivedIndex && archivedIndex < oldIndex) {
		t.Fatalf("OldWorker should appear under ARCHIVED section:\n%s", panel)
	}
	if !strings.Contains(panel, "archived") {
		t.Fatalf("archived detail missing:\n%s", panel)
	}

	// Clicking the archived row selects OldWorker accurately.
	var archivedRow teamRow
	for _, row := range m.teamRows() {
		if row.name == "OldWorker" {
			archivedRow = row
		}
	}
	if archivedRow.name == "" {
		t.Fatal("OldWorker missing from teamRows")
	}
	listW, centerW, _ := m.columnWidths()
	teamX := listW + 1 + centerW + 2
	m, _ = click(t, m, teamX, 1+archivedRow.top)
	if m.selectedMember() != "OldWorker" {
		t.Fatalf("selectedMember = %q, want OldWorker", m.selectedMember())
	}

	// Archived participants cannot be made lead or retired again.
	m, _ = press(t, m, "m")
	if len(fake.roleChanges) != 0 || !strings.Contains(m.popup.text, "archived") {
		t.Fatalf("unexpected make-lead on archived: changes=%+v status=%q", fake.roleChanges, m.popup.text)
	}
	m, _ = press(t, m, "d")
	if m.chat.retiring != "" || !strings.Contains(m.popup.text, "already archived") {
		t.Fatalf("unexpected retire on archived: retiring=%q status=%q", m.chat.retiring, m.popup.text)
	}
}

func TestMakeLeadAndRoleSlashCommandsUpdateRunningTUI(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	showsBefore := len(fake.shows)
	listsBefore := len(fake.lists)

	// Tab to team panel, select Reviewer, press m to make lead.
	m, _ = press(t, m, "tab")
	m, _ = press(t, m, "down")
	var cmd tea.Cmd
	m, cmd = press(t, m, "m")
	m = drive(t, m, cmd)

	if len(fake.roleChanges) != 1 || fake.roleChanges[0] != (SetRoleRequest{
		Directory: "/work", Conversation: testConversation, Name: "Reviewer", Role: "lead",
	}) {
		t.Fatalf("roleChanges = %+v, want Reviewer -> lead", fake.roleChanges)
	}
	if len(fake.shows) <= showsBefore || len(fake.lists) <= listsBefore {
		t.Fatalf("expected immediate show and list refresh after role change: shows=%d lists=%d", len(fake.shows), len(fake.lists))
	}

	// Slash commands /lead and /role work and validate role values.
	m, _ = press(t, m, "esc")
	m = typeText(t, m, "/role Reviewer co-lead")
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)
	if len(fake.roleChanges) != 2 || fake.roleChanges[1].Role != "co-lead" {
		t.Fatalf("roleChanges = %+v, want co-lead", fake.roleChanges)
	}

	m = typeText(t, m, "/lead Lead")
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)
	if len(fake.roleChanges) != 3 || fake.roleChanges[2].Name != "Lead" || fake.roleChanges[2].Role != "lead" {
		t.Fatalf("roleChanges = %+v, want Lead -> lead", fake.roleChanges)
	}

	m = typeText(t, m, "/role Lead boss")
	m, _ = press(t, m, "enter")
	if len(fake.roleChanges) != 3 || !strings.Contains(m.popup.text, "Usage: /role") {
		t.Fatalf("invalid role was not rejected: changes=%+v status=%q", fake.roleChanges, m.popup.text)
	}
}

func TestLivePollReflectsAddedArchivedAndLeadChangedParticipants(t *testing.T) {
	initial := ConversationView{
		Conversation: testConversation,
		Brief:        "Live roster updates",
		Status: ConversationStatus{
			Open: true,
			Members: map[string]MemberStatus{
				"Lead":     {Role: "lead", State: "replied"},
				"Reviewer": {Role: "worker", State: "replied"},
				"Human":    {Role: "human", State: "human"},
			},
		},
		Messages: []TranscriptMessage{{ID: 1, Label: "Lead", Body: "Kickoff", Created: 1795000000}},
		Cursor:   1,
	}
	updated := ConversationView{
		Conversation: testConversation,
		Brief:        "Live roster updates",
		Status: ConversationStatus{
			Open: true,
			Members: map[string]MemberStatus{
				"Lead":     {Role: "worker", State: "replied"},
				"Reviewer": {Role: "worker", State: "retired"},
				"Scout":    {Role: "lead", State: "joined"},
				"Human":    {Role: "human", State: "human"},
			},
		},
		Cursor: 1,
	}
	call := 0
	fake := &fakeOperator{
		showFunc: func(ShowRequest) (ConversationView, error) {
			call++
			if call == 1 {
				return initial, nil
			}
			return updated, nil
		},
	}

	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd)

	// Select Lead in the team panel before the lead change arrives.
	m, _ = press(t, m, "tab")
	if m.selectedMember() != "Lead" {
		t.Fatalf("initial selectedMember = %q, want Lead", m.selectedMember())
	}
	versionBefore := m.chat.version
	listsBefore := len(fake.lists)

	// One poll tick brings the added Scout (new lead), demoted Lead, and archived Reviewer.
	next, cmd = m.Update(tickMsg(time.Now()))
	m = drive(t, next.(model), cmd)

	if m.chat.version == versionBefore {
		t.Fatal("transcript version did not bump when roles changed")
	}
	if len(fake.lists) <= listsBefore {
		t.Fatal("sidebar list did not refresh immediately on roster change")
	}
	if m.selectedMember() != "Lead" {
		t.Fatalf("selection drifted after reorder: got %q, want Lead", m.selectedMember())
	}
	panel := ansi.Strip(strings.Join(m.toggleArchived().teamView(teamWidth, 28), "\n"))
	scoutPos := strings.Index(panel, "Scout lead")
	leadPos := strings.Index(panel, "Lead worker")
	archivedPos := strings.Index(panel, "ARCHIVED")
	reviewerPos := strings.Index(panel, "Reviewer worker")
	if scoutPos < 0 || leadPos < 0 || archivedPos < 0 || reviewerPos < 0 {
		t.Fatalf("panel missing expected live roster updates:\n%s", panel)
	}
	if !(scoutPos < leadPos && leadPos < archivedPos && archivedPos < reviewerPos) {
		t.Fatalf("unexpected team order after live update:\n%s", panel)
	}
}

func TestInviteImmediatelyRefreshesTeamAndShowsNewSlot(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	showsBefore := len(fake.shows)
	listsBefore := len(fake.lists)

	m = typeText(t, m, "/invite Scout")
	var cmd tea.Cmd
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)

	if !m.inviteWindow.open {
		t.Fatal("invite window did not open after /invite")
	}
	if len(fake.shows) <= showsBefore || len(fake.lists) <= listsBefore {
		t.Fatalf("expected immediate show and list refresh after invite: shows=%d lists=%d", len(fake.shows), len(fake.lists))
	}
}

func TestTeamViewRendersTokenUsageBarsAndBalanceWarning(t *testing.T) {
	claudeUsed := 78.0
	claudeRem := int64(22000)
	geminiUsed := 12.0
	geminiRem := int64(880000)
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Balance work across models",
			Status: ConversationStatus{
				Open: true,
				Members: map[string]MemberStatus{
					"Claude": {
						Role:  "lead",
						State: "replied",
						Usage: &MemberUsage{
							Model:              "claude-sonnet-4-6",
							ConversationTokens: 78000,
							ModelTokensUsed:    78000,
							Source:             "reported",
							UsedPercent:        &claudeUsed,
							RemainingTokens:    &claudeRem,
							Status:             "warning",
						},
					},
					"Gemini": {
						Role:  "worker",
						State: "replied",
						Usage: &MemberUsage{
							Model:              "gemini-2.5-pro",
							ConversationTokens: 120000,
							ModelTokensUsed:    120000,
							Source:             "plugin",
							UsedPercent:        &geminiUsed,
							RemainingTokens:    &geminiRem,
							Status:             "ok",
						},
					},
					"Human": {Role: "human", State: "human"},
				},
				TokenBalance: &TokenBalance{
					Balanced:            false,
					ImbalanceGapPercent: 66.0,
					Overloaded:          []string{"Claude"},
					Preferred:           []string{"Gemini"},
					Recommendation:      "Prefer assigning new tasks or handoffs to Gemini.",
				},
			},
		},
	}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd)

	panel := ansi.Strip(strings.Join(m.teamView(32, 28), "\n"))
	if !strings.Contains(panel, "78% · 22.0k left") {
		t.Fatalf("expected Claude usage bar in team panel:\n%s", panel)
	}
	if !strings.Contains(panel, "12% · 880.0k left") {
		t.Fatalf("expected Gemini usage bar in team panel:\n%s", panel)
	}
	if !strings.Contains(panel, "TOKEN BALANCE") || !strings.Contains(panel, "prefer Gemini") {
		t.Fatalf("expected TOKEN BALANCE warning in team panel:\n%s", panel)
	}
	rows := m.teamRows()
	if len(rows) < 2 || rows[0].size != 4 || rows[1].size != 4 {
		t.Fatalf("expected 4-line team rows for members with a model and usage, got %+v", rows)
	}
}

func TestTeamViewShowsActualAndEstimatedTokensAndChatTotal(t *testing.T) {
	claudeActual := int64(60000)
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Track real spend",
			Status: ConversationStatus{
				Open: true,
				Members: map[string]MemberStatus{
					"Claude": {Role: "lead", State: "replied", Usage: &MemberUsage{
						ConversationTokens: 60000, EstimatedTokens: 4000, ActualTokens: &claudeActual, Source: "reported",
					}},
					"Gemini": {Role: "worker", State: "replied", Usage: &MemberUsage{
						ConversationTokens: 3000, EstimatedTokens: 3000, Source: "estimated",
					}},
					"Human": {Role: "human", State: "human"},
				},
				TokenUsage: &ConversationUsage{
					TotalTokens: 63000, ActualTokens: 60000, EstimatedTokens: 3000, ActualMembers: 1, EstimatedMembers: 1,
				},
			},
		},
	}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd)

	panel := ansi.Strip(strings.Join(m.teamView(40, 28), "\n"))
	for _, want := range []string{
		"60.0k tok · est 4.0k",
		"3.0k tok est",
		"CHAT TOKENS 63.0k",
		"60.0k actual · 3.0k est (1 unreported)",
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("expected %q in team panel:\n%s", want, panel)
		}
	}
}

func TestBudgetSlashCommandSendsOperatorSetMemberBudget(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)

	m = typeText(t, m, "/budget Reviewer 250k gemini-2.5-pro")
	var cmd tea.Cmd
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)

	if len(fake.budgets) != 1 {
		t.Fatalf("expected 1 SetMemberBudget call, got %d", len(fake.budgets))
	}
	req := fake.budgets[0]
	if req.Name != "Reviewer" || req.Model != "gemini-2.5-pro" || req.TokenBudget == nil || *req.TokenBudget != 250000 {
		t.Fatalf("unexpected SetMemberBudget request: %+v", req)
	}
}

func TestTasksTabShowsListDetailsFiltersAndJumpsToChat(t *testing.T) {
	replyID := int64(93)
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Build the tasks tab",
			Status: ConversationStatus{
				Open: true,
				Members: map[string]MemberStatus{
					"Lead":            {Role: "lead", State: "replied"},
					"claude designer": {Role: "worker", State: "replied", OpenTasks: []OpenTask{{ID: 93}}},
					"Gemini Neon":     {Role: "worker", State: "replied"},
				},
			},
			Messages: []TranscriptMessage{
				{ID: 93, Label: "Lead", To: "claude designer", Kind: "task", Body: "Design the tasks tab layout\nInclude list and detail panes.", Created: 1700000000},
				{ID: 105, Label: "Lead", To: "Gemini Neon", Kind: "task", Body: "Wire operator_tasks endpoint", Created: 1700000100},
			},
		},
		tasks: TaskList{
			Tasks: []TaskDetail{
				{
					ID:               93,
					Title:            "Design the tasks tab layout",
					Body:             "Design the tasks tab layout\nInclude list and detail panes.",
					Author:           "Lead",
					Owner:            "claude designer",
					State:            "open",
					Created:          1700000000,
					Updated:          1700000060,
					PendingHandoffTo: "Gemini Neon",
					Events: []TaskEvent{
						{ID: 93, Kind: "task", Author: "Lead", To: "claude designer", Body: "Design the tasks tab layout\nInclude list and detail panes.", Created: 1700000000},
						{ID: 99, Kind: "handoff", Author: "claude designer", To: "Gemini Neon", Body: "Passing the UI polish to Gemini Neon.", Created: 1700000060, ReplyTo: &replyID},
					},
				},
				{
					ID:      105,
					Title:   "Wire operator_tasks endpoint",
					Body:    "Wire operator_tasks endpoint",
					Author:  "Lead",
					Owner:   "Gemini Neon",
					State:   "done",
					Created: 1700000100,
					Updated: 1700000200,
					Events: []TaskEvent{
						{ID: 105, Kind: "task", Author: "Lead", To: "Gemini Neon", Body: "Wire operator_tasks endpoint", Created: 1700000100},
						{ID: 110, Kind: "done", Author: "Gemini Neon", Body: "Endpoint and schema ready.", Created: 1700000200, ReplyTo: &replyID},
					},
				},
			},
		},
	}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd)

	// Header shows the Tasks tab button with open task count from status before loading tasks.
	screenText := ansi.Strip(m.View().Content)
	if !strings.Contains(screenText, "Tasks (1)") {
		t.Fatalf("expected Tasks (1) tab button in header:\n%s", screenText)
	}

	// Switch to Tasks tab with ctrl+t.
	m, cmd = press(t, m, "ctrl+t")
	m = drive(t, m, cmd)
	if m.chat.tab != tabTasks || len(fake.taskCalls) != 1 {
		t.Fatalf("expected tabTasks and 1 Tasks() call, got tab=%v calls=%d", m.chat.tab, len(fake.taskCalls))
	}

	tasksText := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"Open (1)", "All (2)", "Done (1)",
		"#93", "Design the tasks tab layout",
		"HANDOFF → Gemini Neon",
		"Include list and detail panes.",
		"ACTIVITY (1)",
		"Passing the UI polish to Gemini Neon.",
	} {
		if !strings.Contains(tasksText, want) {
			t.Fatalf("Tasks tab missing %q:\n%s", want, tasksText)
		}
	}

	// Cycle filter with 'f' from Open -> All -> Done, and select the done task.
	m, _ = press(t, m, "f")
	if m.chat.tasks.filter != filterAll || len(m.filteredTasks()) != 2 {
		t.Fatalf("expected filterAll with 2 tasks, got filter=%v count=%d", m.chat.tasks.filter, len(m.filteredTasks()))
	}
	m, _ = press(t, m, "down")
	selected, _, ok := m.selectedTask()
	if !ok || selected.ID != 105 {
		t.Fatalf("expected task #105 selected after down key, got %+v", selected)
	}
	allText := ansi.Strip(m.View().Content)
	if !strings.Contains(allText, "Endpoint and schema ready.") {
		t.Fatalf("expected task #105 activity details after selecting it:\n%s", allText)
	}

	// Pressing enter on the selected task jumps back to the Chat tab at that message ID.
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)
	if m.chat.tab != tabChat || m.chat.highlight != 105 {
		t.Fatalf("expected jump to Chat tab highlighting #105, got tab=%v highlight=%d", m.chat.tab, m.chat.highlight)
	}

	// Clicking an open task row in the right-hand TEAM panel opens it in the Tasks tab.
	teamRows := m.teamTaskRows(28)
	if len(teamRows) != 1 || teamRows[0].id != 93 {
		t.Fatalf("expected 1 teamTaskRow for #93, got %+v", teamRows)
	}
	m, cmd = click(t, m, 130, 1+teamRows[0].row)
	m = drive(t, m, cmd)
	if m.chat.tab != tabTasks || m.chat.tasks.selectedID != 93 {
		t.Fatalf("clicking team open task did not open #93 in Tasks tab: tab=%v selected=%d", m.chat.tab, m.chat.tasks.selectedID)
	}
}

func rightClick(t *testing.T, m model, x int, y int) (model, tea.Cmd) {
	t.Helper()
	next, command := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
	return next.(model), command
}

func TestRightClickTeamMemberShowsContextOperationsAndRunsThem(t *testing.T) {
	fake := &fakeOperator{}
	m := sized(chatModel(t, fake), 140, 30)

	var reviewer teamRow
	for _, row := range m.teamRows() {
		if row.name == "Reviewer" {
			reviewer = row
		}
	}
	// Right-click on Reviewer in the team panel.
	m, cmd := rightClick(t, m, 130, 1+reviewer.top)
	m = drive(t, m, cmd)
	if !m.contextMenu.open || m.selectedMember() != "Reviewer" {
		t.Fatalf("expected context menu open for Reviewer, got open=%v selected=%q", m.contextMenu.open, m.selectedMember())
	}
	rendered := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"Reviewer (worker)",
		"Message @Reviewer",
		"Ask ?@Reviewer",
		"Copy join prompt",
		"Make lead",
		"Make co-lead",
		"Set token budget…",
		"Retire member…",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("context menu missing %q:\n%s", want, rendered)
		}
	}

	// Left-click the first item ("Message @Reviewer") inside the context menu.
	_, top, left, itemOf := m.contextMenuLayout(28)
	firstItemRow := -1
	for idx, itemIdx := range itemOf {
		if itemIdx == 0 {
			firstItemRow = top + idx
			break
		}
	}
	m, cmd = click(t, m, left+3, 1+firstItemRow)
	m = drive(t, m, cmd)
	if m.contextMenu.open {
		t.Fatal("expected context menu to close after clicking an item")
	}
	if m.chat.focus != focusInput || m.chat.input.Value() != "@Reviewer " {
		t.Fatalf("expected composer prefilled with '@Reviewer ', got focus=%v value=%q", m.chat.focus, m.chat.input.Value())
	}

	// Right-click Reviewer again and press shortcut 'm' to Make lead.
	m, cmd = rightClick(t, m, 130, 1+reviewer.top)
	m = drive(t, m, cmd)
	m, cmd = press(t, m, "m")
	m = drive(t, m, cmd)
	if m.contextMenu.open || len(fake.roleChanges) != 1 || fake.roleChanges[0].Name != "Reviewer" || fake.roleChanges[0].Role != "lead" {
		t.Fatalf("expected SetRole(Reviewer, lead) from context menu, got open=%v calls=%+v", m.contextMenu.open, fake.roleChanges)
	}

	// Right-click Reviewer and click outside the menu to dismiss it.
	m, cmd = rightClick(t, m, 130, 1+reviewer.top)
	m = drive(t, m, cmd)
	if !m.contextMenu.open {
		t.Fatal("expected context menu open")
	}
	m, _ = click(t, m, 2, 2)
	if m.contextMenu.open {
		t.Fatal("expected clicking outside to close the context menu")
	}
}

func TestRightClickChatListItemArchivesOrOpensConversation(t *testing.T) {
	fake := &fakeOperator{list: ConversationList{Conversations: []ConversationSummary{
		{Conversation: "open-1", Title: "Open chat", Open: true},
		{Conversation: "closed-2", Title: "Closed chat", Open: false},
	}}}
	m := sized(newModel(fake, "/work"), 120, 30)
	m = drive(t, m, m.loadList())
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "enter")

	// Right-click the closed chat under the CLOSED header: open chat rows 2-4, header rows 5-6.
	m, cmd := rightClick(t, m, 5, 1+2+3+2)
	m = drive(t, m, cmd)
	if !m.contextMenu.open || m.list.selected != 1 {
		t.Fatalf("expected context menu on second chat, got open=%v selected=%d", m.contextMenu.open, m.list.selected)
	}
	rendered := ansi.Strip(m.View().Content)
	if !strings.Contains(rendered, "Archive chat") || !strings.Contains(rendered, "Copy title") {
		t.Fatalf("expected Archive chat and Copy title in list context menu:\n%s", rendered)
	}

	// Press 'a' shortcut in the context menu to archive the closed chat.
	m, cmd = press(t, m, "a")
	m = drive(t, m, cmd)
	if m.contextMenu.open || len(fake.archives) != 1 || fake.archives[0].Conversation != "closed-2" || !fake.archives[0].Archived {
		t.Fatalf("expected archive request for closed-2, got open=%v archives=%+v", m.contextMenu.open, fake.archives)
	}
}

func TestRightClickTranscriptMessageCopiesAndOpensTask(t *testing.T) {
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Inspect context menu on messages",
			Status: ConversationStatus{
				Open: true,
				Members: map[string]MemberStatus{
					"Lead":   {Role: "lead", State: "replied"},
					"Worker": {Role: "worker", State: "replied"},
					"Human":  {Role: "human", State: "human"},
				},
			},
			Messages: []TranscriptMessage{
				{ID: 42, Label: "Lead", To: "Worker", Kind: "task", Body: "Implement right-click menu", Created: 1700000000},
			},
		},
		tasks: TaskList{
			Tasks: []TaskDetail{
				{ID: 42, Title: "Implement right-click menu", Body: "Implement right-click menu", Author: "Lead", Owner: "Worker", State: "open"},
			},
		},
	}
	m := sized(newModel(fake, "/work"), 140, 30)
	var copied []string
	m.clipboard = func(text string) error {
		copied = append(copied, text)
		return nil
	}
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd)

	// In a short chat, the first message starts right below chatHeader.
	_, center, _ := m.columnWidths()
	msgRow := len(m.chatHeader(center))

	m, cmd = rightClick(t, m, 60, 1+msgRow)
	m = drive(t, m, cmd)
	if !m.contextMenu.open || m.chat.highlight != 42 {
		t.Fatalf("expected context menu on message #42 with highlight, got open=%v highlight=%d", m.contextMenu.open, m.chat.highlight)
	}
	menuView := ansi.Strip(m.View().Content)
	for _, want := range []string{"Message #42 · Lead", "Copy message", "Message @Lead", "Open Task #42 in Tasks"} {
		if !strings.Contains(menuView, want) {
			t.Fatalf("message context menu missing %q:\n%s", want, menuView)
		}
	}

	// Press enter on the first item ("Copy message").
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)
	if len(copied) != 1 || copied[0] != "Implement right-click menu" {
		t.Fatalf("expected copied message body, got %v", copied)
	}

	// Right-click message #42 again and choose "Open Task #42 in Tasks".
	m, cmd = rightClick(t, m, 60, 1+msgRow)
	m = drive(t, m, cmd)
	for index, item := range m.contextMenu.items {
		if item.id == "msg:open-task" {
			m.contextMenu.selected = index
		}
	}
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)
	if m.chat.tab != tabTasks || m.chat.tasks.selectedID != 42 {
		t.Fatalf("expected Tasks tab with task #42 selected, got tab=%v selected=%d", m.chat.tab, m.chat.tasks.selectedID)
	}
}

func TestTransientStatusExpiresAndRestoresLiveTopBar(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	m = m.inform("Copied the prompt for claude designer. Paste it into its chat.")
	if m.status == "" || m.statusTicks != statusDurationTicks {
		t.Fatalf("expected transient status with %d ticks, got status=%q ticks=%d", statusDurationTicks, m.status, m.statusTicks)
	}

	for range statusDurationTicks {
		next, cmd := m.Update(tickMsg(time.Now()))
		m = drive(t, next.(model), cmd)
	}
	if m.status != "" || m.statusTicks != 0 {
		t.Fatalf("expected transient status to clear after %d ticks, got status=%q ticks=%d", statusDurationTicks, m.status, m.statusTicks)
	}
	if !strings.Contains(ansi.Strip(m.topBar()), "core running") {
		t.Fatalf("expected topBar to show live core running status, got %q", ansi.Strip(m.topBar()))
	}
}

func TestQueuedRefreshDrainsWhenPollInFlightAndSyncsSidebar(t *testing.T) {
	fake := &fakeOperator{
		list: ConversationList{
			Conversations: []ConversationSummary{
				{Conversation: testConversation, Title: "Review the retry rule.", Open: true, Members: 3, Joined: 3},
			},
		},
	}
	m := chatModel(t, fake)
	m = drive(t, m, m.loadList())

	// Simulate a background poll already in flight when a retirement completes.
	m.chat.polling = true
	fake.view.Status.Members["Reviewer"] = MemberStatus{Role: "worker", State: "retired"}
	next, cmd := m.Update(retiredMsg{participant: "Reviewer"})
	m = next.(model)
	if !m.chat.refreshPending {
		t.Fatal("expected refreshPending to be queued while a poll is in flight")
	}

	// Finish the older in-flight poll; flushPendingShow must immediately trigger the queued refresh.
	staleView := fake.view
	staleView.Status.Members = map[string]MemberStatus{
		"Lead":     {Role: "lead", State: "joined"},
		"Reviewer": {Role: "worker", State: "joined"},
		"Human":    {Role: "human", State: "human"},
	}
	next, cmd = m.Update(showMsg{conversation: testConversation, mode: showAfter, after: m.chat.cursor, view: staleView})
	m = drive(t, next.(model), cmd)

	if m.chat.view.Status.Members["Reviewer"].State != "retired" {
		t.Fatalf("expected queued refresh to load retired state, got %+v", m.chat.view.Status.Members["Reviewer"])
	}
	if len(m.list.items) != 1 || m.list.items[0].Members != 1 || m.list.items[0].Joined != 1 {
		t.Fatalf("expected sidebar summary to exclude retired member immediately, got %+v", m.list.items)
	}
}

func TestProviderModelContextAndEndedLaunchStatesInTeamPanel(t *testing.T) {
	staleIdle := int64(3600)
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Provider and launch state reality check",
			Status: ConversationStatus{
				Open: true,
				Members: map[string]MemberStatus{
					"Lead Grok":   {Role: "lead", State: "replied", IdleSeconds: &staleIdle},
					"Claude Opus": {Role: "worker", State: "replied", Usage: &MemberUsage{Model: "claude-opus-4-6", ConversationTokens: 12000, Source: "reported"}},
					"Gemini Neon": {Role: "worker", State: "replied", LaunchState: "finished", OpenTasks: []OpenTask{{ID: 7}}},
					"GLM Flash":   {Role: "worker", State: "retired"},
					"Human":       {Role: "human", State: "human"},
				},
			},
		},
	}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd).toggleArchived()

	panel := ansi.Strip(strings.Join(m.teamView(36, 28), "\n"))
	for _, want := range []string{
		"Lead Grok lead\n   xai/grok\n",
		"Claude Opus worker\n   anthropic/claude-opus-4-6\n",
		"Gemini Neon worker\n   google/gemini-neon\n",
		"run finished",
		"GLM Flash worker\n   zai/glm-flash\n",
		"#7 Gemini Neon run finished",
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("team panel missing %q:\n%s", want, panel)
		}
	}
}

func TestReopenClosedChatViaButtonAndSlashCommand(t *testing.T) {
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Demo prep",
			Archived:     true,
			Status: ConversationStatus{
				Open: false,
				Members: map[string]MemberStatus{
					"Lead":  {Role: "lead", State: "replied"},
					"Human": {Role: "human", State: "human"},
				},
			},
		},
	}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	m = drive(t, next.(model), cmd)

	buttons, _ := m.headerButtons()
	plainButtons := ansi.Strip(buttons)
	if !strings.Contains(plainButtons, "Reopen") || !strings.Contains(plainButtons, "Unarchive") {
		t.Fatalf("closed archived header buttons = %q", plainButtons)
	}

	next, cmd = m.clickZone("reopen")
	m = drive(t, next.(model), cmd)
	if len(fake.reopens) != 1 || fake.reopens[0].Conversation != testConversation || fake.reopens[0].Note != "" {
		t.Fatalf("reopen button requests = %+v", fake.reopens)
	}

	m = typeText(t, m, "/reopen Need one more check")
	next, cmd = press(t, m, "enter")
	m = drive(t, next.(model), cmd)
	if len(fake.reopens) != 2 || fake.reopens[1].Note != "Need one more check" {
		t.Fatalf("/reopen requests = %+v", fake.reopens)
	}
}

// archivedTeamModel opens a chat with one archived member that still owns an open task.
func archivedTeamModel(t *testing.T) model {
	t.Helper()
	fake := &fakeOperator{
		view: ConversationView{
			Conversation: testConversation,
			Brief:        "Review the retry rule.",
			Status: ConversationStatus{
				Open: true,
				Members: map[string]MemberStatus{
					"Lead":      {Role: "lead", State: "replied"},
					"Reviewer":  {Role: "worker", State: "replied"},
					"OldWorker": {Role: "worker", State: "retired", OpenTasks: []OpenTask{{ID: 41}}},
					"Human":     {Role: "human", State: "human"},
				},
			},
		},
	}
	m := sized(newModel(fake, "/work"), 140, 30)
	next, cmd := m.openChat(testConversation)
	return drive(t, next.(model), cmd)
}

func TestArchivedSectionStartsCollapsedAndTogglesWithKey(t *testing.T) {
	m := archivedTeamModel(t)
	next, _ := m.setChatFocus(focusTeam)
	m = next.(model)
	panel := ansi.Strip(strings.Join(m.teamView(teamWidth, 28), "\n"))
	if !strings.Contains(panel, "▸ ARCHIVED (1)") {
		t.Fatalf("section should start collapsed:\n%s", panel)
	}
	if strings.Contains(panel, "✕ OldWorker") || slices.Contains(m.slotNames(), "OldWorker") {
		t.Fatalf("collapsed section still draws the archived member:\n%s", panel)
	}
	if !strings.Contains(panel, "#41 OldWorker archived") {
		t.Fatalf("open task of the archived member should stay visible:\n%s", panel)
	}

	m, _ = press(t, m, "a")
	panel = ansi.Strip(strings.Join(m.teamView(teamWidth, 28), "\n"))
	if !strings.Contains(panel, "▾ ARCHIVED") || !strings.Contains(panel, "✕ OldWorker") {
		t.Fatalf("expanded section should draw the archived member:\n%s", panel)
	}
	for range len(m.slotNames()) {
		m, _ = press(t, m, "down")
	}
	if m.selectedMember() != "OldWorker" {
		t.Fatalf("selected %q, want the archived member", m.selectedMember())
	}

	m, _ = press(t, m, "a")
	if slices.Contains(m.slotNames(), "OldWorker") || m.selectedMember() != "Reviewer" {
		t.Fatalf("selection should move to a visible member, got %q of %v", m.selectedMember(), m.slotNames())
	}
}

func TestClickingArchivedHeaderTogglesSectionAndKeepsTaskRowsAligned(t *testing.T) {
	m := archivedTeamModel(t)
	_, headerRow := m.teamLayout()
	if headerRow < 0 {
		t.Fatal("no ARCHIVED header row")
	}
	listW, centerW, _ := m.columnWidths()
	teamX := listW + 1 + centerW + 2

	lines := strings.Split(ansi.Strip(strings.Join(m.teamView(teamWidth, 28), "\n")), "\n")
	if !strings.Contains(lines[headerRow], "▸ ARCHIVED (1)") {
		t.Fatalf("header row %d = %q", headerRow, lines[headerRow])
	}
	taskRows := m.teamTaskRows(28)
	if len(taskRows) != 1 || !strings.Contains(lines[taskRows[0].row], "#41") {
		t.Fatalf("task row %+v does not match the drawn panel:\n%s", taskRows, strings.Join(lines, "\n"))
	}

	m, _ = click(t, m, teamX, 1+headerRow)
	if m.archivedCollapsed {
		t.Fatal("click on the ARCHIVED header should expand the section")
	}

	m, _ = click(t, m, teamX, 1+headerRow)
	if !m.archivedCollapsed {
		t.Fatal("second click should collapse the section")
	}
}

func TestProviderModelLineIsCutWithEllipsisInNarrowTeamPanel(t *testing.T) {
	m := archivedTeamModel(t)
	member := MemberStatus{Role: "worker", State: "replied", Usage: &MemberUsage{Model: "claude-opus-4-6"}}
	block := m.memberBlock("Claude Opus", member, "", 20)
	modelLine := ansi.Strip(block[1])
	if ansi.StringWidth(modelLine) > 20 || !strings.HasSuffix(modelLine, "…") {
		t.Fatalf("model line %q should fit 20 columns and end with an ellipsis", modelLine)
	}
}

func TestTaskViewsShowDueTimeAndOverdueState(t *testing.T) {
	due := time.Date(2026, 11, 23, 15, 30, 0, 0, time.Local).Unix()
	task := TaskDetail{ID: 7, Title: "Review retries", Body: "Review retries", Author: "Lead", Owner: "Worker", State: "open", Due: due, Overdue: true}
	m := model{}

	detail := ansi.Strip(strings.Join(m.taskDetailLines(task, 120), "\n"))
	for _, want := range []string{"OVERDUE", "Due 15:30"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("task detail missing %q:\n%s", want, detail)
		}
	}
	if pill := ansi.Strip(taskStatePill(task)); pill != "overdue" {
		t.Fatalf("expected overdue pill, got %q", pill)
	}
	member := MemberStatus{Role: "worker", State: "replied", OpenTasks: []OpenTask{{ID: 7, Due: due, Overdue: true}}}
	if rows := ansi.Strip(strings.Join(m.memberTasks("Worker", member, 40), "\n")); !strings.Contains(rows, "#7 Worker overdue") {
		t.Fatalf("team panel missing overdue task:\n%s", rows)
	}

	task.Overdue = false
	task.Due = 0
	onTime := ansi.Strip(strings.Join(m.taskDetailLines(task, 120), "\n"))
	if strings.Contains(onTime, "OVERDUE") || strings.Contains(onTime, "Due ") {
		t.Fatalf("task without a due time shows one:\n%s", onTime)
	}
}

// chatWithOpenTasks returns an open chat in which the reviewer owns two unfinished tasks.
func chatWithOpenTasks(t *testing.T, fake *fakeOperator) model {
	t.Helper()
	m := chatModel(t, fake)
	fake.view.Status.Members["Reviewer"] = MemberStatus{
		Role: "worker", State: "joined", OpenTasks: []OpenTask{{ID: 3}, {ID: 5}},
	}
	m, command := m.pollChat()
	return drive(t, m, command)
}

func TestCloseWithOpenTasksAlertsAndDeprecatesOnConfirm(t *testing.T) {
	fake := &fakeOperator{}
	m := sized(chatWithOpenTasks(t, fake), 120, 30)

	m, _ = press(t, m, "ctrl+x")
	if !m.taskAlert.open || m.chat.closing {
		t.Fatalf("close with open tasks did not alert: alert=%+v closing=%v", m.taskAlert, m.chat.closing)
	}
	if content := ansi.Strip(m.View().Content); !strings.Contains(content, "2 tasks are still open") {
		t.Fatalf("alert does not name the open tasks:\n%s", content)
	}
	m, _ = press(t, m, "enter")
	if !m.taskAlert.open || len(fake.closes) != 0 {
		t.Fatalf("enter passed the alert: alert=%+v closes=%+v", m.taskAlert, fake.closes)
	}
	m, _ = press(t, m, "esc")
	if m.taskAlert.open || m.chat.closing || len(fake.closes) != 0 {
		t.Fatalf("esc did not cancel the close: alert=%+v closes=%+v", m.taskAlert, fake.closes)
	}

	m, _ = press(t, m, "ctrl+x")
	m = typeText(t, m, "d")
	if m.taskAlert.open || !m.chat.closing {
		t.Fatalf("d did not continue to the close confirmation: alert=%+v", m.taskAlert)
	}
	if content := ansi.Strip(m.View().Content); !strings.Contains(content, "open tasks become deprecated") {
		t.Fatalf("confirmation does not announce the deprecation:\n%s", content)
	}
	m, command := press(t, m, "enter")
	drive(t, m, command)
	want := CloseRequest{Directory: "/work", Conversation: testConversation, DeprecateTasks: true}
	if len(fake.closes) != 1 || fake.closes[0] != want {
		t.Fatalf("closes = %+v, want %+v", fake.closes, want)
	}
}

func TestOpenTasksAlertOpensTheTasksTab(t *testing.T) {
	fake := &fakeOperator{}
	m := chatWithOpenTasks(t, fake)

	m, _ = press(t, m, "ctrl+x")
	m = typeText(t, m, "t")
	if m.taskAlert.open || m.chat.closing || m.chat.tab != tabTasks || m.chat.tasks.filter != filterOpen {
		t.Fatalf("t did not open the open tasks: alert=%+v tab=%v", m.taskAlert, m.chat.tab)
	}
	if len(fake.closes) != 0 {
		t.Fatalf("checking tasks closed the chat: %+v", fake.closes)
	}
}

func TestCancelledDeprecationDoesNotLeakIntoALaterClose(t *testing.T) {
	fake := &fakeOperator{}
	m := chatWithOpenTasks(t, fake)

	m, _ = press(t, m, "ctrl+x")
	m = typeText(t, m, "d")
	m, _ = press(t, m, "esc")
	if m.chat.closing || m.chat.deprecateOnClose || len(fake.closes) != 0 {
		t.Fatalf("esc kept the close: closing=%v deprecate=%v", m.chat.closing, m.chat.deprecateOnClose)
	}
}

func TestArchiveWithOpenTasksAlertsAndDeprecates(t *testing.T) {
	fake := &fakeOperator{
		list: ConversationList{Conversations: []ConversationSummary{{Conversation: "done", Title: "Done"}}},
		tasks: TaskList{Tasks: []TaskDetail{
			{ID: 1, State: "done"}, {ID: 2, State: "deprecated"}, {ID: 3, State: "blocked"},
		}},
	}
	m := sized(newModel(fake, "/work"), 120, 30)
	m = drive(t, m, m.loadList())
	m, _ = press(t, m, "enter")
	m, _ = press(t, m, "down")

	m, command := press(t, m, "a")
	m = drive(t, m, command)
	if !m.taskAlert.open || !m.taskAlert.archive || m.taskAlert.count != 1 || len(fake.archives) != 0 {
		t.Fatalf("archive with an open task did not alert: alert=%+v archives=%+v", m.taskAlert, fake.archives)
	}
	if content := ansi.Strip(m.View().Content); !strings.Contains(content, "1 task is still open") {
		t.Fatalf("alert does not name the open task:\n%s", content)
	}
	m, command = press(t, m, "esc")
	m = drive(t, m, command)
	if m.taskAlert.open || len(fake.archives) != 0 {
		t.Fatalf("esc archived the chat: %+v", fake.archives)
	}

	m, command = press(t, m, "a")
	m = drive(t, m, command)
	next, command := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	drive(t, next.(model), command)
	want := ArchiveRequest{Directory: "/work", Conversation: "done", Archived: true, DeprecateTasks: true}
	if len(fake.archives) != 1 || fake.archives[0] != want {
		t.Fatalf("archives = %+v, want %+v", fake.archives, want)
	}
}

func TestReissueSelectedReinvitesJoinedMembersOnly(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)
	for index, name := range m.slotNames() {
		m.chat.selected = index
		next, cmd := m.reissueSelected()
		m = drive(t, next.(model), cmd)
		last := fake.invites[len(fake.invites)-1]
		if last.Name != name || last.Reinvite != (name == "Lead") {
			t.Fatalf("invite request for %s = %+v", name, last)
		}
	}
}

func TestReinviteCommandSendsReinvite(t *testing.T) {
	fake := &fakeOperator{}
	m := chatModel(t, fake)

	m = typeText(t, m, "/reinvite Lead")
	var cmd tea.Cmd
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)

	if len(fake.invites) != 1 || fake.invites[0] != (InviteRequest{
		Directory: "/work", Conversation: testConversation, Name: "Lead", Reinvite: true,
	}) {
		t.Fatalf("invite requests = %+v", fake.invites)
	}
}

func TestPingAndRelaunchCommandsAndPresenceState(t *testing.T) {
	fake := &fakeOperator{plugins: PluginList{Plugins: []PluginSummary{{Name: "glm", Model: "zai/glm-5"}}}}
	m := chatModel(t, fake)
	m.plugins = []string{"glm"}

	// /ping dispatches operator_ping and displays the woken notice.
	m = typeText(t, m, "/ping Lead")
	var cmd tea.Cmd
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)
	if len(fake.pings) != 1 || fake.pings[0] != (PingRequest{Directory: "/work", Conversation: testConversation, Name: "Lead"}) {
		t.Fatalf("pings = %+v", fake.pings)
	}
	if !strings.Contains(m.popup.text, "Woke Lead from waiting read (2ms).") {
		t.Fatalf("popup = %q", m.popup.text)
	}
	m.popup = popupState{}

	// Pressing p in the team panel pings the selected member.
	m, _ = press(t, m, "tab")
	m, cmd = press(t, m, "p")
	m = drive(t, m, cmd)
	if len(fake.pings) != 2 || fake.pings[1].Name != "Lead" {
		t.Fatalf("team key p pings = %+v", fake.pings)
	}
	m.popup = popupState{}

	// Pressing l on a joined/ended member prefills /relaunch, and submitting dispatches Relaunch=true.
	m.chat.view.Status.Members["Reviewer"] = MemberStatus{Role: "worker", State: "replied", LaunchState: "failed"}
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "l")
	if got := m.chat.input.Value(); got != "/relaunch Reviewer glm" {
		t.Fatalf("prefilled input = %q, want /relaunch Reviewer glm", got)
	}
	m, cmd = press(t, m, "enter")
	m = drive(t, m, cmd)
	if len(fake.launches) != 1 || fake.launches[0] != (LaunchRequest{
		Directory: "/work", Conversation: testConversation, Name: "Reviewer", Plugin: "glm", Relaunch: true,
	}) {
		t.Fatalf("launches = %+v", fake.launches)
	}

	// Pressing l on a live manual chat does not prefill /relaunch.
	m.chat.focus = focusTeam
	m.chat.selected = 0 // Lead is a live manual chat with no LaunchState.
	m.chat.input.Reset()
	m, _ = press(t, m, "l")
	if got := m.chat.input.Value(); got != "" {
		t.Fatalf("prefilled input for live manual chat = %q, want empty", got)
	}
	if !strings.Contains(m.status, "live chat") {
		t.Fatalf("status = %q, want live chat notice", m.status)
	}

	// Live presence keeps a long-idle connected member bright and labels listening/connected.
	staleIdle := int64(900)
	glyph, detail := memberState(MemberStatus{Role: "worker", State: "replied", Connected: true, IdleSeconds: &staleIdle})
	if glyph != okStyle.Render("●") || detail != "replied · connected · active 15m ago" {
		t.Fatalf("connected state = (%q, %q)", glyph, detail)
	}
	glyph, detail = memberState(MemberStatus{Role: "worker", State: "done", Listening: true})
	if glyph != okStyle.Render("●") || detail != "done · listening" {
		t.Fatalf("listening done state = (%q, %q)", glyph, detail)
	}
}
