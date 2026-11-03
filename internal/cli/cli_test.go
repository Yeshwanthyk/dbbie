package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/yeshwanthyk/dbbie/internal/agent"
	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/session"
	"github.com/yeshwanthyk/dbbie/internal/ui"
)

func TestHelp(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "dbbie") {
		t.Fatalf("help output = %q", stdout.String())
	}
}

func TestDescribeAcceptsCommonDBFlags(t *testing.T) {
	err := Run(context.Background(), []string{"describe", "--driver", "sqlite", "--dsn", "x", "--table", "users"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected unsupported driver error")
	}
	if strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("describe rejected common flags before DB parsing: %v", err)
	}
	if !strings.Contains(err.Error(), "unsupported driver") {
		t.Fatalf("error = %v, want unsupported driver", err)
	}
}

func TestServeMCPFailsUntilImplemented(t *testing.T) {
	err := Run(context.Background(), []string{"serve-mcp"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected serve-mcp to fail until implemented")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("error = %v", err)
	}
}

func TestToolsJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run(context.Background(), []string{"tools", "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "run_readonly_query") {
		t.Fatalf("tools JSON = %s", stdout.String())
	}
}

func TestConnectionsJSONRedactsDSN(t *testing.T) {
	t.Setenv("DBBIE_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"connect", "prod", "postgres", "postgres://secret"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	stdout.Reset()
	if err := Run(context.Background(), []string{"connections", "--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("connections: %v", err)
	}
	if strings.Contains(stdout.String(), "secret") || strings.Contains(stdout.String(), "dsn") {
		t.Fatalf("connections leaked DSN: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "prod") {
		t.Fatalf("connections JSON missing name: %s", stdout.String())
	}
}

func TestOpenStartupSavedConnectionPolicy(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			t.Setenv("DBBIE_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			cfg := config.File{}
			for i := 0; i < count; i++ {
				cfg.Connections = append(cfg.Connections, config.Connection{Name: string(rune('a' + i)), Driver: db.Postgres, DSN: "private-credential"})
			}
			path, _ := config.Path()
			if err := config.Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			oldOpen, oldTUI := openDatabase, runTUI
			t.Cleanup(func() { openDatabase, runTUI = oldOpen, oldTUI })
			opened, ran := 0, false
			openDatabase = func(context.Context, db.Kind, string) (*sql.DB, error) {
				opened++
				conn, _, err := sqlmock.New()
				return conn, err
			}
			runTUI = func(_ context.Context, opts ui.Options) error {
				ran = true
				if len(opts.Connections) != count || opts.OpenConnection == nil {
					t.Fatal("missing public picker options")
				}
				if count == 1 && (opts.DB == nil || opts.Connection != "a") {
					t.Fatal("single saved connection not opened directly")
				}
				if count > 1 && (opts.DB != nil || opts.Connection != "") {
					t.Fatal("multiple connections opened before picker")
				}
				data, err := json.Marshal(opts.Connections)
				if err != nil || strings.Contains(string(data), "credential") || strings.Contains(string(data), "dsn") {
					t.Fatal("public picker options exposed DSN")
				}
				return nil
			}
			err := openTUI(context.Background(), nil, &bytes.Buffer{}, &bytes.Buffer{})
			if count == 0 {
				_, _, existingErr := resolveDBSpec(dbSpec{})
				if err == nil || err.Error() != existingErr.Error() || opened != 0 || ran {
					t.Fatal("missing saved connections should preserve usage error without opening UI/DB")
				}
			} else if err != nil || !ran || opened != boolToInt(count == 1) {
				t.Fatalf("open startup: err=%v ran=%t opened=%d", err, ran, opened)
			}
		})
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestOpenSessionUsesSessionConnectionAndDisablesRecentRestore(t *testing.T) {
	t.Setenv("DBBIE_STATE_DIR", t.TempDir())
	t.Setenv("DBBIE_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	path, _ := config.Path()
	if err := config.Save(path, config.File{Connections: []config.Connection{{Name: "local", Driver: db.Postgres, DSN: "local-secret"}, {Name: "other", Driver: db.MySQL, DSN: "other-secret"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create("demo", "", "local", "postgres", "human"); err != nil {
		t.Fatal(err)
	}
	oldOpen, oldTUI := openDatabase, runTUI
	t.Cleanup(func() { openDatabase, runTUI = oldOpen, oldTUI })
	opened := 0
	openDatabase = func(_ context.Context, kind db.Kind, dsn string) (*sql.DB, error) {
		opened++
		if kind != db.Postgres || dsn != "local-secret" {
			t.Fatal("session did not override --conn")
		}
		conn, _, err := sqlmock.New()
		return conn, err
	}
	runTUI = func(_ context.Context, opts ui.Options) error {
		if opts.Session != "demo" || opts.Connection != "local" || opts.Recent != 0 || !opts.Follow {
			t.Fatalf("session options: %+v", opts)
		}
		return nil
	}
	for _, args := range [][]string{{"--session", "demo", "--follow"}, {"--session", "demo", "--conn", "other", "--follow"}} {
		if err := openTUI(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	if opened != 2 {
		t.Fatal("wrong database open count")
	}
}

func TestOpenWorkspaceManifestIncludesSession(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"tools", "--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var manifest []agent.Tool
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, tool := range manifest {
		if tool.Name == "open_workspace" {
			if !strings.Contains(tool.Inputs["session"], "--session") || !strings.Contains(tool.Description, "stored result") {
				t.Fatal("session missing from open manifest")
			}
			return
		}
	}
	t.Fatal("open_workspace missing")
}

func TestResolveDBSpecRejectsConnMixedWithRawDSN(t *testing.T) {
	_, _, err := resolveDBSpec(dbSpec{ConnName: "prod", Driver: "postgres"})
	if err == nil {
		t.Fatal("expected --conn conflict error")
	}
}
