package catalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/yeshwanthyk/dbbie/internal/db"
)

func TestTablesScansPostgres(t *testing.T) {
	conn, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mock.ExpectQuery(postgresTablesSQL).
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "table_type", "reltuples", "n_live_tup"}).
			AddRow("Mixed Schema", "Quoted.Table", "BASE TABLE", 42.6, 99).
			AddRow("public", "empty", "BASE TABLE", 0, 99).
			AddRow("public", "fallback", "BASE TABLE", -1, 17).
			AddRow("public", "fallback_empty", "BASE TABLE", -1, 0).
			AddRow("public", "fallback_large", "BASE TABLE", -1, int64(9007199254740993)).
			AddRow("public", "missing", "BASE TABLE", nil, nil).
			AddRow("public", "never_analyzed", "BASE TABLE", -1, nil).
			AddRow("public", "negative", "BASE TABLE", -2, 7).
			AddRow("public", "unknown_estimate", "BASE TABLE", nil, 8).
			AddRow("public", "view", "VIEW", 100, 200).
			AddRow("public", "foreign", "FOREIGN TABLE", 100, 200))

	tables, err := NewInspector(db.Postgres, conn).Tables(context.Background())
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	want := []Table{
		{Schema: "Mixed Schema", Name: "Quoted.Table", Type: "BASE TABLE", EstimatedRows: 43},
		{Schema: "public", Name: "empty", Type: "BASE TABLE", EstimatedRows: 0},
		{Schema: "public", Name: "fallback", Type: "BASE TABLE", EstimatedRows: 17},
		{Schema: "public", Name: "fallback_empty", Type: "BASE TABLE", EstimatedRows: 0},
		{Schema: "public", Name: "fallback_large", Type: "BASE TABLE", EstimatedRows: 9007199254740993},
		{Schema: "public", Name: "missing", Type: "BASE TABLE", EstimatedRows: -1},
		{Schema: "public", Name: "never_analyzed", Type: "BASE TABLE", EstimatedRows: -1},
		{Schema: "public", Name: "negative", Type: "BASE TABLE", EstimatedRows: 7},
		{Schema: "public", Name: "unknown_estimate", Type: "BASE TABLE", EstimatedRows: 8},
		{Schema: "public", Name: "view", Type: "VIEW", EstimatedRows: -1},
		{Schema: "public", Name: "foreign", Type: "FOREIGN TABLE", EstimatedRows: -1},
	}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("tables = %+v, want %+v", tables, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTablesScansMySQL(t *testing.T) {
	conn, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mock.ExpectQuery(mysqlTablesSQL).
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "table_type", "table_rows"}).
			AddRow("app", "users", "BASE TABLE", 123).
			AddRow("app", "rounded", "BASE TABLE", 3.5).
			AddRow("app", "empty", "BASE TABLE", 0).
			AddRow("app", "unknown", "BASE TABLE", nil).
			AddRow("app", "view", "VIEW", 123).
			AddRow("app", "null_view", "VIEW", nil))

	tables, err := NewInspector(db.MySQL, conn).Tables(context.Background())
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	want := []Table{
		{Schema: "app", Name: "users", Type: "BASE TABLE", EstimatedRows: 123},
		{Schema: "app", Name: "rounded", Type: "BASE TABLE", EstimatedRows: 4},
		{Schema: "app", Name: "empty", Type: "BASE TABLE", EstimatedRows: 0},
		{Schema: "app", Name: "unknown", Type: "BASE TABLE", EstimatedRows: -1},
		{Schema: "app", Name: "view", Type: "VIEW", EstimatedRows: -1},
		{Schema: "app", Name: "null_view", Type: "VIEW", EstimatedRows: -1},
	}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("tables = %+v, want %+v", tables, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestScanTablesWithoutEstimates(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const query = "select table_schema, table_name, table_type from information_schema.tables"
	mock.ExpectQuery("information_schema\\.tables").
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "table_type"}).
			AddRow("public", "users", "BASE TABLE"))

	tables, err := scanTables(context.Background(), conn, query)
	if err != nil {
		t.Fatalf("scanTables: %v", err)
	}
	want := []Table{{Schema: "public", Name: "users", Type: "BASE TABLE", EstimatedRows: -1}}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("tables = %+v, want %+v", tables, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestColumnsScansMySQL(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mock.ExpectQuery("information_schema\\.columns").
		WithArgs("app", "users").
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "column_name", "ordinal_position", "data_type", "is_nullable", "column_default"}).
			AddRow("app", "users", "id", 1, "bigint", "NO", nil).
			AddRow("app", "users", "email", 2, "varchar", "YES", nil))

	columns, err := NewInspector(db.MySQL, conn).Columns(context.Background(), "app", "users")
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(columns) != 2 || columns[0].Nullable || !columns[1].Nullable {
		t.Fatalf("columns = %+v", columns)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestColumnsUsesCurrentMySQLDatabaseWhenSchemaOmitted(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mock.ExpectQuery("select database\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("app"))
	mock.ExpectQuery("information_schema\\.columns").
		WithArgs("app", "users").
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "column_name", "ordinal_position", "data_type", "is_nullable", "column_default"}).
			AddRow("app", "users", "id", 1, "bigint", "NO", nil))

	columns, err := NewInspector(db.MySQL, conn).Columns(context.Background(), "", "users")
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(columns) != 1 || columns[0].Schema != "app" {
		t.Fatalf("columns = %+v", columns)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
