package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

func TestGridFormatting(t *testing.T) {
	cases := []struct {
		name, typ string
		value     any
		width     int
		want      string
	}{
		{"null", "TEXT", nil, 8, "∅"},
		{"empty", "TEXT", "", 8, ""},
		{"timestamp", "TIMESTAMPTZ", "2026-10-05T16:48:31.123456+02:00", 40, "2026-10-05 16:48:31"},
		{"mysql timestamp", "DATETIME", "2026-10-05 16:48:31.123456", 40, "2026-10-05 16:48:31"},
		{"time value", "TIMESTAMP", time.Date(2026, 10, 5, 16, 48, 31, 123, time.UTC), 40, "2026-10-05 16:48:31"},
		{"uuid shortened", "UUID", "472c1ac4-f057-4e11-8512-121234567890", 12, "472c1ac4…"},
		{"uuid fits", "UUID", "472c1ac4-f057-4e11-8512-121234567890", 36, "472c1ac4-f057-4e11-8512-121234567890"},
		{"bool true", "BOOL", true, 8, "true"},
		{"bool false", "BOOLEAN", "f", 8, "false"},
		{"json", "JSONB", "{\n  \"a\": 1, \"b\": [true, null]\n}", 40, `{"a":1,"b":[true,null]}`},
		{"bytes", "BYTEA", []byte{0xff, 0, 1}, 40, "0xff0001"},
		{"bytes text", "TEXT", []byte("hello\nthere"), 40, `hello\nthere`},
		{"truncated", "TEXT", "abcdefgh", 4, "abc…"},
		{"wide unicode", "TEXT", "帳戶名字", 4, "帳…"},
		{"grapheme", "TEXT", "e\u0301abcdef", 4, "e\u0301ab…"},
		{"controls", "TEXT", "x\x1b[31m\ty", 40, `x\u001b[31m\ty`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gridValue(tc.value, query.Column{Database: tc.typ}, tc.width); got != tc.want {
				t.Fatalf("grid value=%q want %q", got, tc.want)
			}
		})
	}
}

func TestSampledWidthsAndCellStyles(t *testing.T) {
	s := newTestState(t, nil)
	s.renderResult(query.Result{
		Columns: []query.Column{{Name: "n", Database: "NUMERIC"}, {Name: "text", Database: "TEXT"}, {Name: "bool", Database: "BOOL"}},
		Rows:    [][]any{{"12.50", nil, true}, {"2", "", false}, {"100.99", strings.Repeat("a", 90), true}},
	})
	if got := columnWidths(s.current().result); !reflect.DeepEqual(got, []int{6, 40, 5}) {
		t.Fatalf("widths=%v", got)
	}
	if s.grid.GetCell(1, 0).Align != tview.AlignRight || s.grid.GetCell(3, 1).Text != strings.Repeat("a", 39)+"…" {
		t.Fatal("alignment or ellipsis missing")
	}
	_, _, attr := s.grid.GetCell(0, 0).Style.Decompose()
	if attr&tcell.AttrBold == 0 || attr&tcell.AttrUnderline == 0 {
		t.Fatal("header missing bold/separator")
	}
	fg, bg, attr := s.grid.GetCell(1, 1).Style.Decompose()
	if fg != s.theme.TCell(s.theme.Muted) || attr != tcell.AttrNone || bg != s.theme.TCell(s.theme.Selection) {
		t.Fatal("NULL or row selection style missing")
	}
	fg, _, _ = s.grid.GetCell(2, 2).Style.Decompose()
	if fg != s.theme.TCell(s.theme.Danger) {
		t.Fatal("false not colored")
	}
	fg, _, _ = s.grid.GetCell(1, 2).Style.Decompose()
	if fg != s.theme.TCell(s.theme.Success) {
		t.Fatal("true not colored")
	}
	_, selectedBG, _ := s.grid.GetCell(1, 0).SelectedStyle.Decompose()
	if selectedBG != s.theme.TCell(s.theme.Primary) {
		t.Fatal("cursor cell must differ from selected row")
	}
	typeKeys(s, "j")
	_, oldBG, _ := s.grid.GetCell(1, 1).Style.Decompose()
	_, newBG, _ := s.grid.GetCell(2, 1).Style.Decompose()
	if oldBG == newBG || newBG != s.theme.TCell(s.theme.Selection) {
		t.Fatal("row band did not follow cursor")
	}

	rows := make([][]any, 101)
	for i := range rows {
		rows[i] = []any{"x"}
	}
	rows[100][0] = strings.Repeat("z", 200)
	if got := columnWidths(query.Result{Columns: []query.Column{{Name: "a"}}, Rows: rows}); got[0] != 4 {
		t.Fatal("sizing should sample only the first 100 rows with minimum 4")
	}
}

