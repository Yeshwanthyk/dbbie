package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/session"
	"github.com/yeshwanthyk/dbbie/internal/theme"
	"github.com/yeshwanthyk/dbbie/internal/ui"
)

var runTUI = ui.Run

// Resolve the launch policy before opening any database. Multiple saved names
// deliberately leave the spec empty so the TUI can present its startup picker.
func openSpec(spec dbSpec, sessionName string, connections []config.PublicConnection) (dbSpec, bool, error) {
	if sessionName != "" {
		trail, err := session.Open(sessionName)
		if err != nil {
			return spec, false, err
		}
		if spec.Driver != "" || spec.DSN != "" || (spec.ConnName != "" && spec.ConnName != trail.Conn) {
			return spec, false, fmt.Errorf("session %s uses connection %s; drop --conn/--dsn or pass --conn %s", trail.Name, trail.Conn, trail.Conn)
		}
		return dbSpec{ConnName: trail.Conn}, false, nil
	}
	if spec.ConnName != "" || spec.Driver != "" || spec.DSN != "" {
		return spec, false, nil
	}
	if len(connections) == 1 {
		return dbSpec{ConnName: connections[0].Name}, false, nil
	}
	if len(connections) > 1 {
		return spec, true, nil
	}
	// Preserve the existing missing-connection usage error.
	_, _, err := resolveDBSpec(spec)
	return spec, false, err
}

func openTUI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("open")
	fs.Bool("json", !isTerminal(stdout), "print errors as JSON")
	connName := fs.String("conn", "", "saved connection name")
	driverRaw := fs.String("driver", "", "driver")
	dsn := fs.String("dsn", "", "dsn")
	save := fs.String("save", "", "save ad-hoc connection under this name")
	sessionName := fs.String("session", "", "restore stored session tabs without running SQL")
	themeName := fs.String("theme", "midnight", "theme name")
	limit := fs.Int("limit", 100, "preview row limit")
	sqlText := fs.String("sql", "", "initial read-only SQL")
	follow := fs.Bool("follow", false, "open new logged queries as tabs")
	recent := fs.Int("recent", 5, "stored queries to restore with --follow (0 disables)")
	noLog := fs.Bool("no-log", false, "do not log TUI queries (r always logs)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected open arguments")
	}
	if *recent < 0 {
		return errors.New("--recent must be non-negative")
	}
	if *sessionName != "" && (*sqlText != "" || *save != "") {
		return errors.New("--session cannot be combined with --sql or --save")
	}
	resolvedTheme, err := theme.Resolve(*themeName)
	if err != nil {
		return err
	}
	spec := dbSpec{ConnName: *connName, Driver: *driverRaw, DSN: *dsn}
	if err := saveDBSpec(&spec, *save); err != nil {
		return err
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return errors.New("could not read connection configuration")
	}
	spec, pick, err := openSpec(spec, *sessionName, cfg.PublicConnections())
	if err != nil {
		return err
	}
	if *sessionName != "" {
		*recent = 0
	}

	// Handles stay alive for background queries and kept tabs until the UI has
	// drained its workers. Credentials never cross into ui.Options.
	var mu sync.Mutex
	var opened []*sql.DB
	defer func() {
		for _, conn := range opened {
			_ = conn.Close()
		}
	}()
	opener := func(ctx context.Context, name string) (db.Kind, *sql.DB, error) {
		kind, dsn, err := resolveDBSpec(dbSpec{ConnName: name})
		if err != nil {
			return "", nil, errors.New("could not resolve saved connection")
		}
		conn, err := openDatabase(ctx, kind, dsn)
		if err != nil {
			return "", nil, errors.New("could not open database connection")
		}
		mu.Lock()
		opened = append(opened, conn)
		mu.Unlock()
		return kind, conn, nil
	}
	opts := ui.Options{Theme: resolvedTheme, Limit: *limit, InitialSQL: *sqlText, Session: *sessionName,
		Follow: *follow, Recent: *recent, NoLog: *noLog, Connections: cfg.PublicConnections(), OpenConnection: opener}
	opts.RedactStrings = func(values ...string) ([]string, error) {
		clean, err := config.RedactStrings(values...)
		for i := range clean {
			clean[i] = redact(clean[i], []string{spec.DSN})
		}
		return clean, err
	}
	if pick {
		return runTUI(ctx, opts)
	}
	return withOpenDB(ctx, spec, func(ctx context.Context, kind db.Kind, conn *sql.DB) error {
		name := spec.ConnName
		if name == "" {
			name = "adhoc"
		}
		opts.Kind, opts.DB, opts.Connection = kind, conn, name
		opts.SavedConnection = spec.ConnName != ""
		return runTUI(ctx, opts)
	})
}
