package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

// Tests can inject entries and completion synchronously, without file polling.
// The production hook queues every callback onto tview's event loop.
type followHook func(context.Context, int64, func(qlog.Entry), func(error))

func (s *state) prepareLog(follow bool, recent int) error {
	if s.noLog && !follow {
		return nil
	}
	l, err := qlog.Open(s.connection)
	if err != nil {
		return fmt.Errorf("could not open query log: %w", err)
	}
	s.log = l
	if !follow {
		return nil
	}
	// Take the offset before listing: entries appended during startup are
	// either restored or followed, never lost. IDs deduplicate the overlap.
	s.logOffset, err = l.Size()
	if err != nil {
		return err
	}
	if recent > 0 {
		entries, err := l.List(recent)
		if err != nil {
			return err
		}
		for _, e := range entries {
			s.appendEntry(e)
		}
	}
	return nil
}

func (s *state) appendEntry(e qlog.Entry) bool {
	if s.seen[e.ID] {
		return false
	}
	s.seen[e.ID] = true
	title := e.Title
	if title == "" {
		title = e.ID
	}
	t := &tab{title: title, sql: e.SQL, origin: &e, cursor: gridPos{Row: 1}, result: query.Result{
		ID: e.ID, Columns: e.Columns, Rows: e.Rows, RowCount: e.RowCount,
		Truncated: e.Truncated, Elapsed: (time.Duration(e.ElapsedMs) * time.Millisecond).String(),
	}}
	if e.Error != nil {
		t.err = errors.New(e.Error.Message)
	}
	s.tabs = append(s.tabs, t)
	return true
}

func (s *state) typing() bool {
	focus := s.app.GetFocus()
	return focus == s.query || focus == s.command || focus == s.search || focus == s.tableFilter
}

func (s *state) receiveEntry(e qlog.Entry) {
	if s.quitting {
		return
	}
	// A TUI completion may be waiting in the event queue while Follow sees
	// its append. Hold human entries until completions mark their IDs seen,
	// so following our own log never creates a duplicate tab.
	if e.Source == "tui" && e.Actor == "human" && s.pending > 0 {
		s.deferred = append(s.deferred, e)
		return
	}
	if !s.appendEntry(e) {
		return
	}
	if !s.typing() && !s.helpOpen {
		s.switchTab(len(s.tabs) - 1)
	} else {
		s.agentNotice = fmt.Sprintf("◆ new agent query %s · ]a to view", e.ID)
		s.refreshHeader()
		s.refreshMode()
	}
}

func (s *state) flushEntries() {
	if s.pending != 0 {
		return
	}
	entries := s.deferred
	s.deferred = nil
	for _, e := range entries {
		s.receiveEntry(e)
	}
}

func (s *state) startFollow() {
	ctx, cancel := context.WithCancel(s.ctx)
	s.followCancel = cancel
	s.following = true
	follow := s.follow
	if follow == nil {
		l, app := s.log, s.app
		follow = func(ctx context.Context, offset int64, entry func(qlog.Entry), done func(error)) {
			go func() {
				err := l.Follow(ctx, offset, func(e qlog.Entry) {
					app.QueueUpdateDraw(func() { entry(e) })
				})
				// This completion drains before Stop, including when quit races
				// an entry callback. No worker queues work after the loop stops.
				app.QueueUpdateDraw(func() { done(err) })
			}()
		}
	}
	follow(ctx, s.logOffset, s.receiveEntry, func(err error) {
		cancel()
		s.following = false
		if err != nil && !errors.Is(err, context.Canceled) && !s.quitting {
			s.setMessage("query log follow stopped: " + err.Error())
		}
		s.stopWhenDrained()
	})
}

func (s *state) stopWhenDrained() {
	if s.quitting && s.pending == 0 && !s.following && !s.startupPending {
		s.app.Stop()
	}
}