func TestClientSortStableNullsLastAndNoRequery(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := newTestState(t, conn)
	rows := sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn("n").OfType("NUMERIC", ""), sqlmock.NewColumn("label").OfType("TEXT", "")).
		AddRow("10", "ten").AddRow(nil, "null one").AddRow("2", "two a").AddRow("2", "two b").AddRow(nil, "null two").AddRow("-1.25", "negative")
	expectQuery(mock, "select n, label from numbers", rows)
	s.runQuery("select n, label from numbers")
	original := append([][]any(nil), s.current().result.Rows...)
	typeKeys(s, "s")
	if got := labels(s.viewRows); !reflect.DeepEqual(got, []string{"negative", "two a", "two b", "ten", "null one", "null two"}) {
		t.Fatalf("ascending=%v", got)
	}
	if !strings.Contains(s.grid.GetCell(0, 0).Text, "▲") {
		t.Fatal("ascending marker missing")
	}
	typeKeys(s, "S")
	if got := labels(s.viewRows); !reflect.DeepEqual(got, []string{"ten", "two a", "two b", "negative", "null one", "null two"}) {
		t.Fatalf("descending=%v", got)
	}
	if !strings.Contains(s.grid.GetCell(0, 0).Text, "▼") || !reflect.DeepEqual(original, s.current().result.Rows) {
		t.Fatal("sort mutated source or descending marker missing")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func labels(rows [][]any) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row[1].(string)
	}
	return out
}

func TestSortTypedValues(t *testing.T) {
	for _, tc := range []struct {
		typ        string
		rows, want [][]any
	}{
		{"BOOL", [][]any{{true}, {nil}, {false}}, [][]any{{false}, {true}, {nil}}},
		{"TIMESTAMPTZ", [][]any{{"2026-10-05T14:00:00Z"}, {"2026-10-05T16:00:00+03:00"}, {nil}}, [][]any{{"2026-10-05T16:00:00+03:00"}, {"2026-10-05T14:00:00Z"}, {nil}}},
		{"INT8", [][]any{{int64(9007199254740993)}, {int64(9007199254740992)}}, [][]any{{int64(9007199254740992)}, {int64(9007199254740993)}}},
		{"TEXT", [][]any{{"z"}, {""}, {nil}, {"a"}}, [][]any{{""}, {"a"}, {"z"}, {nil}}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			s := newTestState(t, nil)
			s.renderResult(query.Result{Columns: []query.Column{{Name: "x", Database: tc.typ}}, Rows: tc.rows})
			typeKeys(s, "s")
			if !reflect.DeepEqual(s.viewRows, tc.want) {
				t.Fatalf("sorted=%v want %v", s.viewRows, tc.want)
			}
		})
	}
}

func TestEqualFilterComposesWithSearchAndSort(t *testing.T) {
	s := newTestState(t, nil)
	s.renderResult(query.Result{Columns: []query.Column{{Name: "kind"}, {Name: "label"}}, Rows: [][]any{{"a", "zeta"}, {"b", "beta"}, {"a", "alpha"}, {nil, "null"}, {"", "empty"}}})
	typeKeys(s, "f")
	if len(s.viewRows) != 2 || s.current().equalFilter == nil {
		t.Fatal("f did not filter equal cursor values")
	}
	typeKeys(s, "ls/alpha")
	enter(s)
	if len(s.viewRows) != 1 || s.viewRows[0][1] != "alpha" {
		t.Fatal("search did not combine with equality")
	}
	typeKeys(s, "nNf")
	if s.current().equalFilter != nil || len(s.viewRows) != 1 || s.current().rowFilter != "alpha" {
		t.Fatal("f toggle should retain row search")
	}
	escape(s)
	if len(s.viewRows) != 5 || s.current().rowFilter != "" || !s.current().sorted {
		t.Fatal("Esc must clear filters but preserve sort")
	}
	// Filtering NULL is not filtering the empty string.
	s.grid.Select(4, 0) // sorted label order: alpha, beta, empty, null, zeta
	typeKeys(s, "f")
	if len(s.viewRows) != 1 || s.viewRows[0][0] != nil {
		t.Fatal("NULL equality lost")
	}
	typeKeys(s, "f")
	s.grid.Select(3, 0)
	typeKeys(s, "f")
	if len(s.viewRows) != 1 || s.viewRows[0][0] != "" {
		t.Fatal("empty string equality lost")
	}
}

