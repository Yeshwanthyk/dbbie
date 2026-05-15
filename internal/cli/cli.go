package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/catalog"
	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
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
		return withDB(ctx, args[1:], stdout, func(ctx context.Context, kind db.Kind, conn *sql.DB, jsonOut bool) error {
			tables, err := catalog.NewInspector(kind, conn).Tables(ctx)
			if err != nil {
				return err
			}
			if jsonOut {
				return encode(stdout, tables)
			}
			for _, t := range tables {
				fmt.Fprintf(stdout, "%s.%s\t%s\n", t.Schema, t.Name, t.Type)
			}
			return nil
		})
	case "describe":
		fs := flag.NewFlagSet("describe", flag.ContinueOnError)
		fs.SetOutput(stderr)
		connName := fs.String("conn", "", "saved connection name")
		driverRaw := fs.String("driver", "", "driver")
		dsn := fs.String("dsn", "", "dsn")
		jsonOut := fs.Bool("json", false, "print JSON")
		table := fs.String("table", "", "table name")
		schema := fs.String("schema", "", "schema name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *table == "" {
			return errors.New("--table is required")
		}
		return withOpenDB(ctx, dbSpec{ConnName: *connName, Driver: *driverRaw, DSN: *dsn}, func(ctx context.Context, kind db.Kind, conn *sql.DB) error {
			resolvedSchema := *schema
			if resolvedSchema == "" {
				if kind == db.Postgres {
					resolvedSchema = "public"
				}
			}
			columns, err := catalog.NewInspector(kind, conn).Columns(ctx, resolvedSchema, *table)
			if err != nil {
				return err
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
	case "query":
		return runQuery(ctx, args[1:], stdout, stderr)
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
  dbbie connect <name> <driver> <dsn>
  dbbie connections [--json]
  dbbie tables (--conn <name> | --driver <postgres|mysql> --dsn <dsn>) [--json]
  dbbie describe (--conn <name> | --driver <postgres|mysql> --dsn <dsn>) --table <table> [--schema <schema>] [--json]
  dbbie query (--conn <name> | --driver <postgres|mysql> --dsn <dsn>) --sql <sql> [--limit 100] [--timeout 10s] [--json]
  dbbie open (--conn <name> | --driver <postgres|mysql> --dsn <dsn>) [--sql <sql>]`)
}

func connect(args []string, stdout io.Writer) error {
	if len(args) != 3 {
		return errors.New("usage: dbbie connect <name> <driver> <dsn>")
	}
	kind, err := db.ParseKind(args[1])
	if err != nil {
		return err
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.Upsert(config.Connection{Name: args[0], Driver: kind, DSN: args[2]})
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "saved %s (%s)\n", args[0], kind)
	return nil
}

func connections(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("connections", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
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

func withDB(ctx context.Context, args []string, stdout io.Writer, fn func(context.Context, db.Kind, *sql.DB, bool) error) error {
	fs := flag.NewFlagSet("db", flag.ContinueOnError)
	connName := fs.String("conn", "", "saved connection name")
	driverRaw := fs.String("driver", "", "driver")
	dsn := fs.String("dsn", "", "dsn")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withOpenDB(ctx, dbSpec{ConnName: *connName, Driver: *driverRaw, DSN: *dsn}, func(ctx context.Context, kind db.Kind, conn *sql.DB) error {
		return fn(ctx, kind, conn, *jsonOut)
	})
}

type dbSpec struct {
	ConnName string
	Driver   string
	DSN      string
}

func withOpenDB(ctx context.Context, spec dbSpec, fn func(context.Context, db.Kind, *sql.DB) error) error {
	kind, dsn, err := resolveDBSpec(spec)
	if err != nil {
		return err
	}
	driver, err := db.NewSQLDriver(kind)
	if err != nil {
		return err
	}
	conn, err := driver.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(ctx, kind, conn)
}

func resolveDBSpec(spec dbSpec) (db.Kind, string, error) {
	if spec.ConnName != "" {
		if spec.Driver != "" || spec.DSN != "" {
			return "", "", errors.New("--conn cannot be combined with --driver or --dsn")
		}
		path, err := config.Path()
		if err != nil {
			return "", "", err
		}
		cfg, err := config.Load(path)
		if err != nil {
			return "", "", err
		}
		conn, ok := cfg.Find(spec.ConnName)
		if !ok {
			return "", "", fmt.Errorf("unknown connection: %s", spec.ConnName)
		}
		return conn.Driver, conn.DSN, nil
	}
	kind, err := db.ParseKind(spec.Driver)
	if err != nil {
		return "", "", err
	}
	return kind, spec.DSN, nil
}

func runQuery(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(stderr)
	connName := fs.String("conn", "", "saved connection name")
	driverRaw := fs.String("driver", "", "driver")
	dsn := fs.String("dsn", "", "dsn")
	sqlText := fs.String("sql", "", "sql")
	limit := fs.Int("limit", query.DefaultPreviewLimit, "preview row limit")
	timeout := fs.Duration("timeout", 10*time.Second, "query timeout")
	jsonOut := fs.Bool("json", false, "print JSON")
	allowWrite := fs.Bool("allow-write", false, "allow non-read-only SQL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withOpenDB(ctx, dbSpec{ConnName: *connName, Driver: *driverRaw, DSN: *dsn}, func(ctx context.Context, _ db.Kind, conn *sql.DB) error {
		result, err := query.Runner{DB: conn}.Run(ctx, query.Options{
			SQL:      *sqlText,
			Limit:    *limit,
			Timeout:  *timeout,
			ReadOnly: !*allowWrite,
		})
		if err != nil {
			return err
		}
		if *jsonOut {
			return encode(stdout, result)
		}
		fmt.Fprintf(stdout, "%s rows=%d truncated=%t elapsed=%s\n", result.ID, result.RowCount, result.Truncated, result.Elapsed)
		for _, row := range result.Rows {
			fmt.Fprintln(stdout, row)
		}
		return nil
	})
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
