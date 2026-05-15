package catalog

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/yeshwanthyk/dbbie/internal/db"
)

func TestTablesScansPostgres(t *testing.T) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mock.ExpectQuery("information_schema\\.tables").
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "table_type"}).
			AddRow("public", "users", "BASE TABLE").
			AddRow("public", "orders", "BASE TABLE"))

	tables, err := NewInspector(db.Postgres, conn).Tables(context.Background())
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	if len(tables) != 2 || tables[0].Name != "users" || tables[1].Name != "orders" {
		t.Fatalf("tables = %+v", tables)
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
