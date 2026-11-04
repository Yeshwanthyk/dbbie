package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/yeshwanthyk/dbbie/internal/db"
)

type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	// EstimatedRows is a planner estimate, not an exact count. -1 means unknown.
	EstimatedRows int64 `json:"estimatedRows"`
}

type Column struct {
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Name     string `json:"name"`
	Ordinal  int    `json:"ordinal"`
	DataType string `json:"dataType"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
}

type Inspector struct {
	kind db.Kind
	conn *sql.DB
}

func NewInspector(kind db.Kind, conn *sql.DB) Inspector {
	return Inspector{kind: kind, conn: conn}
}

func (i Inspector) Tables(ctx context.Context) ([]Table, error) {
	switch i.kind {
	case db.Postgres:
		return scanTables(ctx, i.conn, postgresTablesSQL)
	case db.MySQL:
		return scanTables(ctx, i.conn, mysqlTablesSQL)
	default:
		return nil, fmt.Errorf("unsupported catalog driver: %s", i.kind)
	}
}

func (i Inspector) Columns(ctx context.Context, schema, table string) ([]Column, error) {
	switch i.kind {
	case db.Postgres:
		return scanColumns(ctx, i.conn, postgresColumnsSQL, schema, table)
	case db.MySQL:
		if schema == "" {
			var current sql.NullString
			if err := i.conn.QueryRowContext(ctx, "select database()").Scan(&current); err != nil {
				return nil, err
			}
			if current.Valid {
				schema = current.String
			}
		}
		return scanColumns(ctx, i.conn, mysqlColumnsSQL, schema, table)
	default:
		return nil, fmt.Errorf("unsupported catalog driver: %s", i.kind)
	}
}

func scanTables(ctx context.Context, conn *sql.DB, query string) ([]Table, error) {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Legacy queries return only table identity. MySQL adds an estimate, and
	// Postgres also adds the live-row fallback for never-analyzed tables.
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var tables []Table
	for rows.Next() {
		t := Table{EstimatedRows: -1}
		var estimate sql.NullFloat64
		var liveRows sql.NullInt64
		dest := []any{&t.Schema, &t.Name, &t.Type}
		if len(columns) >= 4 {
			dest = append(dest, &estimate)
		}
		if len(columns) >= 5 {
			dest = append(dest, &liveRows)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		if t.Type == "BASE TABLE" {
			switch {
			case estimate.Valid && estimate.Float64 >= 0:
				t.EstimatedRows = int64(math.Round(estimate.Float64))
			case liveRows.Valid && liveRows.Int64 >= 0:
				t.EstimatedRows = liveRows.Int64
			}
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func scanColumns(ctx context.Context, conn *sql.DB, query, schema, table string) ([]Column, error) {
	rows, err := conn.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []Column
	for rows.Next() {
		var c Column
		var nullable string
		var def sql.NullString
		if err := rows.Scan(&c.Schema, &c.Table, &c.Name, &c.Ordinal, &c.DataType, &nullable, &def); err != nil {
			return nil, err
		}
		c.Nullable = nullable == "YES"
		if def.Valid {
			c.Default = def.String
		}
		columns = append(columns, c)
	}
	return columns, rows.Err()
}

const postgresTablesSQL = `
select t.table_schema, t.table_name, t.table_type, c.reltuples, s.n_live_tup
from information_schema.tables t
left join pg_catalog.pg_namespace n on n.nspname = t.table_schema
left join pg_catalog.pg_class c on c.relnamespace = n.oid and c.relname = t.table_name
left join pg_catalog.pg_stat_user_tables s on s.relid = c.oid
where t.table_schema not in ('pg_catalog', 'information_schema')
order by t.table_schema, t.table_name`

const mysqlTablesSQL = `
select table_schema, table_name, table_type, table_rows
from information_schema.tables
where table_schema not in ('mysql', 'information_schema', 'performance_schema', 'sys')
order by table_schema, table_name`

const postgresColumnsSQL = `
select table_schema, table_name, column_name, ordinal_position, data_type, is_nullable, column_default
from information_schema.columns
where table_schema = $1 and table_name = $2
order by ordinal_position`

const mysqlColumnsSQL = `
select table_schema, table_name, column_name, ordinal_position, data_type, is_nullable, column_default
from information_schema.columns
where table_schema = ? and table_name = ?
order by ordinal_position`
