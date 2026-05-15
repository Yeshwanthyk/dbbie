package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Kind string

const (
	Postgres Kind = "postgres"
	MySQL    Kind = "mysql"
)

var ErrUnsupportedDriver = errors.New("unsupported driver")

type Opener interface {
	Open(ctx context.Context, dsn string) (*sql.DB, error)
	Kind() Kind
}

type SQLDriver struct {
	kind       Kind
	driverName string
}

func NewSQLDriver(kind Kind) (SQLDriver, error) {
	switch kind {
	case Postgres:
		return SQLDriver{kind: kind, driverName: "pgx"}, nil
	case MySQL:
		return SQLDriver{kind: kind, driverName: "mysql"}, nil
	default:
		return SQLDriver{}, fmt.Errorf("%w: %s", ErrUnsupportedDriver, kind)
	}
}

func ParseKind(raw string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "postgres", "postgresql", "pg":
		return Postgres, nil
	case "mysql", "mariadb":
		return MySQL, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedDriver, raw)
	}
}

func (d SQLDriver) Kind() Kind {
	return d.kind
}

func (d SQLDriver) Open(ctx context.Context, dsn string) (*sql.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("dsn is required")
	}
	conn, err := sql.Open(d.driverName, dsn)
	if err != nil {
		return nil, err
	}
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
