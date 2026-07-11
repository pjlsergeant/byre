package commands

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/deliver"
)

func pickSessions() []deliver.Session {
	return []deliver.Session{
		{ID: "aaa", ProjectID: "proj-aaa", WorkdirID: "proj-aaa", EngineName: "docker"},
		{ID: "bbb", ProjectID: "proj-bbb", WorkdirID: "wt-bbb", EngineName: "podman", Foreign: true, UID: 777},
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func drive(m pickModel, keys ...string) pickModel {
	var tm tea.Model = m
	for _, k := range keys {
		tm, _ = tm.Update(key(k))
	}
	return tm.(pickModel)
}

func TestPickModelSelects(t *testing.T) {
	m := drive(pickModel{sessions: pickSessions(), choice: -1}, "down", "enter")
	if m.quit || m.choice != 1 {
		t.Fatalf("model = %+v", m)
	}
}

func TestPickModelCursorBounds(t *testing.T) {
	m := drive(pickModel{sessions: pickSessions(), choice: -1}, "up", "up", "down", "down", "down", "enter")
	if m.choice != 1 {
		t.Fatalf("cursor escaped the list: %+v", m)
	}
}

func TestPickModelCancels(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		m := drive(pickModel{sessions: pickSessions(), choice: -1}, k)
		if !m.quit || m.choice != -1 {
			t.Fatalf("%s should cancel: %+v", k, m)
		}
	}
}

func TestPickViewShowsHonestMetadata(t *testing.T) {
	v := pickModel{sessions: pickSessions(), choice: -1}.View()
	for _, want := range []string{"proj-aaa (docker)", "wt-bbb (podman)", "owned by uid 777, not you"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
}

func TestGraphicalPickToolDarwinNeedsLocalSession(t *testing.T) {
	stubClipTools(t, "osascript")
	if p := graphicalPickTool("darwin", env(map[string]string{"SSH_CONNECTION": "1.2.3.4"})); p != nil {
		t.Fatal("SSH'd darwin must not attempt a dialog")
	}
	if p := graphicalPickTool("darwin", env(nil)); p == nil {
		t.Fatal("local darwin with osascript should offer a dialog")
	}
}

func TestGraphicalPickToolLinuxNeedsDisplay(t *testing.T) {
	stubClipTools(t, "zenity")
	if p := graphicalPickTool("linux", env(nil)); p != nil {
		t.Fatal("no DISPLAY/WAYLAND_DISPLAY: no dialog")
	}
	if p := graphicalPickTool("linux", env(map[string]string{"DISPLAY": ":0"})); p == nil {
		t.Fatal("X11 with zenity should offer a dialog")
	}
}

func TestMatchPick(t *testing.T) {
	sessions := pickSessions()
	s, ok, err := matchPick(sessions, pickRow(sessions[1]))
	if err != nil || !ok || s.ID != "bbb" {
		t.Fatalf("s=%+v ok=%v err=%v", s, ok, err)
	}
	if _, ok, err := matchPick(sessions, "false"); ok || err != nil {
		t.Fatalf("osascript cancel should be a clean no: ok=%v err=%v", ok, err)
	}
	if _, ok, err := matchPick(sessions, ""); ok || err != nil {
		t.Fatalf("empty answer should be a clean no: ok=%v err=%v", ok, err)
	}
	if _, _, err := matchPick(sessions, "nonsense"); err == nil {
		t.Fatal("unknown answer must error, not guess")
	}
}

func TestGraphicalPickerToolFailureIsNotCancel(t *testing.T) {
	// A broken dialog must surface as an error; only exit 1 is a user cancel.
	stubClipTools(t, "zenity")
	orig := clipRunOut
	t.Cleanup(func() { clipRunOut = orig })
	clipRunOut = func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("zenity: cannot open display")
	}
	p := graphicalPickTool("linux", env(map[string]string{"DISPLAY": ":0"}))
	_, ok, err := p(pickSessions())
	if ok || err == nil {
		t.Fatalf("broken dialog masqueraded as a choice: ok=%v err=%v", ok, err)
	}
}

func TestGraphicalPickerExitOneIsCancel(t *testing.T) {
	stubClipTools(t, "zenity")
	orig := clipRunOut
	t.Cleanup(func() { clipRunOut = orig })
	// A genuine ExitError with code 1 (zenity's cancel), wrapped the way the
	// real seam wraps it.
	_, realErr := exec.Command("sh", "-c", "exit 1").Output()
	clipRunOut = func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("zenity: %w", realErr)
	}
	p := graphicalPickTool("linux", env(map[string]string{"DISPLAY": ":0"}))
	_, ok, err := p(pickSessions())
	if ok || err != nil {
		t.Fatalf("cancel (exit 1) should be a clean no: ok=%v err=%v", ok, err)
	}
}
