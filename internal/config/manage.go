package config

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// ValidateName keeps names usable as connection log filenames, not credentials.
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\r\n\t:=@") {
		return errors.New("invalid connection name")
	}
	return nil
}

// Update holds a stable sidecar lock across the entire read-modify-write.
func Update(path string, fn func(*File) error) error {
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return err
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	cfg, err := Load(path)
	if err != nil {
		return errors.New("could not read connection configuration")
	}
	if err := fn(&cfg); err != nil {
		return err
	}
	return Save(path, cfg)
}

func (f *File) Remove(name string) error {
	for i, conn := range f.Connections {
		if conn.Name == name {
			f.Connections = append(f.Connections[:i], f.Connections[i+1:]...)
			return nil
		}
	}
	return errors.New("unknown connection")
}

func (f *File) Rename(old, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if _, ok := f.Find(name); ok {
		return errors.New("connection name already exists")
	}
	for i := range f.Connections {
		if f.Connections[i].Name == old {
			f.Connections[i].Name = name
			return nil
		}
	}
	return errors.New("unknown connection")
}

var credentialURL = regexp.MustCompile(`(?i)(?:postgres(?:ql)?|mysql)://[^\s"'<>]+`)

// RedactStrings protects persisted session text as well as its credential-free schema.
// Fail closed if configured credentials cannot be read for redaction.
func RedactStrings(values ...string) ([]string, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	cfg, err := Load(path)
	if err != nil {
		return nil, errors.New("could not read connection configuration")
	}
	out := make([]string, len(values))
	for i, value := range values {
		for _, conn := range cfg.Connections {
			if conn.DSN != "" {
				value = strings.ReplaceAll(value, conn.DSN, "[redacted]")
			}
		}
		out[i] = credentialURL.ReplaceAllString(value, "[redacted]")
	}
	return out, nil
}
