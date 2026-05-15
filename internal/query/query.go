package query

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const DefaultPreviewLimit = 100
const MaxPreviewLimit = 1000

var leadingReadOnly = regexp.MustCompile(`(?is)^\s*(with|select|show|describe|desc|explain)\b`)
var leadingWrappable = regexp.MustCompile(`(?is)^\s*(with|select)\b`)
var leadingExplainAnalyze = regexp.MustCompile(`(?is)^\s*explain\s+analyze\b`)
var dangerousKeyword = regexp.MustCompile(`(?is)\b(insert|update|delete|merge|drop|alter|truncate|create|grant|revoke|call|execute|do|copy|vacuum|analyze|set|reset|replace|load)\b`)
var sideEffectFunc = regexp.MustCompile(`(?is)\b(nextval|setval|pg_advisory_lock|get_lock|release_lock|sleep|benchmark)\s*\(`)

type Runner struct {
	DB      *sql.DB
	Clock   func() time.Time
	NowNano func() int64
}

type Options struct {
	SQL       string
	Limit     int
	Timeout   time.Duration
	ReadOnly  bool
	RequestID string
}

type Result struct {
	ID        string   `json:"id"`
	Columns   []Column `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"rowCount"`
	Truncated bool     `json:"truncated"`
	Elapsed   string   `json:"elapsed"`
	Executed  string   `json:"-"`
}

type Column struct {
	Name     string `json:"name"`
	Database string `json:"databaseType,omitempty"`
}

func (r Runner) Run(ctx context.Context, opts Options) (Result, error) {
	sqlText := strings.TrimSpace(opts.SQL)
	if sqlText == "" {
		return Result{}, errors.New("sql is required")
	}
	if opts.ReadOnly {
		if err := ValidateReadOnly(sqlText); err != nil {
			return Result{}, err
		}
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultPreviewLimit
	}
	if limit > MaxPreviewLimit {
		limit = MaxPreviewLimit
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	now := r.Clock
	if now == nil {
		now = time.Now
	}
	start := now()
	executableSQL := sqlText
	if opts.ReadOnly {
		executableSQL = BoundReadSQL(sqlText, limit)
	}
	var rows *sql.Rows
	var tx *sql.Tx
	var err error
	if opts.ReadOnly {
		tx, err = r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return Result{}, err
		}
		defer tx.Rollback()
		rows, err = tx.QueryContext(ctx, executableSQL)
	} else {
		rows, err = r.DB.QueryContext(ctx, executableSQL)
	}
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return Result{}, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return Result{}, err
	}
	resultColumns := make([]Column, len(cols))
	for idx, name := range cols {
		resultColumns[idx] = Column{Name: name}
		if idx < len(types) {
			resultColumns[idx].Database = types[idx].DatabaseTypeName()
		}
	}

	out := Result{
		ID:       resultID(opts.RequestID, sqlText, start),
		Columns:  resultColumns,
		Rows:     make([][]any, 0, min(limit, 32)),
		Executed: executableSQL,
	}
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}

	for rows.Next() {
		if out.RowCount >= limit {
			out.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Result{}, err
		}
		row := make([]any, len(cols))
		for i := range cols {
			row[i] = normalizeValue(values[i])
		}
		out.Rows = append(out.Rows, row)
		out.RowCount++
	}
	if err := rows.Err(); err != nil && !errors.Is(err, io.EOF) {
		return Result{}, err
	}
	out.Elapsed = now().Sub(start).String()
	return out, nil
}

func IsReadOnly(sqlText string) bool {
	return ValidateReadOnly(sqlText) == nil
}

func ValidateReadOnly(sqlText string) error {
	cleaned, err := stripTrailingStatementTerminator(sqlText)
	if err != nil {
		return err
	}
	if !leadingReadOnly.MatchString(cleaned) {
		return fmt.Errorf("query rejected: read-only mode allows SELECT/WITH/SHOW/DESCRIBE/EXPLAIN only")
	}
	withoutLiterals := blankStringLiterals(cleaned)
	if leadingExplainAnalyze.MatchString(withoutLiterals) {
		return fmt.Errorf("query rejected: EXPLAIN ANALYZE may execute the query")
	}
	if dangerousKeyword.MatchString(withoutLiterals) {
		return fmt.Errorf("query rejected: read-only mode forbids write/DDL/session-control keywords")
	}
	if sideEffectFunc.MatchString(withoutLiterals) {
		return fmt.Errorf("query rejected: read-only mode forbids side-effecting functions")
	}
	return nil
}

func BoundReadSQL(sqlText string, limit int) string {
	cleaned, err := stripTrailingStatementTerminator(sqlText)
	if err != nil {
		return strings.TrimSpace(sqlText)
	}
	if limit <= 0 {
		limit = DefaultPreviewLimit
	}
	if !leadingWrappable.MatchString(cleaned) {
		return cleaned
	}
	return fmt.Sprintf("select * from (%s) as dbbie_preview limit %d", cleaned, limit+1)
}

func normalizeValue(v any) any {
	switch typed := v.(type) {
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return typed
	}
}

func resultID(requestID, sqlText string, ts time.Time) string {
	seed := fmt.Sprintf("%s|%d|%s", requestID, ts.UnixNano(), sqlText)
	sum := sha256.Sum256([]byte(seed))
	return "qry_" + hex.EncodeToString(sum[:])[:16]
}

func stripTrailingStatementTerminator(sqlText string) (string, error) {
	trimmed := strings.TrimSpace(sqlText)
	if trimmed == "" {
		return "", nil
	}
	inSingle := false
	inDouble := false
	for idx, r := range trimmed {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ';':
			if !inSingle && !inDouble && strings.TrimSpace(trimmed[idx+1:]) != "" {
				return "", fmt.Errorf("query rejected: multiple statements are not allowed in read-only mode")
			}
		}
	}
	return strings.TrimSuffix(trimmed, ";"), nil
}

func blankStringLiterals(sqlText string) string {
	var out strings.Builder
	out.Grow(len(sqlText))
	inSingle := false
	inDouble := false
	for _, r := range sqlText {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			out.WriteRune(' ')
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			out.WriteRune(' ')
		default:
			if inSingle || inDouble {
				out.WriteRune(' ')
			} else {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}
