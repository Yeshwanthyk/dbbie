package keymodel

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestVimMotionActionsInNormalMode(t *testing.T) {
	model := New()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := updated.(Model)
	if got.Action != MoveDown {
		t.Fatalf("action = %q, want %q", got.Action, MoveDown)
	}
}

func TestCommandModeSuppressesVimMotion(t *testing.T) {
	model := New()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := updated.(Model)
	if got.Mode != Command {
		t.Fatalf("mode = %q, want command", got.Mode)
	}
	if got.Action != Noop {
		t.Fatalf("action = %q, want noop", got.Action)
	}
}
