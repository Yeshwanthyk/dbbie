package theme

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/lipgloss"
	"github.com/gdamore/tcell/v2"
)

type Theme struct {
	Name       string `json:"name"`
	Background string `json:"background"`
	Panel      string `json:"panel"`
	Border     string `json:"border"`
	Primary    string `json:"primary"`
	Accent     string `json:"accent"`
	Text       string `json:"text"`
	Muted      string `json:"muted"`
	Danger     string `json:"danger"`
	Selection  string `json:"selection"`
	Success    string `json:"success"`
	Agent      string `json:"agent"`
}

var themes = map[string]Theme{
	"midnight": {
		Name:       "midnight",
		Background: "#0D1117",
		Panel:      "#161B22",
		Border:     "#30363D",
		Primary:    "#7DD3FC",
		Accent:     "#F2CC60",
		Text:       "#F0F6FC",
		Muted:      "#8B949E",
		Danger:     "#FF7B72",
		Selection:  "#1F2A37",
		Success:    "#7EE787",
		Agent:      "#D2A8FF",
	},
	"light": {
		Name:       "light",
		Background: "#F8FAFC",
		Panel:      "#FFFFFF",
		Border:     "#CBD5E1",
		Primary:    "#0F766E",
		Accent:     "#7C3AED",
		Text:       "#111827",
		Muted:      "#64748B",
		Danger:     "#B91C1C",
		Selection:  "#E2E8F0",
		Success:    "#15803D",
		Agent:      "#7C3AED",
	},
	"contrast": {
		Name:       "contrast",
		Background: "#000000",
		Panel:      "#0A0A0A",
		Border:     "#FFFFFF",
		Primary:    "#00E5FF",
		Accent:     "#FFE600",
		Text:       "#FFFFFF",
		Muted:      "#BDBDBD",
		Danger:     "#FF4D4D",
		Selection:  "#262626",
		Success:    "#00FF7F",
		Agent:      "#D6A3FF",
	},
}

func Names() []string {
	names := make([]string, 0, len(themes))
	for name := range themes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Resolve(name string) (Theme, error) {
	if name == "" {
		name = "midnight"
	}
	t, ok := themes[name]
	if !ok {
		return Theme{}, fmt.Errorf("unknown theme %q; available: %v", name, Names())
	}
	return t, nil
}

func (t Theme) TCell(fg string) tcell.Color {
	return tcell.GetColor(fg)
}

func (t Theme) CellStyle(fg, bg string) tcell.Style {
	return tcell.StyleDefault.Foreground(tcell.GetColor(fg)).Background(tcell.GetColor(bg))
}

func (t Theme) StatusStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Text)).
		Background(lipgloss.Color(t.Panel)).
		Bold(true)
}
