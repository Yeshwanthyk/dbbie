package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/session"
)

// A rendered error returns a failing exit status without emitting a second JSON document.
type renderedError struct{ error }

func (e *renderedError) Unwrap() error { return e.error }

func sessions(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: dbbie session new|ls|show|add|rm|run")
	}
	sub, args := args[0], args[1:]
	fs := newFlags("session " + sub)
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	var name, logID string
	if sub != "ls" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if sub == "add" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		logID, args = args[0], args[1:]
	}
	conn := ""
	title := ""
	item, only := 0, 0
	limit := query.DefaultPreviewLimit
	timeout := 10 * time.Second
	switch sub {
	case "new":
		fs.StringVar(&conn, "conn", "", "saved connection name")
		fs.StringVar(&title, "title", "", "session title")
	case "add":
		fs.StringVar(&title, "title", "", "item title (defaults to log title)")
	case "rm":
		fs.IntVar(&item, "item", 0, "remove only this stable item number")
	case "run":
		fs.IntVar(&only, "only", 0, "run only this item number")
		fs.IntVar(&limit, "limit", query.DefaultPreviewLimit, "preview limit (max 1000)")
		fs.DurationVar(&timeout, "timeout", 10*time.Second, "per-query timeout")
	case "ls", "show":
	default:
		return errors.New("unknown session command")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected session arguments")
	}
	if sub == "ls" {
		list, err := session.List()
		if err != nil {
			return err
		}
		if *jsonOut {
			return encode(stdout, list)
		}
		for _, s := range list {
			fmt.Fprintf(stdout, "%s\t%s\t%s\titems=%d\t%s\t%s\n", s.Name, s.Title, s.Conn, s.Items, s.Updated.Format(time.RFC3339), s.CreatedBy)
		}
		return nil
	}
	if err := session.ValidateName(name); err != nil {
		return err
	}
	actor := queryActor(stdout, false)
	if sub == "new" {
		if conn == "" {
			return errors.New("--conn is required")
		}
		kind, _, err := resolveDBSpec(dbSpec{ConnName: conn})
		if err != nil {
			return err
		}
		s, err := session.Create(name, title, conn, string(kind), actor)
		if err != nil {
			return err
		}
		if *jsonOut {
			return encode(stdout, s)
		}
		fmt.Fprintf(stdout, "created session %s (%s)\n", name, conn)
		return nil
	}
	s, err := session.Open(name)
	if err != nil {
		return err
	}
	switch sub {
	case "show":
		return showSession(s, stdout, *jsonOut)
	case "add":
		if logID == "" {
			return errors.New("usage: dbbie session add <name> <logId> [--title T]")
		}
		l, err := qlog.Open(s.Conn)
		if err != nil {
			return err
		}
		e, err := l.Get(logID)
		if err != nil {
			return err
		}
		if !flagGiven(fs, "title") {
			title = e.Title
		}
		added, err := session.Add(name, session.Item{Title: title, Why: e.Why, SQL: e.SQL, Conn: s.Conn, LogID: e.ID, AddedBy: actor})
		if err != nil {
			return err
		}
		if *jsonOut {
			return encode(stdout, added)
		}
		fmt.Fprintf(stdout, "added %s #%d (%s)\n", name, added.N, added.LogID)
		return nil
	case "rm":
		if flagGiven(fs, "item") {
			if item <= 0 {
				return errors.New("--item must be positive")
			}
			err = session.Remove(name, item)
		} else {
			err = session.Delete(name)
		}
		if err != nil {
			return err
		}
		if *jsonOut {
			return encode(stdout, map[string]any{"removed": name, "item": item})
		}
		fmt.Fprintf(stdout, "removed %s (item=%d; 0 means whole session)\n", name, item)
		return nil
	case "run":
		if flagGiven(fs, "only") && only <= 0 {
			return errors.New("--only must be positive")
		}
		return runSession(ctx, s, only, limit, timeout, actor, stdout, *jsonOut)
	}
	return nil
}

