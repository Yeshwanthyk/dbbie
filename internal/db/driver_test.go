package db

import "testing"

func TestParseKindAliases(t *testing.T) {
	tests := map[string]Kind{
		"postgres":   Postgres,
		"postgresql": Postgres,
		"pg":         Postgres,
		"mysql":      MySQL,
		"mariadb":    MySQL,
	}
	for input, want := range tests {
		got, err := ParseKind(input)
		if err != nil {
			t.Fatalf("ParseKind(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseKind(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseKindRejectsUnknown(t *testing.T) {
	if _, err := ParseKind("sqlite"); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}
