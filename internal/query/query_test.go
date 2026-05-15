package query

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestIsReadOnly(t *testing.T) {
	allowed := []string{
		"select * from users",
		"WITH recent as (select 1) select * from recent",
		" show tables",
		"describe users",
		"explain select * from users",
	}
	for _, sqlText := range allowed {
		if !IsReadOnly(sqlText) {
			t.Fatalf("expected read-only: %s", sqlText)
		}
	}
	rejected := []string{
		"update users set admin = true",
		"delete from users",
		"insert into users values (1)",
		"drop table users",
		"select 1; drop table users",
		"with deleted as (delete from users returning *) select * from deleted",
		"explain analyze select * from users",
		"select nextval('users_id_seq')",
		"select pg_advisory_lock(42)",
	}
	for _, sqlText := range rejected {
		if IsReadOnly(sqlText) {
			t.Fatalf("expected write rejection: %s", sqlText)
		}
	}
}

func TestRunnerBoundsRowsAndMarksTruncated(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "a").
		AddRow(2, "b").
		AddRow(3, "c")
	mock.ExpectBegin()
	mock.ExpectQuery("select \\* from \\(select \\* from users\\) as dbbie_preview limit 3").WillReturnRows(rows)
	mock.ExpectRollback()

	now := time.Unix(100, 0)
	result, err := Runner{
		DB: conn,
		Clock: func() time.Time {
			return now
		},
	}.Run(context.Background(), Options{
		SQL:      "select * from users",
		Limit:    2,
		ReadOnly: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.RowCount != 2 {
		t.Fatalf("RowCount = %d, want 2", result.RowCount)
	}
	if !result.Truncated {
		t.Fatal("expected truncated result")
	}
	if len(result.Rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(result.Rows))
	}
	if result.Rows[1][1] != "b" {
		t.Fatalf("second row = %+v", result.Rows[1])
	}
	if result.Executed != "select * from (select * from users) as dbbie_preview limit 3" {
		t.Fatalf("Executed = %q", result.Executed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerRejectsWriteInReadOnlyMode(t *testing.T) {
	conn, _, cleanup := mockDB(t)
	defer cleanup()
	_, err := Runner{DB: conn}.Run(context.Background(), Options{
		SQL:      "delete from users",
		ReadOnly: true,
	})
	if err == nil {
		t.Fatal("expected read-only rejection")
	}
}

func TestBoundReadSQLOnlyWrapsSelectLikeQueries(t *testing.T) {
	if got := BoundReadSQL("select * from users;", 25); got != "select * from (select * from users) as dbbie_preview limit 26" {
		t.Fatalf("BoundReadSQL select = %q", got)
	}
	if got := BoundReadSQL("show tables", 25); got != "show tables" {
		t.Fatalf("BoundReadSQL show = %q", got)
	}
}

func TestRunnerCapsPreviewLimit(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"id"}).AddRow(1)
	mock.ExpectBegin()
	mock.ExpectQuery("select \\* from \\(select \\* from users\\) as dbbie_preview limit 1001").WillReturnRows(rows)
	mock.ExpectRollback()

	result, err := Runner{DB: conn}.Run(context.Background(), Options{
		SQL:      "select * from users",
		Limit:    1_000_000,
		ReadOnly: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Executed != "select * from (select * from users) as dbbie_preview limit 1001" {
		t.Fatalf("Executed = %q", result.Executed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerPreservesDuplicateColumnValues(t *testing.T) {
	conn, mock, cleanup := mockDB(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"id", "id"}).
		AddRow(1, 2)
	mock.ExpectBegin()
	mock.ExpectQuery("select \\* from \\(select users.id, orders.id from users join orders on true\\) as dbbie_preview limit 11").WillReturnRows(rows)
	mock.ExpectRollback()

	result, err := Runner{DB: conn}.Run(context.Background(), Options{
		SQL:      "select users.id, orders.id from users join orders on true",
		Limit:    10,
		ReadOnly: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Columns) != 2 || len(result.Rows[0]) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if result.Rows[0][0] != int64(1) && result.Rows[0][0] != 1 {
		t.Fatalf("first id = %#v", result.Rows[0][0])
	}
	if result.Rows[0][1] != int64(2) && result.Rows[0][1] != 2 {
		t.Fatalf("second id = %#v", result.Rows[0][1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func mockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock, func()) {
	t.Helper()
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return conn, mock, func() { _ = conn.Close() }
}
