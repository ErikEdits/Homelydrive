// Package core contains the business logic of the Homelydrive server: users,
// spaces (own drive, team folders, mounted folders, shares), files, uploads,
// trash, search, sharing, notifications and the vault.
package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/erikedits/homelydrive/internal/server/config"
	"github.com/erikedits/homelydrive/internal/server/storage"
	"github.com/erikedits/homelydrive/internal/version"
)

// Error values mapped to HTTP status codes by the API layer.
var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrExists       = errors.New("already exists")
	ErrQuota        = errors.New("storage limit reached")
	ErrUnauthorized = errors.New("unauthorized")
	ErrVaultLocked  = errors.New("vault is locked")
	ErrConflict     = errors.New("conflict")
)

// BadRequest is an input validation error with a message for the user.
type BadRequest struct{ Msg string }

func (e *BadRequest) Error() string { return e.Msg }

func badRequest(format string, args ...any) error {
	return &BadRequest{Msg: fmt.Sprintf(format, args...)}
}

// Core is the server's central object.
type Core struct {
	DB      *sql.DB
	Cfg     *config.Store
	DataDir string
	Log     *slog.Logger
	Events  *Broker

	thumbSem  chan struct{}
	indexKick chan struct{}

	spacesMu sync.Mutex
	spaces   map[int64]*Space

	typesMu sync.RWMutex
	types   map[string]FileType

	vaultMu   sync.Mutex
	vaultKeys map[int64]*vaultSession

	authCacheMu sync.Mutex
	authCache   map[string]authCacheEntry

	watch *watcher

	updateMu sync.Mutex
	update   *version.Release
}

// New creates the core. Call Start to launch the background workers.
func New(db *sql.DB, cfg *config.Store, dataDir string, log *slog.Logger) (*Core, error) {
	c := &Core{
		DB:        db,
		Cfg:       cfg,
		DataDir:   dataDir,
		Log:       log,
		Events:    NewBroker(),
		thumbSem:  make(chan struct{}, 2),
		indexKick: make(chan struct{}, 1),
		spaces:    map[int64]*Space{},
		vaultKeys: map[int64]*vaultSession{},
		authCache: map[string]authCacheEntry{},
	}
	if err := c.loadFileTypes(); err != nil {
		return nil, err
	}
	return c, nil
}

// Start launches background work: indexing, file watching, trash expiry,
// upload cleanup, disk space warnings and the update check.
func (c *Core) Start(ctx context.Context) {
	go c.indexWorker(ctx)
	go c.watchLoop(ctx)
	go c.scanLoop(ctx)
	go c.maintenanceLoop(ctx)
}

func (c *Core) maintenanceLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	first := time.After(30 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
		case <-t.C:
		}
		c.ExpireTrash()
		c.cleanupUploads()
		c.checkDiskSpace()
		c.lockIdleVaults()
		if c.Cfg.Get().UpdateCheck {
			c.checkUpdate(ctx)
		}
	}
}

func (c *Core) checkUpdate(ctx context.Context) {
	c.updateMu.Lock()
	last := c.update
	c.updateMu.Unlock()
	rel, err := version.CheckLatest(ctx)
	if err != nil {
		c.Log.Debug("update check failed", "err", err)
		return
	}
	c.updateMu.Lock()
	c.update = rel
	c.updateMu.Unlock()
	if rel.Newer && (last == nil || last.Version != rel.Version) {
		c.Log.Info("new version available", "version", rel.Version)
		c.notifyAdmins("update", map[string]any{"version": rel.Version, "url": rel.URL})
	}
}

// CheckUpdateNow runs the GitHub release check immediately.
func (c *Core) CheckUpdateNow(ctx context.Context) { c.checkUpdate(ctx) }

// Update returns the result of the last release check (may be nil).
func (c *Core) Update() *version.Release {
	c.updateMu.Lock()
	defer c.updateMu.Unlock()
	return c.update
}

func now() int64 { return time.Now().UnixMilli() }

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ThumbDir is where generated thumbnails are cached.
func (c *Core) ThumbDir() string { return filepath.Join(c.DataDir, "thumbs") }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// getSetting / setSetting store small values in the settings table.
func (c *Core) getSetting(key string) string {
	var v string
	c.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

func (c *Core) setSetting(key, value string) {
	c.DB.Exec(`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
}

type configT = config.Config

type storageEntry = storage.Entry

func contextBackground() context.Context { return context.Background() }
