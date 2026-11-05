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
	"slices"
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
	Session   string         `json:"session,omitempty"`
	Item      int            `json:"item,omitempty"` // session item N this run refreshes
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

func StateDir() (string, error) {
	dir := os.Getenv("DBBIE_STATE_DIR")
	if dir == "" {
		if state := os.Getenv("XDG_STATE_HOME"); state != "" {
			dir = filepath.Join(state, "dbbie")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, ".local", "state", "dbbie")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func Open(conn string) (*Log, error) {
	if conn == "" || conn == "." || conn == ".." || strings.ContainsAny(conn, "/\\\x00\r\n") {
		return nil, errors.New("invalid query log connection name")
	}
	dir, err := StateDir()
	if err != nil {
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

// parseID returns the sequence number of an id written by Append ("q_" + digits).
func parseID(id string) (uint64, bool) {
	digits, ok := strings.CutPrefix(id, "q_")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	return n, err == nil
}

// committedEnd is the boundary after the last complete line; bytes past it are
// a torn suffix that readers ignore and the next Append truncates.
func committedEnd(f *os.File) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return lineBoundary(f, info.Size())
}

// readLine returns the complete line that starts at start, ending at or before
// end, and the offset after its newline.
func readLine(f *os.File, start, end int64) ([]byte, int64, error) {
	r := bufio.NewReaderSize(io.NewSectionReader(f, start, end-start), 64<<10)
	line, err := r.ReadBytes('\n')
	if err != nil {
		// Callers pass committed boundaries, so a missing newline means the
		// file changed under the lock.
		return nil, 0, fmt.Errorf("query log line at byte %d is incomplete: %w", start, err)
	}
	return line, start + int64(len(line)), nil
}

func decodeLine(line []byte, offset int64, v any) error {
	if err := json.Unmarshal(line, v); err != nil {
		return fmt.Errorf("invalid query log line at byte %d: %w", offset, err)
	}
	return nil
}

// tail decodes up to limit complete lines that end at end, newest first.
func tail(f *os.File, end int64, limit int) ([]Entry, error) {
	var entries []Entry
	for end > 0 && len(entries) < limit {
		start, err := lineBoundary(f, end-1)
		if err != nil {
			return nil, err
		}
		line := make([]byte, end-start)
		if _, err := f.ReadAt(line, start); err != nil {
			return nil, err
		}
		var e Entry
		if err := decodeLine(line, start, &e); err != nil {
			return nil, err
		}
		entries = append(entries, e)
		end = start
	}
	return entries, nil
}

// Append allocates the next id from the last complete line only. Ids are
// strictly increasing because every writer allocates under LOCK_EX from the
// newest committed entry, so the last line holds the maximum.
func (l *Log) Append(e Entry) (Entry, error) {
	if len(e.Rows) > query.MaxPreviewLimit {
		return Entry{}, errors.New("query log preview exceeds 1000 rows")
	}
	f, err := l.lock(syscall.LOCK_EX)
	if err != nil {
		return Entry{}, err
	}
	defer unlock(f)
	offset, err := committedEnd(f)
	if err != nil {
		return Entry{}, err
	}
	newest, err := tail(f, offset, 1)
	if err != nil {
		return Entry{}, err
	}
	var last uint64
	if len(newest) == 1 {
		n, ok := parseID(newest[0].ID)
		if !ok {
			return Entry{}, errors.New("invalid query log id")
		}
		if n == 0 {
			return Entry{}, errors.New("invalid query log sequence")
		}
		last = n
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

// List returns the newest limit entries in log order, or every entry when
// limit <= 0. A positive limit reads and decodes only those lines.
func (l *Log) List(limit int) ([]Entry, error) {
	f, err := l.lock(syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock(f)
	if limit > 0 {
		end, err := committedEnd(f)
		if err != nil {
			return nil, err
		}
		entries, err := tail(f, end, limit)
		if err != nil {
			return nil, err
		}
		slices.Reverse(entries)
		if entries == nil {
			entries = make([]Entry, 0)
		}
		return entries, nil
	}
	entries := make([]Entry, 0)
	_, err = scan(f, func(e Entry) error {
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Get binary-searches committed lines by id number, relying on the strictly
// increasing ids Append writes. Probes decode only the id; the match is
// decoded in full.
func (l *Log) Get(id string) (Entry, error) {
	target, ok := parseID(id)
	if !ok {
		return Entry{}, ErrNotFound
	}
	f, err := l.lock(syscall.LOCK_SH)
	if err != nil {
		return Entry{}, err
	}
	defer unlock(f)
	hi, err := committedEnd(f)
	if err != nil {
		return Entry{}, err
	}
	var lo int64 // lo and hi are always line boundaries
	for lo < hi {
		start, err := lineBoundary(f, lo+(hi-lo)/2)
		if err != nil {
			return Entry{}, err
		}
		start = max(start, lo)
		line, next, err := readLine(f, start, hi)
		if err != nil {
			return Entry{}, err
		}
		var head struct {
			ID string `json:"id"`
		}
		if err := decodeLine(line, start, &head); err != nil {
			return Entry{}, err
		}
		n, ok := parseID(head.ID)
		if !ok {
			return Entry{}, fmt.Errorf("invalid query log id at byte %d", start)
		}
		switch {
		case n < target:
			lo = next
		case n > target:
			hi = start
		default:
			var e Entry
			if err := decodeLine(line, start, &e); err != nil {
				return Entry{}, err
			}
			if e.ID != id {
				return Entry{}, ErrNotFound
			}
			return e, nil
		}
	}
	return Entry{}, ErrNotFound
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
	var buf [64 << 10]byte
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
