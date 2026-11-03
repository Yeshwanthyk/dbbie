package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

func runQuery(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("query")
	spec := dbFlags(fs)
	sqlText := fs.String("sql", "", "SQL")
	limit := fs.Int("limit", query.DefaultPreviewLimit, "preview row limit (max 1000)")
	timeout := fs.Duration("timeout", 10*time.Second, "query timeout")
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	allowWrite := fs.Bool("allow-write", false, "allow non-read-only SQL")
	why := fs.String("why", "", "reason for this query")
	title := fs.String("title", "", "short query title")
	noLog := fs.Bool("no-log", os.Getenv("DBBIE_NO_LOG") == "1", "skip query logging")
	start := time.Now()
	runErr := fs.Parse(args)
	actor := os.Getenv("DBBIE_ACTOR")
	if actor == "" {
		actor = "human"
		if flagGiven(fs, "why") || !isTerminal(stdout) {
			actor = "agent"
		}
	}
	connName := spec.ConnName
	if connName == "" {
		connName = "adhoc"
	}
	entry := qlog.Entry{Conn: connName, Driver: spec.Driver, Source: "cli", Actor: actor, Title: *title, Why: *why, SQL: *sqlText}
	var result query.Result
	if runErr == nil {
		if fs.NArg() != 0 {
			runErr = commandError("invalid", "unexpected query arguments")
		} else if strings.TrimSpace(*sqlText) == "" {
			runErr = commandError("invalid", "sql is required")
		} else if !*allowWrite {
			if err := query.ValidateReadOnly(*sqlText); err != nil {
				runErr = commandError("rejected", err.Error())
			}
		}
	}
	// Resolve even rejected runs for the saved connection's driver, but never
	// open a database or send rejected SQL to it.
	kind, dsn, resolveErr := resolveDBSpec(*spec)
	if resolveErr == nil {
		entry.Driver = string(kind)
	}
	if runErr == nil {
		runErr = resolveErr
	}
	if runErr == nil {
		duration := *timeout
		if duration <= 0 {
			duration = 10 * time.Second
		}
		queryCtx, cancel := context.WithTimeout(ctx, duration)
		defer cancel()
		conn, err := openDatabase(queryCtx, kind, dsn)
		if err != nil {
			runErr = commandError(errorCode(databaseError(err)), "could not open database connection")
		} else {
			result, err = (query.Runner{DB: conn}).Run(queryCtx, query.Options{
				SQL: *sqlText, Limit: *limit, Timeout: *timeout, ReadOnly: !*allowWrite,
			})
			_ = conn.Close()
			if err != nil {
				runErr = databaseError(err)
			}
		}
	}
	entry.Columns, entry.Rows = result.Columns, result.Rows
	entry.RowCount, entry.Truncated = result.RowCount, result.Truncated
	entry.ElapsedMs = time.Since(start).Milliseconds()
	secrets := append(commandSecrets(append([]string{"query"}, args...)), dsn)
	if runErr != nil {
		runErr = commandError(errorCode(runErr), redact(runErr.Error(), secrets))
		entry.Error = errorDetail(runErr)
	}
	sanitizeEntry(&entry, secrets)
	if !*noLog && os.Getenv("DBBIE_NO_LOG") != "1" {
		log, err := qlog.Open(connName)
		if err == nil {
			entry, err = log.Append(entry)
		}
		if err != nil {
			message := "could not append query log: " + redact(err.Error(), secrets)
			if runErr != nil {
				message = runErr.Error() + "; " + message
			}
			return commandError(errorCodeOrInvalid(runErr), message)
		}
		result.ID = entry.ID
	}
	if runErr != nil {
		return runErr
	}
	result.Columns, result.Rows = entry.Columns, entry.Rows
	if *jsonOut {
		return encode(stdout, result)
	}
	fmt.Fprintf(stdout, "%s rows=%d truncated=%t elapsed=%s\n", result.ID, result.RowCount, result.Truncated, result.Elapsed)
	for _, row := range result.Rows {
		fmt.Fprintln(stdout, row)
	}
	return nil
}

func errorCodeOrInvalid(err error) string {
	if err == nil {
		return "invalid"
	}
	return errorCode(err)
}

func sanitizeEntry(e *qlog.Entry, secrets []string) {
	e.Driver = redact(e.Driver, secrets)
	e.Actor = redact(e.Actor, secrets)
	e.Title = redact(e.Title, secrets)
	e.Why = redact(e.Why, secrets)
	e.SQL = redact(e.SQL, secrets)
	for i := range e.Columns {
		e.Columns[i].Name = redact(e.Columns[i].Name, secrets)
		e.Columns[i].Database = redact(e.Columns[i].Database, secrets)
	}
	for _, row := range e.Rows {
		for i, value := range row {
			if s, ok := value.(string); ok {
				row[i] = redact(s, secrets)
			}
		}
	}
}

// Keep flags private to this command rather than treating '--why' inside SQL as
// an actor hint.
func flagGiven(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			given = true
		}
	})
	return given
}