type sessionItemView struct {
	session.Item
	RowCount int         `json:"rowCount"`
	Error    *qlog.Error `json:"error"`
}

type sessionView struct {
	*session.Session
	Items []sessionItemView `json:"items"`
}

func showSession(s *session.Session, stdout io.Writer, jsonOut bool) error {
	view := sessionView{Session: s, Items: make([]sessionItemView, 0, len(s.Items))}
	l, err := qlog.Open(s.Conn)
	if err != nil {
		return err
	}
	for _, item := range s.Items {
		v := sessionItemView{Item: item}
		if item.LogID != "" {
			e, err := l.Get(item.LogID)
			if err != nil {
				return err
			}
			v.RowCount, v.Error = e.RowCount, e.Error
		}
		view.Items = append(view.Items, v)
	}
	if jsonOut {
		return encode(stdout, view)
	}
	fmt.Fprintf(stdout, "session %s: %s (%s)\n", s.Name, s.Title, s.Conn)
	for _, item := range view.Items {
		fmt.Fprintf(stdout, "#%d %s %s rows=%d\n  why: %s\n  sql: %s\n", item.N, item.Title, item.LogID, item.RowCount, item.Why, item.SQL)
		if item.Error != nil {
			fmt.Fprintf(stdout, "  %s: %s\n", item.Error.Code, item.Error.Message)
		}
	}
	return nil
}

type sessionRunItem struct {
	N      int           `json:"n"`
	LogID  string        `json:"logId"`
	Result *query.Result `json:"result,omitempty"`
	Error  *qlog.Error   `json:"error"`
}

type sessionRunOutput struct {
	Items []sessionRunItem `json:"items"`
	Error *qlog.Error      `json:"error,omitempty"`
}

func runSession(ctx context.Context, s *session.Session, only, limit int, timeout time.Duration, actor string, stdout io.Writer, jsonOut bool) error {
	if only != 0 {
		found := false
		for _, item := range s.Items {
			if item.N == only {
				found = true
			}
		}
		if !found {
			return session.ErrNotFound
		}
	}
	// Validate the saved connection before starting; no ad-hoc credentials here.
	if _, _, err := resolveDBSpec(dbSpec{ConnName: s.Conn}); err != nil {
		return err
	}
	out := sessionRunOutput{Items: make([]sessionRunItem, 0)}
	var firstErr error
	secrets := commandSecrets([]string{"session", "run"})
	for _, item := range s.Items {
		if only != 0 && item.N != only {
			continue
		}
		result, entry, err := executeQuery(ctx, queryRun{Spec: dbSpec{ConnName: s.Conn}, SQL: item.SQL, Title: item.Title, Why: item.Why, Actor: actor, Session: s.Name, Item: item.N, Limit: limit, Timeout: timeout, Secrets: secrets})
		if entry.ID != "" {
			if updateErr := session.SetLogID(s.Name, item.N, entry.ID); updateErr != nil {
				err = commandError(errorCodeOrInvalid(err), joinRunError(err, "could not update session logId: "+updateErr.Error()))
			}
		}
		v := sessionRunItem{N: item.N, LogID: entry.ID}
		if err != nil {
			v.Error = errorDetail(err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			v.Result = &result
		}
		out.Items = append(out.Items, v)
		if !jsonOut {
			fmt.Fprintf(stdout, "#%d %s\n", item.N, item.Title)
			if err != nil {
				fmt.Fprintf(stdout, "%s: %s\n", errorCode(err), err)
			} else if err := printQueryResult(stdout, false, result); err != nil {
				return err
			}
		}
	}
	if firstErr != nil {
		out.Error = errorDetail(firstErr)
	}
	if jsonOut {
		if err := encode(stdout, out); err != nil {
			return err
		}
		if firstErr != nil {
			return &renderedError{firstErr}
		}
	}
	return firstErr
}
