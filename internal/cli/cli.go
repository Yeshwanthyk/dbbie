package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path"

	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	jsonOut := outputJSON(args, stdout)
	// Redact at the output boundary too, including echoed invalid flag values.
	secrets := commandSecrets(args)
	out := redactedWriter{Writer: stdout, secrets: secrets}
	err := run(ctx, args, out, stderr)
	if errors.Is(err, flag.ErrHelp) {
		usage(out)
		return nil
	}
	if err != nil {
		err = commandError(errorCode(err), redact(err.Error(), secrets))
		if jsonOut {
			if encodeErr := encode(out, map[string]any{"error": errorDetail(err)}); encodeErr != nil {
				return encodeErr
			}
		}
	}
	return err
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		usage(stdout)
		return nil
	}
	switch args[0] {
	case "connect":
		return connect(args[1:], stdout)
	case "connections":
		return connections(args[1:], stdout)
	case "tools":
		return tools(args[1:], stdout, stderr)
	case "tables":
		return tables(ctx, args[1:], stdout)
	case "describe":
		return describe(ctx, args[1:], stdout)
	case "query":
		return runQuery(ctx, args[1:], stdout, stderr)
	case "log":
		return listLog(args[1:], stdout)
	case "show":
		return showLog(args[1:], stdout)
	case "open":
		return openTUI(ctx, args[1:], stdout, stderr)
	case "serve-mcp":
		return errors.New("serve-mcp is not implemented yet; use JSON CLI commands for the current AI-first contract")
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `dbbie - DB-agnostic terminal cockpit for Postgres and MySQL

Usage:
  dbbie connect <name> <driver> <dsn> [--json]
  dbbie connections [--json]
  dbbie tables (--conn <name> | --driver <postgres|mysql> --dsn <dsn>) [--match <glob>] [--json]
  dbbie describe (--conn <name> | --driver <postgres|mysql> --dsn <dsn>) --table <table> [--schema <schema>] [--json]
  dbbie query (--conn <name> | --driver <postgres|mysql> --dsn <dsn>) --sql <sql> [--limit 100] [--timeout 10s] [--why <text>] [--title <text>] [--no-log] [--json]
  dbbie log --conn <name> [--limit 20] [--json]
  dbbie show <id> --conn <name> [--offset 0] [--limit 100] [--json]
  dbbie open (--conn <name> | --driver <postgres|mysql> --dsn <dsn>) [--sql <sql>] [--follow] [--recent 5] [--no-log]
  dbbie tools [--json]
  dbbie serve-mcp (not implemented)

JSON is the default when stdout is not a terminal; --json=false prints human-readable output.`)
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	// Errors are rendered once by Run, without leaking flag arguments to stderr.
	fs.SetOutput(io.Discard)
	return fs
}

func connect(args []string, stdout io.Writer) error {
	fs := newFlags("connect")
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	if len(args) < 3 {
		return errors.New("usage: dbbie connect <name> <driver> <dsn> [--json]")
	}
	if err := fs.Parse(args[3:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected connect arguments")
	}
	kind, err := db.ParseKind(args[1])
	if err != nil {
		return err
	}
	configPath, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg.Upsert(config.Connection{Name: args[0], Driver: kind, DSN: args[2]})
	if err := config.Save(configPath, cfg); err != nil {
		return err
	}
	if *jsonOut {
		return encode(stdout, config.PublicConnection{Name: args[0], Driver: kind})
	}
	fmt.Fprintf(stdout, "saved %s (%s)\n", args[0], kind)
	return nil
}

func connections(args []string, stdout io.Writer) error {
	fs := newFlags("connections")
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected connections arguments")
	}
	configPath, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if *jsonOut {
		return encode(stdout, cfg.PublicConnections())
	}
	for _, conn := range cfg.Connections {
		fmt.Fprintf(stdout, "%s\t%s\n", conn.Name, conn.Driver)
	}
	return nil
}

