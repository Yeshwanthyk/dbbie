package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

type logSummary struct {
	ID       string      `json:"id"`
	TS       time.Time   `json:"ts"`
	Actor    string      `json:"actor"`
	Title    string      `json:"title"`
	Why      string      `json:"why"`
	SQL      string      `json:"sql"`
	RowCount int         `json:"rowCount"`
	Error    *qlog.Error `json:"error"`
}

func listLog(args []string, stdout io.Writer) error {
	fs := newFlags("log")
	connName := fs.String("conn", "", "connection log name (adhoc for raw DSN queries)")
	limit := fs.Int("limit", 20, "recent entries; <=0 means all")
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *connName == "" || fs.NArg() != 0 {
		return errors.New("usage: dbbie log --conn <name> [--limit N] [--json]")
	}
	log, err := qlog.Open(*connName)
	if err != nil {
		return err
	}
	entries, err := log.List(*limit)
	if err != nil {
		return err
	}
	summaries := make([]logSummary, 0, len(entries))
	for _, e := range entries {
		summaries = append(summaries, logSummary{e.ID, e.TS, e.Actor, e.Title, e.Why, e.SQL, e.RowCount, e.Error})
	}
	if *jsonOut {
		return encode(stdout, summaries)
	}
	for _, e := range summaries {
		status := fmt.Sprintf("rows=%d", e.RowCount)
		if e.Error != nil {
			status = e.Error.Code + ": " + e.Error.Message
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", e.ID, e.TS.Format(time.RFC3339), e.Actor, e.Title, status)
		fmt.Fprintf(stdout, "  why: %s\n  sql: %s\n", e.Why, e.SQL)
	}
	return nil
}

func showLog(args []string, stdout io.Writer) error {
	fs := newFlags("show")
	connName := fs.String("conn", "", "connection log name")
	offset := fs.Int("offset", 0, "stored row offset")
	limit := fs.Int("limit", query.DefaultPreviewLimit, "stored row page size (max 1000)")
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	var id string
	// Go's flag parser stops at a positional argument; support the documented
	// `show <id> --conn ...` as well as flags before the id.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if id == "" && fs.NArg() == 1 {
		id = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return errors.New("unexpected show arguments")
	}
	if id == "" || *connName == "" {
		return errors.New("usage: dbbie show <id> --conn <name> [--offset N] [--limit N] [--json]")
	}
	if *offset < 0 || *limit < 0 {
		return errors.New("--offset and --limit must be non-negative")
	}
	log, err := qlog.Open(*connName)
	if err != nil {
		return err
	}
	entry, err := log.Get(id)
	if err != nil {
		if errors.Is(err, qlog.ErrNotFound) {
			return fmt.Errorf("query log entry %s not found", id)
		}
		return err
	}
	start := min(*offset, len(entry.Rows))
	count := min(*limit, query.MaxPreviewLimit, len(entry.Rows)-start)
	entry.Rows = entry.Rows[start : start+count]
	if *jsonOut {
		return encode(stdout, entry)
	}
	fmt.Fprintf(stdout, "%s %s rows=%d page=%d:%d truncated=%t elapsed=%dms\n", entry.ID, entry.Title, entry.RowCount, start, start+count, entry.Truncated, entry.ElapsedMs)
	fmt.Fprintf(stdout, "why: %s\nsql: %s\n", entry.Why, entry.SQL)
	if entry.Error != nil {
		fmt.Fprintf(stdout, "%s: %s\n", entry.Error.Code, entry.Error.Message)
	}
	for _, row := range entry.Rows {
		fmt.Fprintln(stdout, row)
	}
	return nil
}
