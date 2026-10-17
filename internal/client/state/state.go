// Package state is the PC app's local database: which files were uploaded
// already (so they are not uploaded again), unfinished uploads (to resume
// them) and the upload history shown in the app.
package state

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the local SQLite database.
type DB struct{ db *sql.DB }

// Open opens (or creates) state.db in dir.
func Open(dir string) (*DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Set("_txlock", "immediate")
	d, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "state.db")+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(4)
	_, err = d.Exec(`
CREATE TABLE IF NOT EXISTS files (
	folder_id   TEXT NOT NULL,
	rel         TEXT NOT NULL,
	size        INTEGER NOT NULL,
	mtime       INTEGER NOT NULL,
	sha256      TEXT NOT NULL DEFAULT '',
	remote      TEXT NOT NULL DEFAULT '',
	uploaded_at INTEGER NOT NULL,
	PRIMARY KEY (folder_id, rel)
);
CREATE TABLE IF NOT EXISTS pending (
	key        TEXT PRIMARY KEY,
	upload_id  TEXT NOT NULL,
	size       INTEGER NOT NULL,
	mtime      INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS history (
	id      INTEGER PRIMARY KEY,
	time    INTEGER NOT NULL,
	kind    TEXT NOT NULL DEFAULT 'upload',
	local   TEXT NOT NULL,
	remote  TEXT NOT NULL DEFAULT '',
	size    INTEGER NOT NULL DEFAULT 0,
	status  TEXT NOT NULL,
	error   TEXT NOT NULL DEFAULT '',
	folder_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS history_time ON history(id);
`)
	if err != nil {
		d.Close()
		return nil, err
	}
	return &DB{db: d}, nil
}

// Close closes the database.
func (s *DB) Close() error { return s.db.Close() }

// FileState is what we know about an uploaded file.
type FileState struct {
	Size   int64
	MTime  int64
	SHA256 string
	Remote string
}

// Get returns the state of a file (ok=false if never uploaded).
func (s *DB) Get(folderID, rel string) (FileState, bool) {
	var f FileState
	err := s.db.QueryRow(`SELECT size, mtime, sha256, remote FROM files WHERE folder_id = ? AND rel = ?`, folderID, rel).
		Scan(&f.Size, &f.MTime, &f.SHA256, &f.Remote)
	return f, err == nil
}

// Put records a finished upload.
func (s *DB) Put(folderID, rel string, f FileState) error {
	_, err := s.db.Exec(`INSERT INTO files(folder_id, rel, size, mtime, sha256, remote, uploaded_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_id, rel) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, sha256 = excluded.sha256,
		remote = excluded.remote, uploaded_at = excluded.uploaded_at`,
		folderID, rel, f.Size, f.MTime, f.SHA256, f.Remote, time.Now().UnixMilli())
	return err
}

// ForgetFolder removes all state of a folder (when it is removed).
func (s *DB) ForgetFolder(folderID string) {
	s.db.Exec(`DELETE FROM files WHERE folder_id = ?`, folderID)
}

// FolderStats returns the number and total size of uploaded files.
func (s *DB) FolderStats(folderID string) (count, size int64) {
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM files WHERE folder_id = ?`, folderID).Scan(&count, &size)
	return
}

// Pending returns an unfinished upload for key if the file did not change.
func (s *DB) Pending(key string, size, mtime int64) (string, bool) {
	var id string
	var psize, pmtime int64
	if err := s.db.QueryRow(`SELECT upload_id, size, mtime FROM pending WHERE key = ?`, key).Scan(&id, &psize, &pmtime); err != nil {
		return "", false
	}
	if psize != size || pmtime != mtime {
		s.ClearPending(key)
		return "", false
	}
	return id, true
}

// SetPending remembers an unfinished upload.
func (s *DB) SetPending(key, uploadID string, size, mtime int64) {
	s.db.Exec(`INSERT OR REPLACE INTO pending(key, upload_id, size, mtime, created_at) VALUES (?, ?, ?, ?, ?)`,
		key, uploadID, size, mtime, time.Now().UnixMilli())
}

// ClearPending forgets an unfinished upload.
func (s *DB) ClearPending(key string) {
	s.db.Exec(`DELETE FROM pending WHERE key = ?`, key)
}

// Entry is a line of the upload history.
type Entry struct {
	ID       int64  `json:"id"`
	Time     int64  `json:"time"`
	Kind     string `json:"kind"`
	Local    string `json:"local"`
	Remote   string `json:"remote"`
	Size     int64  `json:"size"`
	Status   string `json:"status"` // done, exists, error, skipped
	Error    string `json:"error,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
}

// AddHistory appends an entry and trims the history to 5000 lines.
func (s *DB) AddHistory(e Entry) int64 {
	res, err := s.db.Exec(`INSERT INTO history(time, kind, local, remote, size, status, error, folder_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UnixMilli(), e.Kind, e.Local, e.Remote, e.Size, e.Status, e.Error, e.FolderID)
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	if id%100 == 0 {
		s.db.Exec(`DELETE FROM history WHERE id <= ?`, id-5000)
	}
	return id
}

// History returns the newest entries (before id, if > 0).
func (s *DB) History(before int64, limit int, onlyErrors bool) ([]Entry, error) {
	if before <= 0 {
		before = 1 << 62
	}
	q := `SELECT id, time, kind, local, remote, size, status, error, folder_id FROM history WHERE id < ?`
	if onlyErrors {
		q += ` AND status = 'error'`
	}
	rows, err := s.db.Query(q+` ORDER BY id DESC LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Time, &e.Kind, &e.Local, &e.Remote, &e.Size, &e.Status, &e.Error, &e.FolderID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HistoryEntry loads one entry.
func (s *DB) HistoryEntry(id int64) (Entry, error) {
	var e Entry
	err := s.db.QueryRow(`SELECT id, time, kind, local, remote, size, status, error, folder_id FROM history WHERE id = ?`, id).
		Scan(&e.ID, &e.Time, &e.Kind, &e.Local, &e.Remote, &e.Size, &e.Status, &e.Error, &e.FolderID)
	return e, err
}

// ClearHistory deletes the history.
func (s *DB) ClearHistory() { s.db.Exec(`DELETE FROM history`) }

// MarkRetried changes the status of an entry after a retry was queued.
func (s *DB) MarkRetried(id int64) {
	s.db.Exec(`UPDATE history SET status = 'retried' WHERE id = ?`, id)
}
