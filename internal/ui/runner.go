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
	t := s.current()
	t.sql = sqlText
	s.query.SetText(sqlText)
	t.rowFilter = ""
	s.refreshHeader()
	// Capture only immutable inputs; a worker must never read mutable UI state.
	conn, limit := s.db, s.limit
	s.startWork(t, func(ctx context.Context) (query.Result, error) {
		return (query.Runner{DB: conn}).Run(ctx, query.Options{SQL: sqlText, Limit: limit, Timeout: 15 * time.Second, ReadOnly: true})
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
		if t.runID == generation {
			t.cancel = nil
			t.result, t.err = result, err
			t.cursor = gridPos{Row: 1}
			if s.current() == t {
				if err != nil {
					s.renderGrid(query.Result{})
					s.setMessage(err.Error())
				} else {
					s.renderResult(result)
					s.setMessage(fmt.Sprintf("rows=%d truncated=%t elapsed=%s", result.RowCount, result.Truncated, time.Since(t.started).Round(time.Millisecond)))
				}
			}
		}
		// Drain canceled workers before stopping the event loop. Otherwise
		// QueueUpdateDraw could wait forever after Application.Stop.
		if s.quitting && s.pending == 0 {
			s.app.Stop()
		}
	})
}

func (s *state) cancelAll() {
	for _, t := range s.tabs {
		if t.cancel != nil {
			t.cancel()
		}
	}
}

func (s *state) quit() {
	s.quitting = true
	s.cancelAll()
	if s.pending == 0 {
		s.app.Stop()
	}
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
