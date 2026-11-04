package qlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/query"
)

const (
	benchEntries = 2000
	benchRows    = 100
)

// benchLog writes a log shaped like a real one: benchEntries lines, each with a
// benchRows-row preview of mixed column types (~50 KB per line, ~100 MB total).
func benchLog(b *testing.B) *Log {
	b.Helper()
	b.Setenv("DBBIE_STATE_DIR", b.TempDir())
	l, err := Open("bench")
	if err != nil {
		b.Fatal(err)
	}
	cols := []query.Column{{Name: "id", Database: "INT4"}, {Name: "created_at", Database: "TIMESTAMPTZ"}, {Name: "kind", Database: "TEXT"}, {Name: "user_id", Database: "INT4"}, {Name: "amount", Database: "NUMERIC"}, {Name: "payload", Database: "JSONB"}}
	rows := make([][]any, benchRows)
	for r := range rows {
		rows[r] = []any{r, "2026-11-04T12:26:18.966237Z", "purchase", r * 7, "123.45", map[string]any{"sku": fmt.Sprintf("sku-%06d", r), "qty": r % 9, "note": "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua ut enim ad minim veniam quis nostrud exercitation ullamco"}}
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		b.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	ts := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= benchEntries; i++ {
		e := Entry{V: 1, ID: fmt.Sprintf("q_%04d", i), TS: ts, Conn: "bench", Driver: "postgres", Source: "cli", Actor: "agent", Why: "bench", SQL: "select * from events order by id", Columns: cols, Rows: rows, RowCount: benchRows, Truncated: true, ElapsedMs: 12}
		data, err := json.Marshal(e)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = w.Write(append(data, '\n'))
	}
	if err := w.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
	return l
}

func BenchmarkAppend(b *testing.B) {
	l := benchLog(b)
	e := Entry{Source: "cli", Actor: "agent", SQL: "select 1", Columns: []query.Column{{Name: "x", Database: "INT"}}, Rows: [][]any{{1}}, RowCount: 1}
	b.ResetTimer()
	for b.Loop() {
		if _, err := l.Append(e); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkList20(b *testing.B) {
	l := benchLog(b)
	b.ResetTimer()
	for b.Loop() {
		entries, err := l.List(20)
		if err != nil || len(entries) != 20 || entries[19].ID != fmt.Sprintf("q_%04d", benchEntries) {
			b.Fatalf("List = %d entries, %v", len(entries), err)
		}
	}
}

func BenchmarkGetMiddle(b *testing.B) {
	l := benchLog(b)
	b.ResetTimer()
	for b.Loop() {
		e, err := l.Get("q_1000")
		if err != nil || e.ID != "q_1000" || len(e.Rows) != benchRows {
			b.Fatalf("Get = %s, %v", e.ID, err)
		}
	}
}
