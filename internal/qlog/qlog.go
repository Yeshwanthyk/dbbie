// Package qlog stores bounded query previews in a per-connection JSON Lines log.
package qlog

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/query"
)

var ErrNotFound = errors.New("query log entry not found")

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Entry struct {
	V         int            `json:"v"`
	ID        string         `json:"id"`
	TS        time.Time      `json:"ts"`
	Conn      string         `json:"conn"`
	Driver    string         `json:"driver"`
	Source    string         `json:"source"`
	Actor     string         `json:"actor"`
	Title     string         `json:"title"`
	Why       string         `json:"why"`
	SQL       string         `json:"sql"`
	Columns   []query.Column `json:"columns"`
	Rows      [][]any        `json:"rows"`
	RowCount  int            `json:"rowCount"`
	Truncated bool           `json:"truncated"`
	ElapsedMs int64          `json:"elapsedMs"`
	Error     *Error         `json:"error"`
}

type Log struct {
	path string
	conn string
}

func Open(conn string) (*Log, error) {
	if conn == "" || conn == "." || conn == ".." || strings.ContainsAny(conn, "/\\\x00\r\n") {
		return nil, errors.New("invalid query log connection name")
	}
	dir := os.Getenv("DBBIE_STATE_DIR")
	if dir == "" {
		if state := os.Getenv("XDG_STATE_HOME"); state != "" {
			dir = filepath.Join(state, "dbbie")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			dir = filepath.Join(home, ".local", "state", "dbbie")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	l := &Log{path: filepath.Join(dir, conn+".jsonl"), conn: conn}
	f, err := l.lock(syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	defer unlock(f)
	if err := f.Chmod(0o600); err != nil {
		return nil, err
	}
	return l, nil
}

// Each operation opens its own descriptor. A shared descriptor would make flock
// ineffective between goroutines, since flock locks belong to open file descriptions.
func (l *Log) lock(mode int) (*os.File, error) {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), mode)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func unlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// scan reads only newline-terminated entries; a torn final line is not committed.
// The returned offset is the boundary after the last complete line.
func scan(r io.Reader, fn func(Entry) error) (int64, error) {
	reader := bufio.NewReader(r)
	var offset int64
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return offset, nil
		}
		if err != nil {
			return offset, err
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return offset, fmt.Errorf("invalid query log line at byte %d: %w", offset, err)
		}
		offset += int64(len(line))
		if err := fn(e); err != nil {
			return offset, err
		}
	}
}

func (l *Log) Append(e Entry) (Entry, error) {
	if len(e.Rows) > query.MaxPreviewLimit {
		return Entry{}, errors.New("query log preview exceeds 1000 rows")
	}
	f, err := l.lock(syscall.LOCK_EX)
	if err != nil {
		return Entry{}, err
	}
	defer unlock(f)
	var last uint64
	offset, err := scan(f, func(stored Entry) error {
		if !strings.HasPrefix(stored.ID, "q_") {
			return errors.New("invalid query log id")
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(stored.ID, "q_"), 10, 64)
		if err != nil || n <= last {
			return errors.New("invalid query log sequence")
		}
		last = n
		return nil
	})
	if err != nil {
		return Entry{}, err
	}
	if last == ^uint64(0) {
		return Entry{}, errors.New("query log sequence exhausted")
	}
	e.V, e.ID, e.TS, e.Conn = 1, fmt.Sprintf("q_%04d", last+1), time.Now().UTC(), l.conn
	if e.Columns == nil {
		e.Columns = []query.Column{}
	}
	if e.Rows == nil {
		e.Rows = [][]any{}
	}
	data, err := json.Marshal(e)
	if err != nil {
		return Entry{}, err
	}
	// Recover only the uncommitted suffix under the allocation lock. Completed
	// lines are never changed, and the next entry cannot join a torn JSON object.
	if err := f.Truncate(offset); err != nil {
		return Entry{}, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Entry{}, err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return Entry{}, err
	}
	if err := f.Sync(); err != nil {
		return Entry{}, err
	}
	return e, nil
}

func (l *Log) List(limit int) ([]Entry, error) {
	f, err := l.lock(syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock(f)
	entries := make([]Entry, 0)
	// A ring keeps only the requested recent entries while scanning the file.
	var count int
	_, err = scan(f, func(e Entry) error {
		if limit <= 0 || len(entries) < limit {
			entries = append(entries, e)
		} else {
			entries[count%limit] = e
		}
		count++
		return nil
	})
	if err != nil {
		return nil, err
	}
	if limit > 0 && count > limit {
		start := count % limit
		entries = append(entries[start:], entries[:start]...)
	}
	return entries, nil
}

func (l *Log) Get(id string) (Entry, error) {
	f, err := l.lock(syscall.LOCK_SH)
	if err != nil {
		return Entry{}, err
	}
	defer unlock(f)
	var found Entry
	_, err = scan(f, func(e Entry) error {
		if e.ID == id {
			found = e
		}
		return nil
	})
	if err != nil {
		return Entry{}, err
	}
	if found.ID == "" {
		return Entry{}, ErrNotFound
	}
	return found, nil
}

func (l *Log) Size() (int64, error) {
	f, err := l.lock(syscall.LOCK_SH)
	if err != nil {
		return 0, err
	}
	defer unlock(f)
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

var errFollowBatch = errors.New("query log follow batch complete")

// lineBoundary backs up a caller's byte offset to a complete-line boundary.
// In particular, Size may have observed a torn suffix that a writer repairs.
func lineBoundary(f *os.File, offset int64) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	offset = min(offset, info.Size())
	var buf [4096]byte
	for offset > 0 {
		start := max(int64(0), offset-int64(len(buf)))
		n, err := f.ReadAt(buf[:offset-start], start)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		for i := n - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				return start + int64(i) + 1, nil
			}
		}
		offset = start
	}
	return 0, nil
}

func (l *Log) Follow(ctx context.Context, fromOffset int64, fn func(Entry)) error {
	if fromOffset < 0 || fn == nil {
		return errors.New("invalid query log follow arguments")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	offset := fromOffset
	aligned := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := l.lock(syscall.LOCK_SH)
		if err != nil {
			return err
		}
		if !aligned {
			offset, err = lineBoundary(f, offset)
			aligned = err == nil
		}
		if err == nil {
			_, err = f.Seek(offset, io.SeekStart)
		}
		var entries []Entry
		var read int64
		if err == nil {
			read, err = scan(f, func(e Entry) error {
				entries = append(entries, e)
				if len(entries) == 64 {
					return errFollowBatch
				}
				return nil
			})
		}
		unlock(f)
		more := errors.Is(err, errFollowBatch)
		if err != nil && !more {
			return err
		}
		offset += read
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			fn(e)
		}
		if more {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
