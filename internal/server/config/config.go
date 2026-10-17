// Package config loads and saves the server configuration (config.json in the
// data directory). Users, teams and mounts live in the database instead.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const DefaultPort = 8420

// Config is the persisted server configuration.
type Config struct {
	// Port the server listens on (on the Tailscale addresses and on localhost).
	Port int `json:"port"`
	// StorageLocations are the folders (usually one per hard disk) that hold
	// the users' and teams' files. New files go to the location with the most
	// free space, so a full disk spills over to the next one.
	StorageLocations []string `json:"storage_locations"`
	// DefaultQuotaGB is the storage limit for newly created users (0 = unlimited).
	DefaultQuotaGB int64 `json:"default_quota_gb"`
	// TrashDays is how long deleted files stay in the trash.
	TrashDays int `json:"trash_days"`
	// HTTPS enables serving HTTPS with the certificate from `tailscale cert`.
	HTTPS bool `json:"https"`
	// ListenAddrs overrides the automatic Tailscale detection, e.g.
	// ["0.0.0.0:8420"] inside Docker. Leave empty for the default behaviour.
	ListenAddrs []string `json:"listen_addrs,omitempty"`
	// UpdateCheck enables the daily GitHub release check.
	UpdateCheck bool `json:"update_check"`
	// Language is the default UI language ("de" or "en").
	Language string `json:"language"`
	// ScanIntervalMinutes controls the periodic full rescan of all folders.
	ScanIntervalMinutes int `json:"scan_interval_minutes"`
}

// Store wraps a Config with its file path and a lock.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// DefaultDataDir returns the platform default data directory.
func DefaultDataDir() string {
	if d := os.Getenv("HOMELYDRIVE_DATA"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "Homelydrive")
	}
	if os.Geteuid() == 0 {
		return "/var/lib/homelydrive"
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "homelydrive")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "homelydrive")
}

// Defaults returns a configuration with sensible defaults for dataDir.
func Defaults(dataDir string) Config {
	return Config{
		Port:                DefaultPort,
		StorageLocations:    []string{filepath.Join(dataDir, "storage")},
		DefaultQuotaGB:      100,
		TrashDays:           30,
		UpdateCheck:         true,
		Language:            "de",
		ScanIntervalMinutes: 30,
	}
}

// Load reads config.json from dataDir, creating it with defaults if missing.
func Load(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "config.json"), cfg: Defaults(dataDir)}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dataDir, 0o750); err != nil {
			return nil, err
		}
		return s, s.save()
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &s.cfg); err != nil {
		return nil, err
	}
	s.normalize()
	return s, nil
}

func (s *Store) normalize() {
	c := &s.cfg
	if c.Port <= 0 || c.Port > 65535 {
		c.Port = DefaultPort
	}
	if c.TrashDays <= 0 {
		c.TrashDays = 30
	}
	if c.ScanIntervalMinutes <= 0 {
		c.ScanIntervalMinutes = 30
	}
	if c.Language != "en" {
		c.Language = "de"
	}
	if len(c.StorageLocations) == 0 {
		c.StorageLocations = Defaults(filepath.Dir(s.path)).StorageLocations
	}
}

// Get returns a copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.StorageLocations = append([]string(nil), s.cfg.StorageLocations...)
	c.ListenAddrs = append([]string(nil), s.cfg.ListenAddrs...)
	return c
}

// Update applies fn to the configuration and saves it.
func (s *Store) Update(fn func(c *Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	s.normalize()
	return s.save()
}

// Path returns the location of config.json.
func (s *Store) Path() string { return s.path }

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
