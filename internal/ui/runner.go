package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/session"
)

type queryWork func(context.Context) (query.Result, error)

type safeQueryError struct {
	message string
	cause   error
}

func (e safeQueryError) Error() string { return e.message }
func (e safeQueryError) Unwrap() error { return e.cause }

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
	if s.current().origin != nil || s.current().sessionName != "" {
		s.newTab("query", sqlText, nil)
	}
	s.runTabQuery(sqlText, false)
}

func (s *state) rerunTab() {
	t := s.current()
	if t.sessionName == "" && t.origin != nil && t.origin.Session != "" {
		if t.logConn != "" && t.logConn != s.connection {
			s.setMessage("session uses " + t.logConn + "; :conn to switch before r")
			return
		}
		if trail, err := session.Open(t.origin.Session); err == nil {
			for _, item := range trail.Items {
				if item.LogID == t.logID {
					t.sessionName, t.itemN, t.sessionConn, t.why = trail.Name, item.N, trail.Conn, item.Why
					break
				}
			}
		}
	}
	if t.sessionName != "" && t.sessionConn != s.connection {
		s.setMessage("session uses " + t.sessionConn + "; :conn to switch before r")
		return
	}
	s.runTabQuery(t.sql, true)
}

func (s *state) runTabQuery(sqlText string, forceLog bool) {
	if s.connecting || s.awaitingConnection {
		s.setMessage("choose a connection first with :conn")
		return
	}
	if forceLog && s.log == nil {
		l, err := qlog.Open(s.connection)
		if err != nil {
			s.setMessage("could not open query log")
			return
		}
		s.log = l
	}
	t := s.current()
	clean, redactErr := s.redactStrings(sqlText, t.title, t.why)
	if redactErr != nil {
		s.setMessage("could not safely redact query text")
		return
	}
	t.sql, t.title, t.why = clean[0], clean[1], clean[2]
	s.query.SetText(t.sql)
	t.rowFilter = ""
	t.equalFilter, t.sorted, t.record = nil, false, false
	s.resultPages.SwitchToPage("grid")
	s.refreshHeader()
	// Capture only immutable inputs; a worker must never read mutable UI state.
	conn, limit, log, noLog := s.db, s.limit, s.log, s.noLog
	kind, title, why, sessionName, itemN := s.kind, t.title, t.why, t.sessionName, t.itemN
	redact := s.redactStrings
	if t.origin != nil && sessionName == "" {
		sessionName = t.origin.Session
	}
	s.startWork(t, func(ctx context.Context) (query.Result, error) {
		start := time.Now()
		result, err := (query.Runner{DB: conn}).Run(ctx, query.Options{SQL: sqlText, Limit: limit, Timeout: 15 * time.Second, ReadOnly: true})
		e := humanEntry(kind, title, sqlText, result, err, time.Since(start))
		e.Why, e.Session = why, sessionName
		if t.sessionName != "" {
			e.Item = itemN
		}
		if redactErr := sanitizeLogEntry(&e, redact); redactErr != nil {
			return query.Result{}, fmt.Errorf("query completed but could not safely redact its result")
		}
		result.Columns, result.Rows = e.Columns, e.Rows
		if err != nil {
			err = safeQueryError{message: e.Error.Message, cause: err}
		}
		if log != nil && (!noLog || forceLog || sessionName != "") {
			entry, logErr := log.Append(e)
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
	connection := s.connection
	s.pending++
	s.refreshMode()
	s.runner(ctx, work, func() {
		if s.current() == t && t.runID == generation {
			s.refreshMode()
		}
	}, func(result query.Result, err error) {
		cancel()
		s.pending--
		if result.ID != "" && connection == s.connection {
			s.seen[result.ID] = true
		}
		if t.runID == generation {
			warning := ""
			t.notice = ""
			t.logID, t.logConn = "", ""
			if strings.HasPrefix(result.ID, "q_") {
				t.logID, t.logConn = result.ID, connection
				if t.origin != nil && s.log != nil && connection == s.connection {
					if entry, getErr := s.log.Get(result.ID); getErr == nil {
						t.origin = &entry
					}
				}
				if t.sessionName != "" {
					if updateErr := session.SetLogID(t.sessionName, t.itemN, result.ID); updateErr != nil {
						warning = fmt.Sprintf("ran %s, but session %s #%d was not updated: %v", result.ID, t.sessionName, t.itemN, updateErr)
					}
				}
			}
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
				if t.origin != nil || t.sessionName != "" {
					s.showTab()
				}
				if warning != "" {
					s.setMessage(warning)
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
	if s.connectCancel != nil {
		s.connectCancel()
	}
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
	if s.connectCancel != nil {
		s.connectCancel()
		s.setMessage("canceling connection…")
		return
	}
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
