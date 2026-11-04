package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yeshwanthyk/dbbie/internal/db"
)

func TestConcurrentUpdatesAndFailedRenamePreserveConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	const writers = 30
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- Update(path, func(cfg *File) error {
				cfg.Upsert(Connection{Name: fmt.Sprintf("conn%d", i), Driver: db.Postgres, DSN: "private"})
				return nil
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(path)
	if err != nil || len(cfg.Connections) != writers {
		t.Fatalf("lost update: %d %v", len(cfg.Connections), err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []func(*File) error{
		func(cfg *File) error { return cfg.Rename("conn0", "conn1") },
		func(cfg *File) error { return cfg.Remove("missing") },
		func(cfg *File) error { cfg.Upsert(Connection{Name: "partial"}); return errors.New("abort") },
	} {
		if err := Update(path, op); err == nil {
			t.Fatal("expected failure")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("failed update changed configuration")
		}
	}
}

func TestSaveRepairsPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, File{}); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode: %v %v", path, info, err)
		}
	}
}

func TestConnectionNamesCannotBePathsOrCredentials(t *testing.T) {
	for _, name := range []string{"local", "my-database", "Production", "read only"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("valid %q: %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "../config", "a/b", "a\\b", "a\x00", "a\n", "postgres://secret", "user:pass@tcp(localhost)", "host=localhost password=secret"} {
		if err := ValidateName(name); err == nil {
			t.Errorf("invalid name accepted: %q", name)
		}
	}
}
