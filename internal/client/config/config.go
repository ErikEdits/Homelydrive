// Package config stores the PC app settings (client.json in the user's
// config folder: %APPDATA%\Homelydrive on Windows, ~/.config/Homelydrive on Linux).
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Folder is a local folder that is uploaded to the server (upload only:
// files deleted locally stay on the server).
type Folder struct {
	ID      string `json:"id"`
	Local   string `json:"local"`
	Space   string `json:"space"`  // remote space reference, e.g. "home"
	Remote  string `json:"remote"` // remote folder path, e.g. "PC/Dokumente"
	Enabled bool   `json:"enabled"`
}

// Config is the persisted PC app configuration.
type Config struct {
	Server        string   `json:"server"`
	Token         string   `json:"token"`
	Username      string   `json:"username"`
	DeviceName    string   `json:"device_name"`
	Folders       []Folder `json:"folders"`
	BandwidthKBps int      `json:"bandwidth_kbps"` // upload limit, 0 = unlimited
	Paused        bool     `json:"paused"`
	Notifications bool     `json:"notifications"`
	DriveEnabled  bool     `json:"drive_enabled"`
	DriveMount    string   `json:"drive_mount"`    // "H:" on Windows, a folder on Linux
	DriveCacheMB  int      `json:"drive_cache_mb"` // size of the read cache of the virtual drive
	UploadFolder  string   `json:"upload_folder"`  // target of "Auf Homelydrive hochladen"
	DownloadDir   string   `json:"download_dir"`
	Language      string   `json:"language"`
	UpdateCheck   bool     `json:"update_check"`
	Integration   bool     `json:"integration"` // context menu entries installed
}

// Store holds the config and saves changes.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// Dir returns the folder holding the app's files.
func Dir() string {
	if d := os.Getenv("HOMELYDRIVE_CLIENT_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "Homelydrive")
}

func defaults() Config {
	host, _ := os.Hostname()
	mount := "H:"
	if runtime.GOOS != "windows" {
		home, _ := os.UserHomeDir()
		mount = filepath.Join(home, "Homelydrive")
	}
	dl := ""
	if home, err := os.UserHomeDir(); err == nil {
		dl = filepath.Join(home, "Downloads")
	}
	return Config{
		DeviceName:    "PC " + host,
		Notifications: true,
		DriveMount:    mount,
		DriveCacheMB:  2048,
		UploadFolder:  "Vom PC hochgeladen",
		DownloadDir:   dl,
		UpdateCheck:   true,
	}
}

// Load reads the config (creating defaults on first start).
func Load() (*Store, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "client.json"), cfg: defaults()}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns a copy of the config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.Folders = append([]Folder(nil), s.cfg.Folders...)
	return c
}

// Update changes the config and saves it.
func (s *Store) Update(fn func(c *Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	s.cfg.Server = strings.TrimRight(strings.TrimSpace(s.cfg.Server), "/")
	return s.save()
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// NewID returns a random identifier for folders.
func NewID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
