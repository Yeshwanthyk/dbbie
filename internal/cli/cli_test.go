package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
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

func TestResolveDBSpecRejectsConnMixedWithRawDSN(t *testing.T) {
	_, _, err := resolveDBSpec(dbSpec{ConnName: "prod", Driver: "postgres"})
	if err == nil {
		t.Fatal("expected --conn conflict error")
	}
}
