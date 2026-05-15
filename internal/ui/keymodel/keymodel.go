package keymodel

import tea "github.com/charmbracelet/bubbletea"

type Mode string

const (
	Normal  Mode = "normal"
	Command Mode = "command"
	Search  Mode = "search"
	Query   Mode = "query"
)

type Action string

const (
	Noop         Action = ""
	MoveUp       Action = "move_up"
	MoveDown     Action = "move_down"
	MoveLeft     Action = "move_left"
	MoveRight    Action = "move_right"
	Open         Action = "open"
	Quit         Action = "quit"
	StartCommand Action = "start_command"
	StartSearch  Action = "start_search"
	FocusQuery   Action = "focus_query"
	Escape       Action = "escape"
)

type Model struct {
	Mode   Mode
	Action Action
}

func New() Model {
	return Model{Mode: Normal}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.Action = Noop
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.Mode = Normal
		m.Action = Escape
	case ":":
		m.Mode = Command
		m.Action = StartCommand
	case "/":
		m.Mode = Search
		m.Action = StartSearch
	case "i":
		m.Mode = Query
		m.Action = FocusQuery
	case "enter":
		m.Action = Open
	case "q", "ctrl+c":
		if m.Mode == Normal {
			m.Action = Quit
		}
	case "j", "down":
		if m.Mode == Normal {
			m.Action = MoveDown
		}
	case "k", "up":
		if m.Mode == Normal {
			m.Action = MoveUp
		}
	case "h", "left":
		if m.Mode == Normal {
			m.Action = MoveLeft
		}
	case "l", "right":
		if m.Mode == Normal {
			m.Action = MoveRight
		}
	}
	return m, nil
}

func (m Model) View() string { return string(m.Mode) }
