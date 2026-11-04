package ui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/session"
)

type pickerItem struct{ name, detail string }
type picker struct {
	input          *tview.InputField
	list           *tview.Table
	items, visible []pickerItem
	choose         func(string)
	returnFocus    tview.Primitive
}

func (s *state) showPicker(title string, items []pickerItem, choose func(string)) {
	p := &picker{input: s.input("/ "), list: tview.NewTable().SetSelectable(true, false), items: items, choose: choose, returnFocus: s.app.GetFocus()}
	p.list.SetBackgroundColor(s.theme.TCell(s.theme.Panel))
	p.list.SetSelectedStyle(s.theme.CellStyle(s.theme.Text, s.theme.Selection))
	p.input.SetPlaceholder("type to fuzzy filter · ↑/↓ select · Enter open · Esc cancel")
	filterItems := func(filter string) {
		p.list.Clear()
		p.visible = nil
		for _, item := range p.items {
			label := item.name + " · " + item.detail
			positions, ok := fuzzyMatches(label, filter)
			if !ok {
				continue
			}
			row := len(p.visible)
			p.visible = append(p.visible, item)
			p.list.SetCell(row, 0, tview.NewTableCell(s.highlightTableName(label, positions, s.theme.Text)).SetExpansion(1))
		}
		if len(p.visible) == 0 {
			p.list.SetCell(0, 0, tview.NewTableCell("no matches").SetSelectable(false).SetTextColor(s.theme.TCell(s.theme.Muted)))
		}
		p.list.Select(0, 0).SetOffset(0, 0)
	}
	p.input.SetChangedFunc(filterItems)
	card := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(p.input, 1, 0, true).AddItem(p.list, 0, 1, false)
	card.Box = tview.NewBox()
	s.pane(card.Box, title)
	card.SetBorderPadding(1, 1, 2, 2)
	column := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(nil, 0, 1, false).AddItem(card, 18, 0, true).AddItem(nil, 0, 1, false)
	modal := tview.NewFlex().AddItem(nil, 0, 1, false).AddItem(column, 90, 0, true).AddItem(nil, 0, 1, false)
	modal.Box = tview.NewBox().SetBackgroundColor(s.theme.TCell(s.theme.Background))
	column.Box = tview.NewBox().SetBackgroundColor(s.theme.TCell(s.theme.Background))
	s.picker = p
	s.pages.AddPage("picker", modal, true, true)
	filterItems("")
	s.app.SetFocus(p.input)
}

func (s *state) closePicker() {
	focus := s.picker.returnFocus
	s.picker = nil
	s.pages.RemovePage("picker")
	if focus == s.command || focus == nil {
		s.focusResults()
	} else {
		s.app.SetFocus(focus)
	}
}

func (s *state) capturePicker(event *tcell.EventKey) *tcell.EventKey {
	p := s.picker
	switch event.Key() {
	case tcell.KeyEsc:
		s.closePicker()
		if s.db == nil {
			s.quit()
		}
		return nil
	case tcell.KeyEnter:
		row, _ := p.list.GetSelection()
		if row >= 0 && row < len(p.visible) {
			item := p.visible[row]
			s.closePicker()
			p.choose(item.name)
		}
		return nil
	case tcell.KeyDown, tcell.KeyUp, tcell.KeyCtrlJ, tcell.KeyCtrlK:
		row, _ := p.list.GetSelection()
		delta := 1
		if event.Key() == tcell.KeyUp || event.Key() == tcell.KeyCtrlK {
			delta = -1
		}
		p.list.Select(min(max(0, row+delta), max(0, len(p.visible)-1)), 0)
		return nil
	}
	return event
}

func (s *state) pickSessions() {
	trails, err := session.List()
	if err != nil {
		s.setMessage("could not list sessions: " + err.Error())
		return
	}
	items := make([]pickerItem, 0, len(trails))
	for _, trail := range trails {
		items = append(items, pickerItem{trail.Name, fmt.Sprintf("%s · %d items · %s", trail.Title, trail.Items, trail.Updated.Local().Format("2006-01-02 15:04"))})
	}
	s.showPicker("sessions", items, s.openSession)
}

func (s *state) pickConnections() {
	items := make([]pickerItem, 0, len(s.connections))
	// Only public connections ever reach the picker; no DSN exists in this shape.
	for _, conn := range s.connections {
		items = append(items, pickerItem{conn.Name, string(conn.Driver)})
	}
	title := "saved connections"
	if s.awaitingConnection && s.message != "" {
		title += " · " + s.message
	}
	s.showPicker(title, items, func(name string) { s.switchConnection(name, nil) })
}

func (s *state) switchConnection(name string, after func()) {
	if name == s.connection {
		if after != nil {
			after()
		}
		return
	}
	if s.pending > 0 || s.connecting {
		s.setMessage("wait for the running query or cancel with Ctrl-c")
		return
	}
	if s.openConnection == nil {
		s.setMessage("connection switching unavailable")
		return
	}
	s.connecting = true
	s.pending++
	s.setMessage("connecting to " + name + "…")
	var kind db.Kind
	var conn *sql.DB
	var tables []catalog.Table
	var log *qlog.Log
	var offset int64
	var recentEntries []qlog.Entry
	opener, follow, noLog := s.openConnection, s.followEnabled, s.noLog
	startup, recent := s.awaitingConnection, s.startupRecent
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	s.connectCancel = cancel
	s.runner(ctx, func(ctx context.Context) (query.Result, error) {
		var err error
		kind, conn, err = opener(ctx, name)
		if err != nil {
			return query.Result{}, err
		}
		tables, err = catalog.NewInspector(kind, conn).Tables(ctx)
		if err != nil {
			return query.Result{}, errors.New("could not load database catalog")
		}
		if !noLog || follow {
			log, err = qlog.Open(name)
			if err == nil && follow {
				offset, err = log.Size()
				if err == nil && startup && recent > 0 {
					recentEntries, err = log.List(recent)
				}
			}
		}
		return query.Result{}, err
	}, func() {}, func(_ query.Result, err error) {
		cancel()
		s.connectCancel = nil
		s.pending--
		s.connecting = false
		if s.quitting {
			s.stopWhenDrained()
			return
		}
		if err != nil {
			s.setMessage("could not switch connection: " + err.Error())
			if s.db == nil {
				s.pickConnections()
			}
			return
		}
		if s.followCancel != nil {
			s.followCancel()
		}
		s.followGeneration++
		s.following = false
		s.awaitingConnection = false
		s.connectionSaved = true
		s.db, s.kind, s.connection, s.tables, s.log, s.logOffset = conn, kind, name, tables, log, offset
		s.seen, s.deferred = make(map[string]bool), nil
		for _, t := range s.tabs {
			if t.logConn == name && t.logID != "" {
				s.seen[t.logID] = true
			}
		}
		for _, entry := range recentEntries {
			s.appendEntry(entry)
		}
		s.filterTables("")
		s.tableFilter.SetText("")
		s.refreshHeader()
		s.setMessage("connected to " + name)
		if after != nil {
			after()
		}
		if s.initialSQL != "" {
			initial := s.initialSQL
			s.initialSQL = ""
			s.runInitialSQL(initial)
		}
		if follow {
			s.startFollow()
		}
	})
}
