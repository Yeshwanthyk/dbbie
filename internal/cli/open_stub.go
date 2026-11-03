package cli

import (
	"context"
	"database/sql"
	"errors"
	"io"

	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/theme"
	"github.com/yeshwanthyk/dbbie/internal/ui"
)

var runTUI = ui.Run

func openTUI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("open")
	fs.Bool("json", !isTerminal(stdout), "print errors as JSON")
	connName := fs.String("conn", "", "saved connection name")
	driverRaw := fs.String("driver", "", "driver")
	dsn := fs.String("dsn", "", "dsn")
	save := fs.String("save", "", "save ad-hoc connection under this name")
	themeName := fs.String("theme", "midnight", "theme name")
	limit := fs.Int("limit", 100, "preview row limit")
	sqlText := fs.String("sql", "", "initial read-only SQL")
	follow := fs.Bool("follow", false, "open new logged queries as tabs")
	recent := fs.Int("recent", 5, "stored queries to restore with --follow (0 disables)")
	noLog := fs.Bool("no-log", false, "do not log TUI queries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected open arguments")
	}
	if *recent < 0 {
		return errors.New("--recent must be non-negative")
	}
	resolvedTheme, err := theme.Resolve(*themeName)
	if err != nil {
		return err
	}
	spec := dbSpec{ConnName: *connName, Driver: *driverRaw, DSN: *dsn}
	if err := saveDBSpec(&spec, *save); err != nil {
		return err
	}
	return withOpenDB(ctx, spec, func(ctx context.Context, kind db.Kind, conn *sql.DB) error {
		name := spec.ConnName
		if name == "" {
			name = "adhoc"
		}
		return runTUI(ctx, ui.Options{Kind: kind, DB: conn, Theme: resolvedTheme, Limit: *limit, InitialSQL: *sqlText,
			Connection: name, Follow: *follow, Recent: *recent, NoLog: *noLog})
	})
}
