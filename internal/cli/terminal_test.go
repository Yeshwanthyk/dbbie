package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/yeshwanthyk/dbbie/internal/query"
)

type terminalBuffer struct {
	bytes.Buffer
	fd uintptr
}

func (b *terminalBuffer) Fd() uintptr { return b.fd }

func TestTerminalDefaultsAndActor(t *testing.T) {
	pty := testPTY(t)
	for _, tc := range []struct {
		name  string
		flags []string
		actor string
		json  bool
	}{
		{"human terminal", nil, "human", false},
		{"JSON does not imply agent", []string{"--json"}, "human", true},
		{"why implies agent", []string{"--why", "intent"}, "agent", false},
		{"empty why supplied", []string{"--why="}, "agent", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, _ := setupQuery(t)
			expectRead(mock)
			out := &terminalBuffer{fd: pty.Fd()}
			if !isTerminal(out) {
				t.Fatal("PTY does not expose terminal attributes")
			}
			args := append([]string{"query", "--conn", "local", "--sql", "select 1 as x"}, tc.flags...)
			if err := Run(context.Background(), args, out, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if json.Valid(out.Bytes()) != tc.json {
				t.Fatalf("terminal output = %s", out.String())
			}
			if entries := readEntries(t, "local"); len(entries) != 1 || entries[0].Actor != tc.actor {
				t.Fatalf("entries = %+v", entries)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQueryCapsLoggedPreview(t *testing.T) {
	mock, _ := setupQuery(t)
	rows := sqlmock.NewRows([]string{"x"})
	for i := 0; i < 1001; i++ {
		rows.AddRow(i)
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`dbbie_preview limit 1001`).WillReturnRows(rows)
	mock.ExpectRollback()
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"query", "--conn", "local", "--sql", "select 1 as x", "--limit", "99999"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result query.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1000 || result.RowCount != 1000 || !result.Truncated {
		t.Fatalf("result rows=%d truncated=%t", len(result.Rows), result.Truncated)
	}
	entries := readEntries(t, "local")
	if len(entries) != 1 || len(entries[0].Rows) != 1000 || !entries[0].Truncated {
		t.Fatalf("logged rows=%d", len(entries[0].Rows))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
