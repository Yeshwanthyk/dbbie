package ui

// Grid benchmarks on a real result: set DBBIE_BENCH_DSN to a Postgres DSN whose
// database has the perf seed (perf/seed.sql). Without it they skip, so the
// numbers always come from rows the real driver returned.
//
//	DBBIE_BENCH_DSN=postgres://postgres@localhost:55432/perf?sslmode=disable \
//	  go test ./internal/ui -run '^$' -bench Grid -benchmem -count 6

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/db"
	"github.com/yeshwanthyk/dbbie/internal/query"
	"github.com/yeshwanthyk/dbbie/internal/theme"
)

var (
	benchOnce   sync.Once
	benchResult query.Result
	benchErr    error
)

func benchRows(b *testing.B) query.Result {
	b.Helper()
	dsn := os.Getenv("DBBIE_BENCH_DSN")
	if dsn == "" {
		b.Skip("DBBIE_BENCH_DSN not set")
	}
	benchOnce.Do(func() {
		driver, err := db.NewSQLDriver(db.Postgres)
		if err != nil {
			benchErr = err
			return
		}
		conn, err := driver.Open(context.Background(), dsn)
		if err != nil {
			benchErr = err
			return
		}
		defer conn.Close()
		benchResult, benchErr = (query.Runner{DB: conn}).Run(context.Background(), query.Options{
			SQL: "select * from events order by id", Limit: query.MaxPreviewLimit, Timeout: 30 * time.Second, ReadOnly: true,
		})
	})
	if benchErr != nil {
		b.Fatal(benchErr)
	}
	if len(benchResult.Rows) != query.MaxPreviewLimit {
		b.Fatalf("fixture has %d rows, want %d", len(benchResult.Rows), query.MaxPreviewLimit)
	}
	return benchResult
}

// benchState is a laid-out state with a 200x50 simulation screen, the size of
// a large terminal window.
func benchState(b *testing.B) (*state, tview.Primitive, tcell.SimulationScreen) {
	b.Helper()
	resolved, err := theme.Resolve("midnight")
	if err != nil {
		b.Fatal(err)
	}
	s := newState(context.Background(), tview.NewApplication(), Options{Kind: db.Postgres, Theme: resolved, Limit: query.MaxPreviewLimit})
	root := s.layout()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		b.Fatal(err)
	}
	screen.SetSize(200, 50)
	root.SetRect(0, 0, 200, 50)
	s.focusResults()
	return s, root, screen
}

func draw(root tview.Primitive, screen tcell.Screen) {
	screen.Clear()
	root.Draw(screen)
	screen.Show()
}

// Opening a 1000-row result: what the user waits for after the query returns.
func BenchmarkGridRenderResult(b *testing.B) {
	result := benchRows(b)
	s, root, screen := benchState(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.renderResult(result)
		draw(root, screen)
	}
}

// One frame with nothing changed: the floor cost of every redraw.
func BenchmarkGridDraw(b *testing.B) {
	result := benchRows(b)
	s, root, screen := benchState(b)
	s.renderResult(result)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		draw(root, screen)
	}
}

// One j/k step: selection change plus the frame that shows it.
func BenchmarkGridMoveRow(b *testing.B) {
	result := benchRows(b)
	s, root, screen := benchState(b)
	s.renderResult(result)
	draw(root, screen)
	dir := 1
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		row, _ := s.grid.GetSelection()
		if row >= len(result.Rows) || row <= 1 {
			dir = -dir
		}
		s.moveGrid(dir, 0)
		draw(root, screen)
	}
}

// One l/h step across 24 columns.
func BenchmarkGridMoveColumn(b *testing.B) {
	result := benchRows(b)
	s, root, screen := benchState(b)
	s.renderResult(result)
	draw(root, screen)
	dir := 1
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, col := s.grid.GetSelection()
		if col >= len(result.Columns)-1 || col <= 0 {
			dir = -dir
		}
		s.moveGrid(0, dir)
		draw(root, screen)
	}
}

// One keystroke in `/` search: the search input calls searchGrid on every change.
func BenchmarkGridSearchKeystroke(b *testing.B) {
	result := benchRows(b)
	s, root, screen := benchState(b)
	s.renderResult(result)
	terms := []string{"u", "us", "use", "user", "user_", "user_4"}
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.searchGrid(terms[i%len(terms)])
		draw(root, screen)
		i++
	}
}

// Sorting the 1000 rows by a text column (s / S on the grid).
func BenchmarkGridSort(b *testing.B) {
	result := benchRows(b)
	s, root, screen := benchState(b)
	s.renderResult(result)
	s.grid.Select(1, 4) // username
	desc := false
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.sortGrid(desc)
		draw(root, screen)
		desc = !desc
	}
}
