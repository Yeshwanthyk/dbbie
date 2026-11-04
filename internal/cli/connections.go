package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
)

func manageConnection(args []string, stdout io.Writer) error {
	fs := newFlags("connect")
	jsonOut := fs.Bool("json", !isTerminal(stdout), "print JSON")
	mode := "save"
	var name, driver, dsn, old string
	if len(args) > 0 && args[0] == "--rm" {
		mode = "remove"
		if len(args) < 2 {
			return errors.New("usage: dbbie connect --rm <name> [--json]")
		}
		name, args = args[1], args[2:]
	} else if len(args) > 0 && args[0] == "--rename" {
		mode = "rename"
		if len(args) < 3 {
			return errors.New("usage: dbbie connect --rename <old> <new> [--json]")
		}
		old, name, args = args[1], args[2], args[3:]
	} else {
		if len(args) < 3 || strings.HasPrefix(args[0], "-") {
			return errors.New("usage: dbbie connect <name> <driver> <dsn> | --rm <name> | --rename <old> <new> [--json]")
		}
		name, driver, dsn, args = args[0], args[1], args[2], args[3:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected connect arguments")
	}
	if mode != "remove" {
		if err := config.ValidateName(name); err != nil {
			return err
		}
	}
	p, err := config.Path()
	if err != nil {
		return err
	}
	var public config.PublicConnection
	err = config.Update(p, func(cfg *config.File) error {
		switch mode {
		case "remove":
			return cfg.Remove(name)
		case "rename":
			if err := cfg.Rename(old, name); err != nil {
				return err
			}
			conn, _ := cfg.Find(name)
			public = config.PublicConnection{Name: name, Driver: conn.Driver}
		default:
			kind, err := db.ParseKind(driver)
			if err != nil {
				return err
			}
			cfg.Upsert(config.Connection{Name: name, Driver: kind, DSN: dsn})
			public = config.PublicConnection{Name: name, Driver: kind}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		if mode == "remove" {
			return encode(stdout, map[string]string{"removed": name})
		}
		return encode(stdout, public)
	}
	fmt.Fprintf(stdout, "%s %s\n", mode, name)
	return nil
}

// Save only ad-hoc credentials, then replace the spec with its saved name.
// open uses this before handing the connection to the unchanged TUI.
func saveDBSpec(spec *dbSpec, name string) error {
	if name == "" {
		return nil
	}
	if spec.ConnName != "" {
		return errors.New("--save requires --driver and --dsn, not --conn")
	}
	if err := config.ValidateName(name); err != nil {
		return err
	}
	kind, dsn, err := resolveDBSpec(*spec)
	if err != nil {
		return err
	}
	p, err := config.Path()
	if err != nil {
		return err
	}
	if err := config.Update(p, func(cfg *config.File) error {
		cfg.Upsert(config.Connection{Name: name, Driver: kind, DSN: dsn})
		return nil
	}); err != nil {
		return err
	}
	*spec = dbSpec{ConnName: name}
	return nil
}
