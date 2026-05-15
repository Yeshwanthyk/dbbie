package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/yeshwanthyk/dbbie/internal/db"
)

type Connection struct {
	Name   string  `json:"name"`
	Driver db.Kind `json:"driver"`
	DSN    string  `json:"dsn"`
}

type PublicConnection struct {
	Name   string  `json:"name"`
	Driver db.Kind `json:"driver"`
}

type File struct {
	Connections []Connection `json:"connections"`
}

func Path() (string, error) {
	if override := os.Getenv("DBBIE_CONFIG"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dbbie", "config.json"), nil
}

func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}
	var cfg File
	if err := json.Unmarshal(data, &cfg); err != nil {
		return File{}, err
	}
	return cfg, nil
}

func Save(path string, cfg File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (f File) Find(name string) (Connection, bool) {
	for _, conn := range f.Connections {
		if conn.Name == name {
			return conn, true
		}
	}
	return Connection{}, false
}

func (f *File) Upsert(conn Connection) {
	for idx := range f.Connections {
		if f.Connections[idx].Name == conn.Name {
			f.Connections[idx] = conn
			return
		}
	}
	f.Connections = append(f.Connections, conn)
}

func (f File) PublicConnections() []PublicConnection {
	out := make([]PublicConnection, len(f.Connections))
	for idx, conn := range f.Connections {
		out[idx] = PublicConnection{Name: conn.Name, Driver: conn.Driver}
	}
	return out
}
