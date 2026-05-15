package config

import (
	"path/filepath"
	"testing"

	"github.com/yeshwanthyk/dbbie/internal/db"
)

func TestSaveLoadUpsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := File{}
	cfg.Upsert(Connection{Name: "prod", Driver: db.Postgres, DSN: "postgres://one"})
	cfg.Upsert(Connection{Name: "prod", Driver: db.MySQL, DSN: "mysql://two"})

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Connections) != 1 {
		t.Fatalf("connections len = %d, want 1", len(got.Connections))
	}
	conn, ok := got.Find("prod")
	if !ok {
		t.Fatal("missing prod connection")
	}
	if conn.Driver != db.MySQL || conn.DSN != "mysql://two" {
		t.Fatalf("connection = %+v, want updated mysql entry", conn)
	}
}

func TestPublicConnectionsRedactsDSN(t *testing.T) {
	cfg := File{Connections: []Connection{{Name: "prod", Driver: db.Postgres, DSN: "postgres://secret"}}}
	public := cfg.PublicConnections()
	if len(public) != 1 {
		t.Fatalf("public len = %d, want 1", len(public))
	}
	if public[0].Name != "prod" || public[0].Driver != db.Postgres {
		t.Fatalf("public connection = %+v", public[0])
	}
}

func TestPathAllowsConfigOverride(t *testing.T) {
	t.Setenv("DBBIE_CONFIG", "/tmp/dbbie-test-config.json")
	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if path != "/tmp/dbbie-test-config.json" {
		t.Fatalf("path = %q", path)
	}
}
