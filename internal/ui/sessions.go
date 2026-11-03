package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/session"
)

// Loading a session reads local files only. Its SQL is never submitted to a runner.
func sessionTabs(name string) (*session.Session, []*tab, error) {
	trail, err := session.Open(name)
	if err != nil {
		return nil, nil, err
	}
	log, err := qlog.Open(trail.Conn)
	if err != nil {
		return nil, nil, err
	}
	tabs := make([]*tab, 0, len(trail.Items))
	for _, item := range trail.Items {
		t := &tab{sql: item.SQL, notice: "no stored result · r to run", cursor: gridPos{Row: 1}}
		if item.LogID != "" {
			e, err := log.Get(item.LogID)
			if err == nil {
				t = storedTab(e)
			} else if !errors.Is(err, qlog.ErrNotFound) {
				return nil, nil, err
			}
		}
		t.title, t.sql, t.why = item.Title, item.SQL, item.Why
		if t.title == "" {
			t.title = fmt.Sprintf("#%d", item.N)
		}
		t.sessionName, t.itemN, t.sessionConn = name, item.N, trail.Conn
		tabs = append(tabs, t)
	}
	if len(tabs) == 0 {
		tabs = append(tabs, &tab{title: "query", notice: "empty session · i to write SQL · :add to add"})
	}
	return trail, tabs, nil
}

func (s *state) installSession(trail *session.Session, tabs []*tab) {
	for _, t := range s.tabs {
		if t.cancel != nil {
			t.cancel()
		}
	}
	s.tabs, s.active = tabs, 0
	s.activeSession, s.sessionConn = trail.Name, trail.Conn
	for _, t := range tabs {
		if t.logID != "" {
			s.seen[t.logID] = true
		}
	}
	if s.query != nil {
		s.showTab()
		s.focusResults()
	}
}

func (s *state) restoreSession(name string) error {
	trail, tabs, err := sessionTabs(name)
	if err != nil {
		return err
	}
	if trail.Conn != s.connection {
		return errors.New("session connection mismatch")
	}
	s.installSession(trail, tabs)
	return nil
}

func (s *state) openSession(name string) {
	if s.pending > 0 || s.connecting {
		s.setMessage("wait for the running query or cancel with Ctrl-c")
		return
	}
	trail, tabs, err := sessionTabs(name)
	if err != nil {
		s.setMessage("could not open session: " + err.Error())
		return
	}
	install := func() { s.installSession(trail, tabs) }
	if trail.Conn != s.connection {
		s.switchConnection(trail.Conn, install)
	} else {
		install()
	}
}

func (s *state) tabItem(t *tab) (session.Item, error) {
	if t.cancel != nil {
		return session.Item{}, errors.New("wait for the running query or cancel with Ctrl-c")
	}
	if (t.logID != "" && t.logConn != s.connection) || (t.sessionName != "" && t.sessionConn != s.connection) {
		return session.Item{}, errors.New("tabs span connections; switch to their connection or close them before saving")
	}
	return session.Item{Title: t.title, Why: t.why, SQL: t.sql, Conn: s.connection, LogID: t.logID, AddedBy: "human"}, nil
}

func (s *state) saveSession(name, title string) {
	s.saveTab()
	if s.connecting || s.db == nil || !s.connectionSaved {
		s.setMessage("sessions require a saved connection; :conn to choose one")
		return
	}
	// Blank tabs have no SQL to save or re-run; leave them out of the session.
	var saved []*tab
	var items []session.Item
	for _, t := range s.tabs {
		if strings.TrimSpace(t.sql) == "" {
			continue
		}
		item, err := s.tabItem(t)
		if err != nil {
			s.setMessage(err.Error())
			return
		}
		saved, items = append(saved, t), append(items, item)
	}
	if len(items) == 0 {
		s.setMessage("nothing to save: every tab is empty")
		return
	}
	if _, err := session.Create(name, title, s.connection, string(s.kind), "human"); err != nil {
		s.setMessage(err.Error())
		return
	}
	// Validate all tabs before creation and roll back the new session if an add
	// fails; never leave a success-looking partial trail behind.
	for i, item := range items {
		added, err := session.Add(name, item)
		if err != nil {
			if rollbackErr := session.Delete(name); rollbackErr != nil {
				s.setMessage("save failed; partial session remains: " + name)
			} else {
				s.setMessage("save failed: " + err.Error())
			}
			return
		}
		items[i] = added
	}
	for i, t := range saved {
		t.sessionName, t.itemN, t.sessionConn = name, items[i].N, s.connection
	}
	s.activeSession, s.sessionConn = name, s.connection
	s.showTab()
	s.setMessage("saved session " + name)
}

func (s *state) addToSession() {
	if s.activeSession == "" {
		s.setMessage("no active session; :save <name> first or :sessions")
		return
	}
	if s.sessionConn != s.connection || s.connecting {
		s.setMessage("active session uses " + s.sessionConn + "; :conn to switch first")
		return
	}
	s.saveTab()
	if t := s.current(); t.sessionName == s.activeSession {
		s.setMessage(fmt.Sprintf("already in session %s · #%d; r re-runs it", t.sessionName, t.itemN))
		return
	}
	if strings.TrimSpace(s.current().sql) == "" {
		s.setMessage("nothing to add: this tab has no SQL")
		return
	}
	item, err := s.tabItem(s.current())
	if err == nil {
		item, err = session.Add(s.activeSession, item)
	}
	if err != nil {
		s.setMessage(err.Error())
		return
	}
	t := s.current()
	t.sessionName, t.itemN, t.sessionConn = s.activeSession, item.N, s.connection
	s.showTab()
	s.setMessage(fmt.Sprintf("added to session %s · #%d", s.activeSession, item.N))
}
