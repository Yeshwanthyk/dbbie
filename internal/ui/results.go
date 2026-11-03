package ui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// The frame keeps the inspector outside the scrolling grid. Empty/error
// notices are drawn in the grid body, leaving returned column headers visible.
type resultsFrame struct {
	*tview.Flex
	state *state
}

func (f *resultsFrame) Draw(screen tcell.Screen) {
	_, _, width, _ := f.GetInnerRect()
	f.state.fitUUIDColumns(width)
	f.Flex.Draw(screen)
	s := f.state
	if len(s.viewRows) > 0 && s.current().err == nil || s.current().record {
		return
	}
	x, y, width, height := s.grid.GetInnerRect()
	if len(s.current().result.Columns) > 0 {
		y++
		height--
	}
	if width <= 0 || height <= 0 {
		return
	}
	style := s.theme.CellStyle(s.theme.Muted, s.theme.Panel).Dim(true)
	message := "0 rows"
	if s.current().err != nil {
		message = displayValue(s.current().err.Error())
		style = s.theme.CellStyle(s.theme.Danger, s.theme.Panel)
	} else if len(s.current().result.Columns) == 0 {
		message = "Open a table or run a query"
	}
	for row := y; row < y+height; row++ {
		for col := x; col < x+width; col++ {
			screen.SetContent(col, row, ' ', nil, style)
		}
	}
	message = truncateCells(message, width)
	offset := max(0, (width-tview.TaggedStringWidth(tview.Escape(message)))/2)
	tview.Print(screen, tview.Escape(message), x+offset, y+height/2, width-offset, tview.AlignLeft, s.theme.TCell(s.theme.Muted))
	// Print uses its own style; recolor the notice without losing its glyphs.
	for col := x + offset; col < x+width; col++ {
		main, combining, _, _ := screen.GetContent(col, y+height/2)
		screen.SetContent(col, y+height/2, main, combining, style)
	}
}

// Keep UUIDs whole when they fit. Only when the pane would overflow do they
// collapse to the recognizable first eight characters plus an ellipsis.
func (s *state) fitUUIDColumns(width int) {
	if width == s.gridViewportWidth || len(s.viewRows) == 0 {
		return
	}
	s.gridViewportWidth = width
	widths := append([]int(nil), s.naturalWidths...)
	total := max(0, len(widths)-1)
	for _, w := range widths {
		total += w
	}
	for col, column := range s.current().result.Columns {
		if col >= len(widths) || !strings.EqualFold(column.Database, "UUID") {
			continue
		}
		if total > width && widths[col] > 9 {
			total -= widths[col] - 9
			widths[col] = 9
		}
	}
	for col, column := range s.current().result.Columns {
		if col >= len(widths) || !strings.EqualFold(column.Database, "UUID") {
			continue
		}
		header := displayValue(column.Name)
		marker := ""
		if t := s.current(); t.sorted && t.sortColumn == col {
			marker = " ▲"
			if t.sortDesc {
				marker = " ▼"
			}
		}
		header = truncateCells(header, widths[col]-len([]rune(marker))) + marker
		header += strings.Repeat(" ", max(0, widths[col]-tview.TaggedStringWidth(tview.Escape(header))))
		s.grid.GetCell(0, col).SetText(tview.Escape(header)).SetMaxWidth(widths[col])
		for row, values := range s.viewRows {
			s.grid.GetCell(row+1, col).SetText(tview.Escape(gridValue(rowValue(values, col), column, widths[col]))).SetMaxWidth(widths[col])
		}
	}
}

func (s *state) resultKey(key rune) bool {
	switch key {
	case 'y':
		s.pendingKey = key
	case 'Y':
		s.yank(true)
	case 's', 'S':
		s.sortGrid(key == 'S')
	case 'f':
		s.toggleCellFilter()
	default:
		return false
	}
	return true
}

func writeOSC52(writer io.Writer, data []byte) error {
	sequence := "\x1b]52;c;" + base64.StdEncoding.EncodeToString(data) + "\a"
	n, err := io.WriteString(writer, sequence)
	if err == nil && n != len(sequence) {
		return io.ErrShortWrite
	}
	return err
}

func (s *state) yank(rowJSON bool) {
	value, _, ok := s.cursorValue()
	if !ok {
		return
	}
	data := []byte(fullValue(value))
	label := "cell"
	if rowJSON {
		row, _ := s.grid.GetSelection()
		object := make(map[string]any, len(s.current().result.Columns))
		for col, column := range s.current().result.Columns {
			object[column.Name] = rowValue(s.viewRows[row-1], col)
		}
		var err error
		data, err = json.Marshal(object)
		if err != nil {
			s.setMessage("copy failed: row is not JSON encodable")
			return
		}
		label = "row JSON"
	}
	if s.clipboardWriter != nil {
		if err := writeOSC52(s.clipboardWriter, data); err != nil {
			s.setMessage("copy failed: terminal write")
			return
		}
	} else if s.screen != nil {
		// tcell serializes clipboard escapes with terminal drawing, avoiding
		// interleaved stdout writes corrupting the screen or the OSC52 payload.
		s.screen.SetClipboard(data)
	} else {
		s.setMessage("copy unavailable: no terminal")
		return
	}
	s.copiedUntil = time.Now().Add(1500 * time.Millisecond)
	s.setMessage(fmt.Sprintf("copied %s", label))
	if s.screen != nil {
		if s.copyTimer != nil {
			s.copyTimer.Stop()
		}
		app := s.app
		s.copyTimer = time.AfterFunc(1500*time.Millisecond, func() {
			// QueueEvent is buffered and does not wait for an update callback;
			// unlike QueueUpdateDraw this cannot strand a worker after quit.
			app.QueueEvent(tcell.NewEventKey(tcell.KeyF24, 0, tcell.ModNone))
		})
	}
}

func (s *state) expireCopyMessage() {
	if !s.copiedUntil.IsZero() && time.Now().After(s.copiedUntil) {
		s.copiedUntil = time.Time{}
		if strings.HasPrefix(s.message, "copied ") {
			s.setMessage("")
		}
	}
}
