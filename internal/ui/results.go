package ui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
	s := f.state
	s.fitColumns(width)
	inspectorHeight := 2
	if s.current().record || len(s.viewRows) == 0 || s.current().err != nil {
		inspectorHeight = 0
	}
	f.ResizeItem(s.cellInfo, inspectorHeight, 0)
	if s.current().record && width != s.recordWidth {
		s.recordWidth = width
		s.renderRecord()
	}
	f.Flex.Draw(screen)
	if inspectorHeight > 0 {
		x, y, width, _ := s.cellInfo.GetRect()
		for col := x; col < x+width; col++ {
			screen.SetContent(col, y, '─', nil, s.theme.CellStyle(s.theme.Border, s.theme.Panel))
		}
	}
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
	style := s.theme.CellStyle(s.theme.Muted, s.theme.Panel)
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
	hint := "i edit SQL · t find a table"
	if s.current().err != nil {
		hint = "i edit SQL · t find a table"
	} else if s.current().rowFilter != "" || s.current().equalFilter != nil {
		hint = "Esc clear filters · / change filter"
	} else if len(s.current().result.Columns) > 0 {
		hint = "0 rows returned · i edit SQL"
	}
	if height >= 3 {
		tview.Print(screen, tview.Escape(truncateCells(hint, width)), x, y+height/2+1, width, tview.AlignCenter, s.theme.TCell(s.theme.Muted))
	}
	// Print uses its own style; recolor the notice without losing its glyphs.
	for col := x + offset; col < x+width; col++ {
		main, combining, _, _ := screen.GetContent(col, y+height/2)
		screen.SetContent(col, y+height/2, main, combining, style)
	}
}

// Allocate the pane before tview lays out columns: compact UUIDs first when
// needed, then shrink data-heavy columns, preserving short headers. Spare
// width restores truncated small columns before longer values. Nothing is
// sized from an already-truncated cell, so enlarging the terminal is lossless.
func (s *state) fitColumns(width int) {
	if width == s.gridViewportWidth || len(s.naturalWidths) == 0 {
		return
	}
	s.gridViewportWidth = width
	columns := s.current().result.Columns
	widths := append([]int(nil), s.naturalWidths...)
	minimum := make([]int, len(widths))
	wanted := make([]int, len(widths))
	total := max(0, len(widths)-1)
	for col, column := range columns {
		nameWidth := tview.TaggedStringWidth(tview.Escape(displayValue(column.Name)))
		if s.current().sorted && s.current().sortColumn == col {
			nameWidth += 2
		}
		minimum[col] = min(widths[col], max(4, min(16, nameWidth)))
		wanted[col] = max(widths[col], nameWidth)
		for _, row := range s.viewRows[:min(widthSampleRows, len(s.viewRows))] {
			wanted[col] = max(wanted[col], tview.TaggedStringWidth(tview.Escape(compactValue(rowValue(row, col), column))))
		}
		total += widths[col]
	}
	for col, column := range columns {
		if total <= width {
			break
		}
		if strings.EqualFold(column.Database, "UUID") && widths[col] > max(9, minimum[col]) {
			compact := max(9, minimum[col])
			total -= widths[col] - compact
			widths[col] = compact
		}
	}
	for total > width {
		largest := -1
		for col, w := range widths {
			if w > minimum[col] && (largest < 0 || w > widths[largest]) {
				largest = col
			}
		}
		if largest < 0 {
			break
		} // many columns: retain useful widths and scroll
		widths[largest]--
		total--
	}
	// Restore complete short columns first; avoid wasting width on a UUID
	// whose compact representation needs nine cells until the full value fits.
	order := make([]int, len(widths))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return wanted[order[i]] < wanted[order[j]] })
	for _, col := range order {
		extra := min(wanted[col]-widths[col], max(0, width-total))
		if strings.EqualFold(columns[col].Database, "UUID") && widths[col]+extra < wanted[col] {
			continue
		}
		widths[col] += extra
		total += extra
	}
	for col, column := range columns {
		header := displayValue(column.Name)
		marker := ""
		if t := s.current(); t.sorted && t.sortColumn == col {
			marker = " ▲"
			if t.sortDesc {
				marker = " ▼"
			}
		}
		name := truncateCells(header, max(1, widths[col]-len([]rune(marker)))) + marker
		header = tview.Escape(name)
		typ := displayValue(column.Database)
		if typ != "" && tview.TaggedStringWidth(tview.Escape(name+" "+typ)) <= widths[col] {
			header += " [" + s.theme.Muted + "::BU]" + tview.Escape(typ) + "[-:-:bu]"
		}
		header += strings.Repeat(" ", max(0, widths[col]-tview.TaggedStringWidth(header)))
		s.grid.GetCell(0, col).SetText(header).SetMaxWidth(widths[col])
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
