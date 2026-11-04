package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/session"
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
	sessionName := fs.String("session", os.Getenv("DBBIE_SESSION"), "append to a named session on this saved connection")
	save := fs.String("save", "", "save ad-hoc connection under this name")
	parseErr := fs.Parse(args)
	if parseErr == nil && fs.NArg() != 0 {
		parseErr = commandError("invalid", "unexpected query arguments")
	}
	if parseErr == nil {
		if err := saveDBSpec(spec, *save); err != nil {
			return err
		}
	}
	actor := queryActor(stdout, flagGiven(fs, "why"))
	if parseErr == nil && *sessionName != "" {
		if *noLog || os.Getenv("DBBIE_NO_LOG") == "1" {
			return errors.New("--session requires query logging; unset DBBIE_SESSION or enable logging")
		}
		if err := ensureSession(*sessionName, *spec, actor); err != nil {
			return err
		}
	}
	result, entry, runErr := executeQuery(ctx, queryRun{
		Spec: *spec, SQL: *sqlText, Title: *title, Why: *why, Actor: actor,
		Session: *sessionName, Limit: *limit, Timeout: *timeout, AllowWrite: *allowWrite,
		NoLog: *noLog || os.Getenv("DBBIE_NO_LOG") == "1", InitialError: parseErr,
		Secrets: commandSecrets(append([]string{"query"}, args...)),
	})
	if parseErr == nil && *sessionName != "" && entry.ID != "" {
		_, err := session.Add(*sessionName, session.Item{Title: entry.Title, Why: entry.Why, SQL: entry.SQL, Conn: entry.Conn, LogID: entry.ID, AddedBy: actor})
		if err != nil {
			return commandError(errorCodeOrInvalid(runErr), joinRunError(runErr, "could not append session item: "+err.Error()))
		}
	}
	if runErr != nil {
		return runErr
	}
	return printQueryResult(stdout, *jsonOut, result)
}

func queryActor(stdout io.Writer, hasWhy bool) string {
	if actor := os.Getenv("DBBIE_ACTOR"); actor != "" {
		return actor
	}
	if hasWhy || !isTerminal(stdout) {
		return "agent"
	}
	return "human"
}

func ensureSession(name string, spec dbSpec, actor string) error {
	if err := session.ValidateName(name); err != nil {
		return err
	}
	if spec.ConnName == "" {
		return errors.New("--session requires a saved --conn (or ad-hoc --save)")
	}
	kind, _, err := resolveDBSpec(spec)
	if err != nil {
		return err
	}
	s, err := session.Open(name)
	if errors.Is(err, session.ErrNotFound) {
		s, err = session.Create(name, "", spec.ConnName, string(kind), actor)
		if errors.Is(err, session.ErrExists) {
			s, err = session.Open(name)
		}
	}
	if err != nil {
		return err
	}
	if s.Conn != spec.ConnName {
		return errors.New("session connection must match --conn")
	}
	return nil
}

type queryRun struct {
	Spec                            dbSpec
	SQL, Title, Why, Actor, Session string
	Limit                           int
	Timeout                         time.Duration
	AllowWrite, NoLog               bool
	InitialError                    error
	Secrets                         []string
}

// Shared execution/safety/logging path for query and session run. A session run
// never supplies AllowWrite or NoLog, including when DBBIE_NO_LOG is set.
func executeQuery(ctx context.Context, opts queryRun) (query.Result, qlog.Entry, error) {
	start := time.Now()
	runErr := opts.InitialError
	connName := opts.Spec.ConnName
	if connName == "" {
		connName = "adhoc"
	}
	entry := qlog.Entry{Conn: connName, Driver: opts.Spec.Driver, Source: "cli", Actor: opts.Actor, Title: opts.Title, Why: opts.Why, SQL: opts.SQL, Session: opts.Session}
	var result query.Result
	if runErr == nil {
		if strings.TrimSpace(opts.SQL) == "" {
			runErr = commandError("invalid", "sql is required")
		} else if !opts.AllowWrite {
			if err := query.ValidateReadOnly(opts.SQL); err != nil {
				runErr = commandError("rejected", err.Error())
			}
		}
	}
	// Resolve even rejected SQL for the driver, but never send it to the DB.
	kind, dsn, resolveErr := resolveDBSpec(opts.Spec)
	if resolveErr == nil {
		entry.Driver = string(kind)
	}
	if runErr == nil {
		runErr = resolveErr
	}
	if runErr == nil {
		duration := opts.Timeout
		if duration <= 0 {
			duration = 10 * time.Second
		}
		queryCtx, cancel := context.WithTimeout(ctx, duration)
		defer cancel()
		conn, err := openDatabase(queryCtx, kind, dsn)
		if err != nil {
			runErr = commandError(errorCode(databaseError(err)), "could not open database connection")
		} else {
			result, err = (query.Runner{DB: conn}).Run(queryCtx, query.Options{SQL: opts.SQL, Limit: opts.Limit, Timeout: opts.Timeout, ReadOnly: !opts.AllowWrite})
			_ = conn.Close()
			if err != nil {
				runErr = databaseError(err)
			}
		}
	}
	entry.Columns, entry.Rows = result.Columns, result.Rows
	entry.RowCount, entry.Truncated = result.RowCount, result.Truncated
	entry.ElapsedMs = time.Since(start).Milliseconds()
	secrets := append(opts.Secrets, dsn)
	if runErr != nil {
		runErr = commandError(errorCode(runErr), redact(runErr.Error(), secrets))
		entry.Error = errorDetail(runErr)
	}
	sanitizeEntry(&entry, secrets)
	if !opts.NoLog {
		log, err := qlog.Open(connName)
		if err == nil {
			entry, err = log.Append(entry)
		}
		if err != nil {
			return result, entry, commandError(errorCodeOrInvalid(runErr), joinRunError(runErr, "could not append query log: "+redact(err.Error(), secrets)))
		}
		result.ID = entry.ID
	}
	result.Columns, result.Rows = entry.Columns, entry.Rows
	return result, entry, runErr
}

func joinRunError(err error, message string) string {
	if err != nil {
		return err.Error() + "; " + message
	}
	return message
}

func printQueryResult(stdout io.Writer, jsonOut bool, result query.Result) error {
	if jsonOut {
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
	e.Session = redact(e.Session, secrets)
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

func flagGiven(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			given = true
		}
	})
	return given
}