func tables(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlags("tables")
	spec := dbFlags(fs)
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	match := fs.String("match", "", "filter names or schema.name by glob")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected tables arguments")
	}
	if _, err := path.Match(*match, ""); err != nil {
		return fmt.Errorf("invalid --match glob: %w", err)
	}
	return withOpenDB(ctx, *spec, func(ctx context.Context, kind db.Kind, conn *sql.DB) error {
		all, err := catalog.NewInspector(kind, conn).Tables(ctx)
		if err != nil {
			return databaseError(err)
		}
		filtered := make([]catalog.Table, 0, len(all))
		for _, table := range all {
			nameMatch, _ := path.Match(*match, table.Name)
			fullMatch, _ := path.Match(*match, table.Schema+"."+table.Name)
			if *match == "" || nameMatch || fullMatch {
				filtered = append(filtered, table)
			}
		}
		if *jsonOut {
			return encode(stdout, filtered)
		}
		for _, t := range filtered {
			fmt.Fprintf(stdout, "%s.%s\t%s\n", t.Schema, t.Name, t.Type)
		}
		return nil
	})
}

func describe(ctx context.Context, args []string, stdout io.Writer) error {
	fs := newFlags("describe")
	spec := dbFlags(fs)
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	table := fs.String("table", "", "table name")
	schema := fs.String("schema", "", "schema name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *table == "" || fs.NArg() != 0 {
		return errors.New("--table is required; unexpected positional arguments are not allowed")
	}
	return withOpenDB(ctx, *spec, func(ctx context.Context, kind db.Kind, conn *sql.DB) error {
		resolvedSchema := *schema
		if resolvedSchema == "" && kind == db.Postgres {
			resolvedSchema = "public"
		}
		columns, err := catalog.NewInspector(kind, conn).Columns(ctx, resolvedSchema, *table)
		if err != nil {
			return databaseError(err)
		}
		if *jsonOut {
			return encode(stdout, columns)
		}
		for _, c := range columns {
			nullable := "not null"
			if c.Nullable {
				nullable = "null"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", c.Name, c.DataType, nullable)
		}
		return nil
	})
}

type dbSpec struct {
	ConnName string
	Driver   string
	DSN      string
}

func dbFlags(fs *flag.FlagSet) *dbSpec {
	spec := &dbSpec{}
	fs.StringVar(&spec.ConnName, "conn", "", "saved connection name")
	fs.StringVar(&spec.Driver, "driver", "", "postgres or mysql")
	fs.StringVar(&spec.DSN, "dsn", "", "connection DSN (never printed)")
	return spec
}

// Replaced by SQL-mock tests; production always opens through the shared driver.
var openDatabase = func(ctx context.Context, kind db.Kind, dsn string) (*sql.DB, error) {
	driver, err := db.NewSQLDriver(kind)
	if err != nil {
		return nil, err
	}
	return driver.Open(ctx, dsn)
}

func withOpenDB(ctx context.Context, spec dbSpec, fn func(context.Context, db.Kind, *sql.DB) error) error {
	kind, dsn, err := resolveDBSpec(spec)
	if err != nil {
		return err
	}
	conn, err := openDatabase(ctx, kind, dsn)
	if err != nil {
		// Driver parsing and connection errors may contain fragments of credentials.
		return commandError(errorCode(databaseError(err)), "could not open database connection")
	}
	defer conn.Close()
	err = fn(ctx, kind, conn)
	if err != nil {
		return commandError(errorCode(err), redact(err.Error(), []string{dsn}))
	}
	return nil
}

func resolveDBSpec(spec dbSpec) (db.Kind, string, error) {
	if spec.ConnName != "" {
		if spec.Driver != "" || spec.DSN != "" {
			return "", "", errors.New("--conn cannot be combined with --driver or --dsn")
		}
		configPath, err := config.Path()
		if err != nil {
			return "", "", err
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			return "", "", errors.New("could not read connection configuration")
		}
		conn, ok := cfg.Find(spec.ConnName)
		if !ok {
			return "", "", fmt.Errorf("unknown connection: %s", spec.ConnName)
		}
		kind, err := db.ParseKind(string(conn.Driver))
		return kind, conn.DSN, err
	}
	kind, err := db.ParseKind(spec.Driver)
	if err != nil {
		return "", "", err
	}
	if spec.DSN == "" {
		return "", "", errors.New("dsn is required")
	}
	return kind, spec.DSN, nil
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
