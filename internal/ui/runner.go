package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/query"
)

type queryWork func(context.Context) (query.Result, error)

// Tests replace this hook with an inline runner; live workers queue every UI
// mutation (including progress) through Application.QueueUpdateDraw.
type runnerHook func(context.Context, queryWork, func(), func(query.Result, error))

func (s *state) backgroundRunner(ctx context.Context, work queryWork, progress func(), done func(query.Result, error)) {
	go func() {
		type outcome struct {
			result query.Result
			err    error
		}
		finished := make(chan outcome, 1)
		go func() { result, err := work(ctx); finished <- outcome{result, err} }()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case out := <-finished:
				s.app.QueueUpdateDraw(func() { done(out.result, out.err) })
				return
			case <-ticker.C:
				s.app.QueueUpdateDraw(progress)
			}
		}
	}()
}

func (s *state) runQuery(sqlText string) {
	if s.current().origin != nil {
		s.newTab("query", sqlText, nil)
	}
	t := s.current()
	t.sql = sqlText
	s.query.SetText(sqlText)
	t.rowFilter = ""
	t.equalFilter, t.sorted, t.record = nil, false, false
	s.resultPages.SwitchToPage("grid")
	s.refreshHeader()
	// Capture only immutable inputs; a worker must never read mutable UI state.
	conn, limit, log, noLog := s.db, s.limit, s.log, s.noLog
	kind, title := s.kind, t.title
	s.startWork(t, func(ctx context.Context) (query.Result, error) {
		start := time.Now()
		result, err := (query.Runner{DB: conn}).Run(ctx, query.Options{SQL: sqlText, Limit: limit, Timeout: 15 * time.Second, ReadOnly: true})
		if log != nil && !noLog {
			entry, logErr := log.Append(humanEntry(kind, title, sqlText, result, err, time.Since(start)))
			if logErr != nil {
				if err != nil {
					return result, fmt.Errorf("%w; query log write failed: %v", err, logErr)
				}
				return result, fmt.Errorf("query log write failed: %w", logErr)
			}
			result.ID = entry.ID
		}
		return result, err
	})
}

func (s *state) startWork(t *tab, work queryWork) {
	if t.cancel != nil {
		t.cancel()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t.cancel = cancel
	t.started = time.Now()
	t.runID++
	generation := t.runID
	s.pending++
	s.refreshMode()
	s.runner(ctx, work, func() {
		if s.current() == t && t.runID == generation {
			s.refreshMode()
		}
	}, func(result query.Result, err error) {
		cancel()
		s.pending--
		if result.ID != "" {
			s.seen[result.ID] = true
		}
		if t.runID == generation {
			t.cancel = nil
			t.result, t.err = result, err
			t.record = false
			t.cursor = gridPos{Row: 1}
			if s.current() == t {
				if s.app.GetFocus() == s.recordView {
					s.resultPages.SwitchToPage("grid")
					s.focusResults()
				}
				if err != nil {
					s.searchGrid(t.rowFilter)
					s.setMessage(err.Error())
				} else {
					s.renderResult(result)
					s.setMessage("")
				}
			}
		}
		// Drain canceled workers before stopping the event loop. Otherwise
		// QueueUpdateDraw could wait forever after Application.Stop.
		s.flushEntries()
		s.stopWhenDrained()
	})
}

func (s *state) cancelAll() {
	if s.followCancel != nil {
		s.followCancel()
	}
	if s.copyTimer != nil {
		s.copyTimer.Stop()
	}
	for _, t := range s.tabs {
		if t.cancel != nil {
			t.cancel()
		}
	}
}

func (s *state) quit() {
	s.quitting = true
	s.cancelAll()
	s.stopWhenDrained()
}

func (s *state) cancelOrQuit() {
	if cancel := s.current().cancel; cancel != nil {
		cancel()
		s.setMessage("canceling…")
		return
	}
	// A background tab may still be running. Ctrl-c cancels it as well rather
	// than silently abandoning a query when the foreground tab is idle.
	for _, t := range s.tabs {
		if t.cancel != nil {
			t.cancel()
			s.setMessage("canceling background query…")
			return
		}
	}
	s.quit()
}
