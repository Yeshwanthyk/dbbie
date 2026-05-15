package cli

import (
	"context"
	"database/sql"
	"flag"
	"io"

	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/theme"
	"github.com/yeshwanthyk/dbbie/internal/ui"
)

func openTUI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(stderr)
	connName := fs.String("conn", "", "saved connection name")
	driverRaw := fs.String("driver", "", "driver")
	dsn := fs.String("dsn", "", "dsn")
	themeName := fs.String("theme", "midnight", "theme name")
	limit := fs.Int("limit", 100, "preview row limit")
	sqlText := fs.String("sql", "", "initial read-only SQL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedTheme, err := theme.Resolve(*themeName)
	if err != nil {
		return err
	}
	return withOpenDB(ctx, dbSpec{ConnName: *connName, Driver: *driverRaw, DSN: *dsn}, func(ctx context.Context, kind db.Kind, conn *sql.DB) error {
		return ui.Run(ctx, ui.Options{Kind: kind, DB: conn, Theme: resolvedTheme, Limit: *limit, InitialSQL: *sqlText})
	})
}
