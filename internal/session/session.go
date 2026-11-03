// Package session stores named, ordered query trails, never connection credentials.
package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/yeshwanthyk/dbbie/internal/config"
	"github.com/yeshwanthyk/dbbie/internal/qlog"
)

var (
	ErrNotFound  = errors.New("session or item not found")
	ErrExists    = errors.New("session already exists")
	namePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	logIDPattern = regexp.MustCompile(`^q_[0-9]+$`)
)

type Item struct {
	N       int       `json:"n"`
	Title   string    `json:"title"`
	Why     string    `json:"why"`
	SQL     string    `json:"sql"`
	Conn    string    `json:"conn"`
	LogID   string    `json:"logId"`
	Added   time.Time `json:"added"`
	AddedBy string    `json:"addedBy"`
}

type Session struct {
	V         int       `json:"v"`
	Name      string    `json:"name"`
	Title     string    `json:"title"`
	Conn      string    `json:"conn"`
	Driver    string    `json:"driver"`
	CreatedBy string    `json:"createdBy"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Items     []Item    `json:"items"`
	// NextN persists the high-water mark so deleting the last item never reuses its number.
	NextN int `json:"nextN,omitempty"`
}

type Summary struct {
	Name      string    `json:"name"`
	Title     string    `json:"title"`
	Conn      string    `json:"conn"`
	Items     int       `json:"items"`
	Updated   time.Time `json:"updated"`
	CreatedBy string    `json:"createdBy"`
}

func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return errors.New("invalid session name: use 1-64 lowercase letters, digits, dots, underscores or hyphens, starting with a letter or digit")
	}
	return nil
}

func directory() (string, error) {
	dir, err := qlog.StateDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Lock a stable sidecar inode, not the data file that atomic rename replaces.
// Each operation opens its own descriptor, serializing goroutines and processes.
func locked(name string, exclusive bool, fn func(string) error) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	dir, err := directory()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "."+name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	for {
		err = syscall.Flock(int(f.Fd()), mode)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn(filepath.Join(dir, name+".json"))
}

func load(path, name string) (*Session, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, errors.New("invalid session file")
	}
	if s.V != 1 || s.Name != name || config.ValidateName(s.Conn) != nil || (s.Driver != "postgres" && s.Driver != "mysql") {
		return nil, errors.New("invalid session file")
	}
	last := 0
	for i := range s.Items {
		item := &s.Items[i]
		if item.N <= last || (item.Conn != "" && item.Conn != s.Conn) {
			return nil, errors.New("invalid session items")
		}
		item.Conn = s.Conn // older files can omit the redundant connection field
		last = item.N
	}
	if s.NextN <= last {
		s.NextN = last + 1
	}
	if s.Items == nil {
		s.Items = []Item{}
	}
	return &s, nil
}

func save(path string, s *Session) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".session-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func Open(name string) (*Session, error) {
	var s *Session
	err := locked(name, false, func(path string) (err error) { s, err = load(path, name); return err })
	return s, err
}

func Create(name, title, conn, driver, actor string) (*Session, error) {
	if err := config.ValidateName(conn); err != nil {
		return nil, err
	}
	if driver != "postgres" && driver != "mysql" {
		return nil, errors.New("invalid session driver")
	}
	var s *Session
	err := locked(name, true, func(path string) error {
		if _, err := os.Stat(path); err == nil {
			return ErrExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		clean, err := config.RedactStrings(title, actor)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		s = &Session{V: 1, Name: name, Title: clean[0], Conn: conn, Driver: driver, CreatedBy: clean[1], Created: now, Updated: now, Items: []Item{}, NextN: 1}
		return save(path, s)
	})
	return s, err
}

func List() ([]Summary, error) {
	dir, err := directory()
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(file.Name(), ".json")
		s, err := Open(name)
		if errors.Is(err, ErrNotFound) {
			continue
		} // concurrently deleted
		if err != nil {
			return nil, err
		}
		out = append(out, Summary{s.Name, s.Title, s.Conn, len(s.Items), s.Updated, s.CreatedBy})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Updated.Equal(out[j].Updated) {
			return out[i].Name < out[j].Name
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out, nil
}

func mutate(name string, fn func(*Session) error) error {
	return locked(name, true, func(path string) error {
		s, err := load(path, name)
		if err != nil {
			return err
		}
		if err := fn(s); err != nil {
			return err
		}
		s.Updated = time.Now().UTC()
		return save(path, s)
	})
}

func Add(name string, item Item) (Item, error) {
	err := mutate(name, func(s *Session) error {
		if item.Conn != "" && item.Conn != s.Conn {
			return errors.New("session connection mismatch")
		}
		if item.LogID != "" && !logIDPattern.MatchString(item.LogID) {
			return errors.New("invalid query log id")
		}
		clean, err := config.RedactStrings(item.Title, item.Why, item.SQL, item.AddedBy, item.LogID)
		if err != nil {
			return err
		}
		item.Title, item.Why, item.SQL, item.AddedBy, item.LogID = clean[0], clean[1], clean[2], clean[3], clean[4]
		if s.NextN <= 0 || s.NextN == int(^uint(0)>>1) {
			return errors.New("session item sequence exhausted")
		}
		item.N, item.Conn, item.Added = s.NextN, s.Conn, time.Now().UTC()
		s.NextN++
		s.Items = append(s.Items, item)
		return nil
	})
	return item, err
}

func Remove(name string, n int) error {
	return mutate(name, func(s *Session) error {
		for i, item := range s.Items {
			if item.N == n {
				s.Items = append(s.Items[:i], s.Items[i+1:]...)
				return nil
			}
		}
		return ErrNotFound
	})
}

func SetLogID(name string, n int, logID string) error {
	if !logIDPattern.MatchString(logID) {
		return errors.New("invalid query log id")
	}
	return mutate(name, func(s *Session) error {
		for i := range s.Items {
			if s.Items[i].N == n {
				s.Items[i].LogID = logID
				return nil
			}
		}
		return ErrNotFound
	})
}

func Delete(name string) error {
	return locked(name, true, func(path string) error {
		if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		// Keep the lock inode: removing it could admit two concurrent owners.
		return syncDir(filepath.Dir(path))
	})
}
