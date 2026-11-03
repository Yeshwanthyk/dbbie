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