func TestRecordViewKeysAndFullInspector(t *testing.T) {
	s := newTestState(t, nil)
	full := "2026-10-05T16:48:31.123456+02:00"
	s.renderResult(query.Result{Columns: []query.Column{{Name: "created", Database: "TIMESTAMPTZ"}, {Name: "payload", Database: "TEXT"}}, Rows: [][]any{{full, strings.Repeat("x", 100)}, {nil, "second"}}})
	if !strings.Contains(s.cellInfo.GetText(true), "created · TIMESTAMPTZ · "+full) {
		t.Fatalf("inspector=%s", s.cellInfo.GetText(false))
	}
	enter(s)
	if !s.current().record || s.app.GetFocus() != s.recordView || !strings.Contains(s.status.GetText(false), "RECORD") || !strings.Contains(s.recordView.GetText(false), full) || !strings.Contains(s.recordView.GetText(false), strings.Repeat("x", 100)) {
		t.Fatal("record must show full typed values and mode")
	}
	typeKeys(s, "j")
	if !strings.Contains(s.recordView.GetText(false), "second") || !strings.Contains(s.recordView.GetText(false), "NULL") {
		t.Fatal("record j did not move row")
	}
	typeKeys(s, "k")
	if !strings.Contains(s.recordView.GetText(false), full) {
		t.Fatal("record k did not move row")
	}
	escape(s)
	row, col := s.grid.GetSelection()
	if s.current().record || s.app.GetFocus() != s.grid || row != 1 || col != 0 || !strings.Contains(s.status.GetText(false), "NORMAL") {
		t.Fatal("Esc did not restore grid cursor/mode")
	}
}

func TestYankOSC52FullCellAndVisibleRowJSON(t *testing.T) {
	s := newTestState(t, nil)
	var writer bytes.Buffer
	s.clipboardWriter = &writer
	long := strings.Repeat("世界", 30) + "\n[red]"
	s.renderResult(query.Result{Columns: []query.Column{{Name: "n", Database: "INT"}, {Name: "text"}, {Name: "nullable"}}, Rows: [][]any{{2, "other", nil}, {1, long, nil}}})
	typeKeys(s, "sl")
	typeKeys(s, "y")
	if writer.Len() != 0 {
		t.Fatal("a single y must not copy")
	}
	typeKeys(s, "y")
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(long)) + "\a"
	if writer.String() != want || !strings.Contains(s.status.GetText(false), "copied cell") {
		t.Fatalf("OSC52 cell=%q", writer.String())
	}
	writer.Reset()
	enter(s)
	typeKeys(s, "Y")
	raw := strings.TrimSuffix(strings.TrimPrefix(writer.String(), "\x1b]52;c;"), "\a")
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(decoded, &object); err != nil {
		t.Fatal(err)
	}
	if object["n"] != float64(1) || object["text"] != long || object["nullable"] != nil {
		t.Fatalf("copied wrong sorted row=%s", decoded)
	}
	s.copiedUntil = time.Now().Add(-time.Second)
	press(s, tcell.KeyF24, 0)
	if strings.Contains(s.status.GetText(false), "copied") {
		t.Fatal("copy feedback did not expire")
	}
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestOSC52ShortWriteAndCopyFailure(t *testing.T) {
	if err := writeOSC52(shortWriter{}, []byte("a")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write=%v", err)
	}
	s := newTestState(t, nil)
	s.clipboardWriter = shortWriter{}
	s.renderResult(query.Result{Columns: []query.Column{{Name: "a"}}, Rows: [][]any{{"a"}}})
	typeKeys(s, "yy")
	if !strings.Contains(s.status.GetText(false), "copy failed") {
		t.Fatal("failed write reported copied")
	}
}

