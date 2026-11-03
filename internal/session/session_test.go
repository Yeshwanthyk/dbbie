package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/db"
)

func setup(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("DBBIE_STATE_DIR", state)
	t.Setenv("DBBIE_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	return state
}

func TestLifecycleStableNumbersAndListOrder(t *testing.T) {
	setup(t)
	s, err := Create("first", "First", "local", "postgres", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if s.V != 1 || s.Name != "first" || s.Title != "First" || s.CreatedBy != "agent" || s.Conn != "local" || s.Driver != "postgres" || len(s.Items) != 0 || s.Created.IsZero() || !s.Created.Equal(s.Updated) {
		t.Fatalf("created: %+v", s)
	}
	if _, err := Create("first", "", "local", "postgres", "agent"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	for n := 1; n <= 3; n++ {
		item, err := Add("first", Item{Title: "one", SQL: "select 1", Why: "verify", LogID: "q_0001", AddedBy: "agent"})
		if err != nil {
			t.Fatal(err)
		}
		if item.N != n || item.Conn != "local" || item.Added.IsZero() {
			t.Fatalf("item: %+v", item)
		}
	}
	if err := Remove("first", 2); err != nil {
		t.Fatal(err)
	}
	if err := Remove("first", 3); err != nil {
		t.Fatal(err)
	}
	item, err := Add("first", Item{SQL: "select 2"})
	if err != nil || item.N != 4 {
		t.Fatalf("must not reuse removed number: %+v, %v", item, err)
	}
	if err := SetLogID("first", 4, "q_0009"); err != nil {
		t.Fatal(err)
	}
	s, err = Open("first")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 2 || s.Items[0].N != 1 || s.Items[1].N != 4 || s.Items[1].LogID != "q_0009" || !s.Updated.After(s.Created) {
		t.Fatalf("stored: %+v", s)
	}
	if _, err := Create("second", "", "local", "postgres", "human"); err != nil {
		t.Fatal(err)
	}
	list, err := List()
	if err != nil || len(list) != 2 || list[0].Name != "second" || list[1].Items != 2 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	time.Sleep(time.Millisecond)
	if err := SetLogID("first", 1, "q_0010"); err != nil {
		t.Fatal(err)
	}
	list, err = List()
	if err != nil || list[0].Name != "first" {
		t.Fatalf("updated order: %+v, %v", list, err)
	}
	if err := Delete("first"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open("first"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open removed: %v", err)
	}
	if err := Delete("first"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestNameValidation(t *testing.T) {
	setup(t)
	for _, name := range []string{"a", "1", "refund-bug", "a.b_c-0", strings.Repeat("a", 64)} {
		if err := ValidateName(name); err != nil {
			t.Errorf("valid %q: %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "../x", "a/b", "a\\b", "UPPER", "-a", "_a", "a b", "a\n", "a\x00", "é", strings.Repeat("a", 65)} {
		if err := ValidateName(name); err == nil {
			t.Errorf("accepted %q", name)
		}
		if _, err := Create(name, "", "local", "postgres", "agent"); err == nil {
			t.Errorf("created %q", name)
		}
		if _, err := Open(name); err == nil {
			t.Errorf("opened %q", name)
		}
		if err := Delete(name); err == nil {
			t.Errorf("deleted %q", name)
		}
	}
}

func TestPermissionsNoDSNAndFailedMutationIsAtomic(t *testing.T) {
	state := setup(t)
	p, _ := config.Path()
	secret := "user:secret@tcp(localhost:3306)/db"
	if err := config.Save(p, config.File{Connections: []config.Connection{{Name: "local", Driver: db.MySQL, DSN: secret}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create("trail", secret, "local", "mysql", secret); err != nil {
		t.Fatal(err)
	}
	if _, err := Add("trail", Item{Title: secret, Why: secret, SQL: "select '" + secret + "'", AddedBy: secret}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{state, filepath.Join(state, "sessions"), filepath.Join(state, "sessions", "trail.json"), filepath.Join(state, "sessions", ".trail.lock")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s permission %o != %o", p, info.Mode().Perm(), want)
		}
	}
	path := filepath.Join(state, "sessions", "trail.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), secret) || strings.Contains(string(before), `"dsn"`) {
		t.Fatalf("credentials in session: %s", before)
	}
	for _, op := range []func() error{
		func() error { _, err := Add("trail", Item{Conn: "other", SQL: "select 1"}); return err },
		func() error { return Remove("trail", 99) },
		func() error { return SetLogID("trail", 99, "q_0001") },
		func() error { return SetLogID("trail", 1, "postgres://secret") },
	} {
		if err := op(); err == nil {
			t.Fatal("expected failure")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("failed mutation changed session: %v", err)
		}
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".tmp") {
			t.Fatalf("leaked temp: %s", file.Name())
		}
	}
}

func TestConcurrentAddsAndAtomicReaders(t *testing.T) {
	state := setup(t)
	if _, err := Create("concurrent", "", "local", "postgres", "agent"); err != nil {
		t.Fatal(err)
	}
	const writers = 40
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	stop, readerDone := make(chan struct{}), make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				readerDone <- nil
				return
			default:
			}
			data, err := os.ReadFile(filepath.Join(state, "sessions", "concurrent.json"))
			if err != nil {
				readerDone <- err
				return
			}
			if !json.Valid(data) {
				readerDone <- fmt.Errorf("torn JSON: %s", data)
				return
			}
		}
	}()
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := Add("concurrent", Item{SQL: "select 1"}); errs <- err }()
	}
	wg.Wait()
	close(errs)
	close(stop)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	s, err := Open("concurrent")
	if err != nil || len(s.Items) != writers {
		t.Fatalf("lost updates: %+v %v", s, err)
	}
	for i, item := range s.Items {
		if item.N != i+1 {
			t.Fatalf("sequence: %+v", s.Items)
		}
	}
}

func TestProcessWriter(t *testing.T) {
	if os.Getenv("DBBIE_SESSION_TEST_CHILD") != "1" {
		return
	}
	for i := 0; i < 8; i++ {
		if _, err := Add("processes", Item{SQL: "select 1"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFlockAcrossProcesses(t *testing.T) {
	setup(t)
	if _, err := Create("processes", "", "local", "postgres", "agent"); err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 3)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestProcessWriter$")
		commands[i].Env = append(os.Environ(), "DBBIE_SESSION_TEST_CHILD=1")
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open("processes")
	if err != nil || len(s.Items) != 24 {
		t.Fatalf("process updates: %+v, %v", s, err)
	}
}

func TestStateDirectoryFallbacks(t *testing.T) {
	setup(t)
	t.Setenv("DBBIE_STATE_DIR", "")
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	if _, err := Create("xdg", "", "local", "postgres", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "dbbie", "sessions", "xdg.json")); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	if _, err := Create("home", "", "local", "postgres", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "dbbie", "sessions", "home.json")); err != nil {
		t.Fatal(err)
	}
}
