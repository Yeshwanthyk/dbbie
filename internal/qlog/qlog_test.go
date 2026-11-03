package qlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/query"
)

func testLog(t *testing.T) *Log {
	t.Helper()
	t.Setenv("DBBIE_STATE_DIR", t.TempDir())
	l, err := Open("local")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestAppendListGet(t *testing.T) {
	l := testLog(t)
	for i := 1; i <= 4; i++ {
		e, err := l.Append(Entry{V: 99, ID: "ignored", Conn: "ignored", Source: "cli", Actor: "agent", SQL: "select 1", Columns: []query.Column{{Name: "x", Database: "INT"}}, Rows: [][]any{{i}}, RowCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		if e.ID != fmt.Sprintf("q_%04d", i) || e.V != 1 || e.Conn != "local" || e.TS.IsZero() || e.TS.Location() != time.UTC {
			t.Fatalf("stored entry = %+v", e)
		}
	}
	for _, tc := range []struct {
		name  string
		limit int
		ids   []string
	}{
		{"all", 0, []string{"q_0001", "q_0002", "q_0003", "q_0004"}},
		{"negative", -1, []string{"q_0001", "q_0002", "q_0003", "q_0004"}},
		{"recent", 2, []string{"q_0003", "q_0004"}},
		{"ring wraps", 3, []string{"q_0002", "q_0003", "q_0004"}},
		{"last", 1, []string{"q_0004"}},
		{"oversized", 10, []string{"q_0001", "q_0002", "q_0003", "q_0004"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := l.List(tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, e := range entries {
				ids = append(ids, e.ID)
			}
			if !reflect.DeepEqual(ids, tc.ids) {
				t.Fatalf("ids = %v, want %v", ids, tc.ids)
			}
		})
	}
	for _, tc := range []struct {
		id      string
		missing bool
	}{{"q_0002", false}, {"q_9999", true}, {"", true}} {
		t.Run("get "+tc.id, func(t *testing.T) {
			e, err := l.Get(tc.id)
			if tc.missing {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("Get error = %v", err)
				}
				return
			}
			if err != nil || e.ID != tc.id || e.Rows[0][0] != float64(2) || e.Columns[0].Database != "INT" {
				t.Fatalf("Get = %+v, %v", e, err)
			}
		})
	}
}

func TestTornFinalLine(t *testing.T) {
	for _, tail := range []string{`{"v":1,"id":"q_9999"`, `{"v":1,"id":"q_9999"}`, "partial"} {
		t.Run(tail, func(t *testing.T) {
			l := testLog(t)
			if _, err := l.Append(Entry{SQL: "select 1"}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(l.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(l.path, append(before, tail...), 0o600); err != nil {
				t.Fatal(err)
			}
			entries, err := l.List(0)
			if err != nil || len(entries) != 1 {
				t.Fatalf("List torn = %v, %v", entries, err)
			}
			if _, err := l.Get("q_0001"); err != nil {
				t.Fatal(err)
			}
			if _, err := l.Get("q_9999"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("torn Get = %v", err)
			}
			e, err := l.Append(Entry{SQL: "select 2"})
			if err != nil || e.ID != "q_0002" {
				t.Fatalf("Append after torn = %+v, %v", e, err)
			}
			after, err := os.ReadFile(l.path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(after), string(before)) {
				t.Fatal("completed lines changed")
			}
			entries, err = l.List(0)
			if err != nil || len(entries) != 2 {
				t.Fatalf("List recovered = %v, %v", entries, err)
			}
		})
	}
}

func TestFollow(t *testing.T) {
	for _, fromStart := range []bool{true, false} {
		t.Run(fmt.Sprint(fromStart), func(t *testing.T) {
			l := testLog(t)
			first, err := l.Append(Entry{SQL: "select 1"})
			if err != nil {
				t.Fatal(err)
			}
			size, err := l.Size()
			if err != nil || size <= 0 {
				t.Fatalf("Size = %d, %v", size, err)
			}
			if fromStart {
				size = 0
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			seen := make(chan Entry, 4)
			done := make(chan error, 1)
			go func() {
				done <- l.Follow(ctx, size, func(e Entry) {
					// Re-entry must not deadlock on a held read lock.
					_, _ = l.Append(Entry{SQL: "select 3"})
					seen <- e
					cancel()
				})
			}()
			second, err := l.Append(Entry{SQL: "select 2"})
			if err != nil {
				t.Fatal(err)
			}
			want := second.ID
			if fromStart {
				want = first.ID
			}
			select {
			case e := <-seen:
				if e.ID != want {
					t.Fatalf("Follow id = %s, want %s", e.ID, want)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("Follow did not deliver")
			}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("Follow canceled = %v", err)
			}
		})
	}
}

func TestFollowWaitsForNewline(t *testing.T) {
	l := testLog(t)
	e := Entry{V: 1, ID: "q_0001", SQL: "select 1"}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	seen := make(chan Entry, 1)
	done := make(chan error, 1)
	go func() { done <- l.Follow(ctx, 0, func(e Entry) { seen <- e; cancel() }) }()
	select {
	case <-seen:
		t.Fatal("delivered an incomplete line")
	case <-time.After(300 * time.Millisecond):
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	select {
	case got := <-seen:
		if got.ID != e.ID {
			t.Fatalf("Follow = %+v", got)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("newline was not delivered")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestFollowFromTornSizeAndBatches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		torn  bool
		count int
	}{
		{"size at torn suffix", true, 1},
		{"multiple bounded batches", false, 130},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := testLog(t)
			var offset int64
			if tc.torn {
				if _, err := l.Append(Entry{}); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.WriteString(`{"v":1,"id":"q_0002"`); err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
				offset, err = l.Size()
				if err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < tc.count; i++ {
				if _, err := l.Append(Entry{}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var ids []string
			err := l.Follow(ctx, offset, func(e Entry) {
				ids = append(ids, e.ID)
				if len(ids) == tc.count {
					cancel()
				}
			})
			if !errors.Is(err, context.Canceled) || len(ids) != tc.count {
				t.Fatalf("Follow = %v, ids = %v", err, ids)
			}
			start := 1
			if tc.torn {
				start = 2
			}
			for i, id := range ids {
				if want := fmt.Sprintf("q_%04d", start+i); id != want {
					t.Fatalf("id = %s, want %s", id, want)
				}
			}
		})
	}
}

func TestConcurrentAppend(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprint(separate), func(t *testing.T) {
			l := testLog(t)
			const writers, perWriter = 8, 10
			var wg sync.WaitGroup
			errs := make(chan error, writers)
			for i := 0; i < writers; i++ {
				wg.Go(func() {
					writer := l
					if separate {
						var err error
						writer, err = Open("local")
						if err != nil {
							errs <- err
							return
						}
					}
					for j := 0; j < perWriter; j++ {
						if _, err := writer.Append(Entry{SQL: "select 1"}); err != nil {
							errs <- err
							return
						}
					}
				})
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
			assertSequential(t, l, writers*perWriter)
		})
	}
}

func TestAppendProcessHelper(t *testing.T) {
	if os.Getenv("DBBIE_QLOG_CHILD") != "1" {
		return
	}
	l, err := Open("local")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err := l.Append(Entry{SQL: "select 1"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentProcesses(t *testing.T) {
	l := testLog(t)
	var commands []*exec.Cmd
	for i := 0; i < 4; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAppendProcessHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "DBBIE_QLOG_CHILD=1")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	// The parent writes to the same file while all four children are active.
	for i := 0; i < 12; i++ {
		if _, err := l.Append(Entry{SQL: "select 2"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	assertSequential(t, l, 60)
}

func assertSequential(t *testing.T, l *Log, count int) {
	t.Helper()
	entries, err := l.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != count {
		t.Fatalf("entries = %d, want %d", len(entries), count)
	}
	for i, e := range entries {
		if want := fmt.Sprintf("q_%04d", i+1); e.ID != want {
			t.Fatalf("entry %d = %s, want %s", i, e.ID, want)
		}
	}
}

func TestLocationAndPermissions(t *testing.T) {
	for _, source := range []string{"override", "xdg", "home"} {
		t.Run(source, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("HOME", base)
			t.Setenv("XDG_STATE_HOME", "")
			t.Setenv("DBBIE_STATE_DIR", "")
			dir := filepath.Join(base, ".local", "state", "dbbie")
			if source == "xdg" {
				t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
				dir = filepath.Join(base, "state", "dbbie")
			} else if source == "override" {
				dir = filepath.Join(base, "override")
				t.Setenv("DBBIE_STATE_DIR", dir)
				t.Setenv("XDG_STATE_HOME", filepath.Join(base, "ignored"))
			}
			// Existing permissive modes must be tightened too.
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "local.jsonl"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			l, err := Open("local")
			if err != nil {
				t.Fatal(err)
			}
			if l.path != filepath.Join(dir, "local.jsonl") {
				t.Fatalf("path = %s", l.path)
			}
			for p, want := range map[string]os.FileMode{dir: 0o700, l.path: 0o600} {
				info, err := os.Stat(p)
				if err != nil || info.Mode().Perm() != want {
					t.Fatalf("permissions %s = %v, %v; want %o", p, info, err, want)
				}
			}
		})
	}
}

func TestInvalidInputsAndCorruption(t *testing.T) {
	l := testLog(t)
	for _, conn := range []string{"", ".", "..", "../outside", "a/b", "a\\b", "a\n"} {
		if _, err := Open(conn); err == nil {
			t.Fatalf("accepted name %q", conn)
		}
	}
	if _, err := l.Append(Entry{Rows: make([][]any, 1001)}); err == nil {
		t.Fatal("accepted unbounded preview")
	}
	if _, err := l.Append(Entry{Rows: [][]any{{make(chan int)}}}); err == nil {
		t.Fatal("accepted non-JSON row")
	}
	if n, err := l.Size(); err != nil || n != 0 {
		t.Fatalf("failed append changed Size: %d, %v", n, err)
	}
	for _, tc := range []struct {
		offset int64
		fn     func(Entry)
	}{{-1, func(Entry) {}}, {0, nil}} {
		if err := l.Follow(context.Background(), tc.offset, tc.fn); err == nil {
			t.Fatal("accepted invalid Follow")
		}
	}
	if err := os.WriteFile(l.path, []byte("bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.List(0); err == nil {
		t.Fatal("ignored corrupt completed line")
	}
	if _, err := l.Append(Entry{}); err == nil {
		t.Fatal("appended to corrupt completed log")
	}
}