func TestEmptyAndErrorNoticesOnScreen(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()
	s := newTestState(t, conn)
	expectQuery(mock, "select id from empty_table", sqlmock.NewRows([]string{"id"}))
	s.runQuery("select id from empty_table")
	screen, text := drawShell(t, s)
	x, y, w, h := s.grid.GetInnerRect()
	r, _, style, _ := screen.GetContent(x+(w-6)/2, y+1+(h-1)/2)
	fg, _, _ := style.Decompose()
	if r != '0' || fg != s.theme.TCell(s.theme.Muted) || !strings.Contains(text, "id") || !strings.Contains(s.resultPane.GetTitle(), "0 rows") {
		t.Fatal("empty notice must be centered/muted with header intact")
	}
	enter(s)
	typeKeys(s, "yyYsSf")
	if s.current().record {
		t.Fatal("empty result opened record view")
	}
	mock.ExpectBegin()
	mock.ExpectQuery("select \\* from").WillReturnError(errors.New("database proof error"))
	mock.ExpectRollback()
	s.runQuery("select missing from missing")
	screen, text = drawShell(t, s)
	x, y, w, h = s.grid.GetInnerRect()
	r, _, style, _ = screen.GetContent(x+(w-len("database proof error"))/2, y+h/2)
	fg, _, _ = style.Decompose()
	if r != 'd' || fg != s.theme.TCell(s.theme.Danger) || !strings.Contains(text, "database proof error") || !strings.Contains(s.resultPane.GetTitle(), "error") {
		t.Fatalf("error must be red in results pane:\n%s", text)
	}
	s.newTab("other", "", nil)
	typeKeys(s, "1")
	_, text = drawShell(t, s)
	if !strings.Contains(text, "database proof error") {
		t.Fatal("error lost after tab switch")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionLeavesRecordOpenedWhileQueryRan(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "result", true: "error"}[fail], func(t *testing.T) {
			s := newTestState(t, nil)
			s.renderResult(query.Result{Columns: []query.Column{{Name: "old"}}, Rows: [][]any{{"old value"}}})
			var finish func(query.Result, error)
			s.runner = func(_ context.Context, _ queryWork, _ func(), done func(query.Result, error)) { finish = done }
			s.runQuery("select 1")
			enter(s) // inspect the previous rows while the query is running
			if !s.current().record || s.current().cancel == nil {
				t.Fatal("record/running precondition missing")
			}
			var err error
			if fail {
				err = errors.New("new error")
			}
			finish(query.Result{Columns: []query.Column{{Name: "new"}}, Rows: [][]any{{"new value"}}}, err)
			if s.current().record || s.app.GetFocus() != s.grid || s.current().cancel != nil {
				t.Fatal("completion left focus on a hidden record view")
			}
			_, text := drawShell(t, s)
			want := "new value"
			if fail {
				want = "new error"
			}
			if !strings.Contains(text, want) || strings.Contains(text, "old value") {
				t.Fatal("completion did not display new result/error")
			}
		})
	}
}

func TestTabsRetainSortEqualFilterAndRecord(t *testing.T) {
	s := newTestState(t, nil)
	s.renderResult(query.Result{Columns: []query.Column{{Name: "kind"}, {Name: "n", Database: "INT"}}, Rows: [][]any{{"a", 2}, {"b", 3}, {"a", 1}}})
	typeKeys(s, "fls")
	enter(s)
	typeKeys(s, "j")
	original := s.current()
	s.newTab("second", "", nil)
	typeKeys(s, "1")
	row, col := s.grid.GetSelection()
	if s.current() != original || !original.record || original.equalFilter == nil || !original.sorted || len(s.viewRows) != 2 || row != 2 || col != 1 || s.app.GetFocus() != s.recordView {
		t.Fatal("tab view state lost")
	}
	escape(s)
	if !strings.Contains(s.cellInfo.GetText(true), "n · INT · 2") {
		t.Fatal("visible-row inspector mapping lost")
	}
}
