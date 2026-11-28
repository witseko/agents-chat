package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// fakeCores is a CoreProcesses that records stops and drops stopped cores from its list.
type fakeCores struct {
	items   []RunningCore
	stopped []int
	stopErr error
}

func (f *fakeCores) List() ([]RunningCore, error) {
	return slices.Clone(f.items), nil
}

func (f *fakeCores) Stop(pid int) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = append(f.stopped, pid)
	f.items = slices.DeleteFunc(f.items, func(core RunningCore) bool { return core.PID == pid })
	return nil
}

// coresModel returns a home screen model with the given fake cores and data directory.
func coresModel(cores *fakeCores) model {
	m := newModel(&fakeOperator{}, "/work")
	m.processes = cores
	m.dataDir = "/home/me/.agentcy"
	m.width, m.height = 100, 30
	return m
}

func TestParseCoreProcessesKeepsOnlyCoresOfTheUser(t *testing.T) {
	output := strings.Join([]string{
		"  501   111    01:02:03 /Users/me/agentcy/bin/agentcy core",
		"  501   222       00:05 /usr/bin/python3 -m agentcy.mcp.core",
		"  502   333       00:05 /opt/agentcy core",
		"  501   444       00:05 /Users/me/agentcy/bin/agentcy --workspace /w",
		"  501   555       00:05 vim agentcy core.go",
		"",
	}, "\n")

	cores := parseCoreProcesses(output, 501)

	want := []RunningCore{{PID: 111, Uptime: "01:02:03"}, {PID: 222, Uptime: "00:05"}}
	if !slices.Equal(cores, want) {
		t.Fatalf("cores = %+v, want %+v", cores, want)
	}
}

func TestDataDirFromLsofFindsTheCoreLock(t *testing.T) {
	output := "p111\nfcwd\nn/\nf3\nn/home/me/.agentcy/core.lock\nf4\nn/home/me/.agentcy/core.sock\n"

	if got := dataDirFromLsof(output); got != "/home/me/.agentcy" {
		t.Fatalf("data dir = %q, want /home/me/.agentcy", got)
	}
	if got := dataDirFromLsof("p111\nn/tmp/other\n"); got != "" {
		t.Fatalf("data dir = %q, want empty", got)
	}
}

func TestCoresWindowListsRunningCoresAndMarksThisUI(t *testing.T) {
	cores := &fakeCores{items: []RunningCore{
		{PID: 111, Uptime: "01:02:03", DataDir: "/home/me/.agentcy"},
		{PID: 222, Uptime: "00:05", DataDir: "/tmp/other"},
	}}
	m, command := press(t, coresModel(cores), "c")
	m = drive(t, m, command)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Running cores", "process 111", "/home/me/.agentcy (this UI)", "process 222", "/tmp/other"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "/tmp/other (this UI)") {
		t.Fatalf("another data directory is marked as this UI:\n%s", view)
	}
}

func TestStopCoreAsksForConfirmationThenStopsTheSelectedCore(t *testing.T) {
	cores := &fakeCores{items: []RunningCore{
		{PID: 111, Uptime: "01:02:03", DataDir: "/home/me/.agentcy"},
		{PID: 222, Uptime: "00:05", DataDir: "/tmp/other"},
	}}
	m, command := press(t, coresModel(cores), "c")
	m = drive(t, m, command)
	m, _ = press(t, m, "down")
	m, _ = press(t, m, "s")

	if !strings.Contains(ansi.Strip(m.View().Content), "Stop process 222?") {
		t.Fatalf("expected a confirmation for process 222:\n%s", ansi.Strip(m.View().Content))
	}
	if len(cores.stopped) != 0 {
		t.Fatalf("stopped %v before confirmation", cores.stopped)
	}

	m, command = press(t, m, "enter")
	m = drive(t, m, command)

	if !slices.Equal(cores.stopped, []int{222}) {
		t.Fatalf("stopped = %v, want [222]", cores.stopped)
	}
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "process 222 ·") || !strings.Contains(view, "process 111") {
		t.Fatalf("list was not refreshed after the stop:\n%s", view)
	}
	if m.popup.kind != popupSuccess {
		t.Fatalf("popup = %v, want success", m.popup.kind)
	}
}

func TestStopCoreEscCancelsConfirmation(t *testing.T) {
	cores := &fakeCores{items: []RunningCore{{PID: 111, Uptime: "00:05"}}}
	m, command := press(t, coresModel(cores), "c")
	m = drive(t, m, command)
	m, _ = press(t, m, "s")
	m, _ = press(t, m, "esc")

	if m.cores.confirming || !m.cores.open {
		t.Fatalf("esc should cancel the confirmation and keep the window open: %+v", m.cores)
	}
	m, _ = press(t, m, "esc")
	if m.cores.open {
		t.Fatal("second esc should close the window")
	}
	if len(cores.stopped) != 0 {
		t.Fatalf("stopped = %v, want none", cores.stopped)
	}
}

func TestStopCoreFailureShowsError(t *testing.T) {
	cores := &fakeCores{items: []RunningCore{{PID: 111, Uptime: "00:05"}}, stopErr: errors.New("did not stop")}
	m, command := press(t, coresModel(cores), "c")
	m = drive(t, m, command)
	m, _ = press(t, m, "s")
	m, command = press(t, m, "enter")
	m = drive(t, m, command)

	if m.popup.kind != popupError || !strings.Contains(m.popup.text, "did not stop") {
		t.Fatalf("popup = %+v, want the stop error", m.popup)
	}
	if m.cores.stopping != 0 {
		t.Fatalf("stopping = %d, want 0", m.cores.stopping)
	}
}

func TestTopBarCoresButtonOpensTheWindow(t *testing.T) {
	cores := &fakeCores{items: []RunningCore{{PID: 111, Uptime: "00:05"}}}
	m := coresModel(cores)
	_, zones := m.topBarLayout()
	if len(zones) != 1 || zones[0].id != "cores" || zones[0].x1 > m.width {
		t.Fatalf("zones = %+v, want one cores button on screen", zones)
	}

	next, command := m.Update(tea.MouseClickMsg{X: zones[0].x0, Y: 0, Button: tea.MouseLeft})
	m = drive(t, next.(model), command)

	if !m.cores.open || len(m.cores.items) != 1 {
		t.Fatalf("cores window = %+v, want open with one core", m.cores)
	}
}
