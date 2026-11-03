package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

func sanitizeLogEntry(e *qlog.Entry, redact func(...string) ([]string, error)) error {
	fields := []*string{&e.Driver, &e.Source, &e.Actor, &e.Title, &e.Why, &e.SQL, &e.Session}
	if e.Error != nil {
		fields = append(fields, &e.Error.Message)
	}
	for i := range e.Columns {
		fields = append(fields, &e.Columns[i].Name, &e.Columns[i].Database)
	}
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		values = append(values, *field)
	}
	for _, row := range e.Rows {
		for _, value := range row {
			if text, ok := value.(string); ok {
				values = append(values, text)
			}
		}
	}
	clean, err := redact(values...)
	if err != nil {
		return err
	}
	for i, field := range fields {
		*field = clean[i]
	}
	i := len(fields)
	for _, row := range e.Rows {
		for col, value := range row {
			if _, ok := value.(string); ok {
				row[col] = clean[i]
				i++
			}
		}
	}
	return nil
}

func humanEntry(kind db.Kind, title, sql string, result query.Result, err error, elapsed time.Duration) qlog.Entry {
	e := qlog.Entry{Driver: string(kind), Source: "tui", Actor: "human", Title: title, SQL: sql,
		Columns: result.Columns, Rows: result.Rows, RowCount: result.RowCount,
		Truncated: result.Truncated, ElapsedMs: elapsed.Milliseconds()}
	if err != nil {
		code := "db"
		switch {
		case strings.TrimSpace(sql) == "":
			code = "invalid"
		case query.ValidateReadOnly(sql) != nil:
			code = "rejected"
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			code = "timeout"
		}
		e.Error = &qlog.Error{Code: code, Message: err.Error()}
	}
	return e
}
