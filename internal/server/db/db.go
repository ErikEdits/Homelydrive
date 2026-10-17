// Package db opens the SQLite database and applies schema migrations.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite" // pure Go SQLite driver (includes FTS5)
)

// Open opens (and migrates) the database file in dataDir.
func Open(dataDir string) (*sql.DB, error) {
	return OpenFile(filepath.Join(dataDir, "homelydrive.db"))
}

// OpenFile opens (and migrates) the database at path. Use ":memory:" in tests.
func OpenFile(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	if path != ":memory:" {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	q.Set("_txlock", "immediate")
	dsn := "file:" + path + "?" + q.Encode()
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		d.SetMaxOpenConns(1)
	} else {
		d.SetMaxOpenConns(8)
	}
	if err := migrate(d); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func migrate(d *sql.DB) error {
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := d.QueryRow(`SELECT version FROM schema_version`).Scan(&v)
	if err == sql.ErrNoRows {
		if _, err := d.Exec(`INSERT INTO schema_version(version) VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := d.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// migrations are applied in order; never edit an existing entry, only append.
var migrations = []string{
	`
CREATE TABLE users (
	id            INTEGER PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	display_name  TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	is_admin      INTEGER NOT NULL DEFAULT 0,
	quota_bytes   INTEGER NOT NULL DEFAULT 0,
	lang          TEXT NOT NULL DEFAULT '',
	vault_salt    BLOB,
	vault_key     BLOB,
	warned_quota  INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);

CREATE TABLE tokens (
	id           INTEGER PRIMARY KEY,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash   TEXT NOT NULL UNIQUE,
	device       TEXT NOT NULL DEFAULT '',
	admin        INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL,
	last_used_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE spaces (
	id         INTEGER PRIMARY KEY,
	kind       TEXT NOT NULL,             -- home | team | mount
	name       TEXT NOT NULL,
	owner_id   INTEGER REFERENCES users(id) ON DELETE CASCADE,
	root_path  TEXT NOT NULL DEFAULT '',  -- mounts only
	created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX spaces_kind_name ON spaces(kind, name);

CREATE TABLE space_members (
	space_id  INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	can_write INTEGER NOT NULL DEFAULT 1,
	PRIMARY KEY (space_id, user_id)
);

CREATE TABLE files (
	id         INTEGER PRIMARY KEY,
	space_id   INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	path       TEXT NOT NULL,
	parent     TEXT NOT NULL,
	name       TEXT NOT NULL,
	is_dir     INTEGER NOT NULL,
	size       INTEGER NOT NULL DEFAULT 0,
	mtime      INTEGER NOT NULL DEFAULT 0,
	sha256     TEXT NOT NULL DEFAULT '',
	mime       TEXT NOT NULL DEFAULT '',
	category   TEXT NOT NULL DEFAULT '',
	sort_time  INTEGER NOT NULL DEFAULT 0,
	indexed    INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	UNIQUE (space_id, path)
);
CREATE INDEX files_parent ON files(space_id, parent);
CREATE INDEX files_sha ON files(sha256) WHERE sha256 <> '';
CREATE INDEX files_gallery ON files(category, sort_time);
CREATE INDEX files_size ON files(space_id, size);
CREATE INDEX files_pending ON files(indexed) WHERE indexed = 0;

CREATE VIRTUAL TABLE files_fts USING fts5(name, content, tokenize = 'unicode61 remove_diacritics 2');
CREATE TRIGGER files_ai AFTER INSERT ON files BEGIN
	INSERT INTO files_fts(rowid, name, content) VALUES (new.id, new.name, '');
END;
CREATE TRIGGER files_ad AFTER DELETE ON files BEGIN
	DELETE FROM files_fts WHERE rowid = old.id;
END;
CREATE TRIGGER files_au AFTER UPDATE OF name ON files BEGIN
	UPDATE files_fts SET name = new.name WHERE rowid = new.id;
END;

CREATE TABLE file_meta (
	file_id INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
	color   TEXT NOT NULL DEFAULT '',
	note    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE stars (
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	file_id    INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (user_id, file_id)
);

CREATE TABLE tags (
	id      INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name    TEXT NOT NULL COLLATE NOCASE,
	UNIQUE (user_id, name)
);

CREATE TABLE file_tags (
	file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
	tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	PRIMARY KEY (file_id, tag_id)
);

CREATE TABLE recent (
	user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	file_id     INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
	accessed_at INTEGER NOT NULL,
	PRIMARY KEY (user_id, file_id)
);
CREATE INDEX recent_user ON recent(user_id, accessed_at);

CREATE TABLE trash (
	id         INTEGER PRIMARY KEY,
	space_id   INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	orig_path  TEXT NOT NULL,
	name       TEXT NOT NULL,
	is_dir     INTEGER NOT NULL,
	size       INTEGER NOT NULL DEFAULT 0,
	deleted_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
	deleted_at INTEGER NOT NULL
);
CREATE INDEX trash_space ON trash(space_id);

CREATE TABLE shares (
	id             INTEGER PRIMARY KEY,
	owner_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	space_id       INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	path           TEXT NOT NULL,
	is_dir         INTEGER NOT NULL,
	target_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	can_write      INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL,
	UNIQUE (space_id, path, target_user_id)
);
CREATE INDEX shares_target ON shares(target_user_id);

CREATE TABLE links (
	id            INTEGER PRIMARY KEY,
	token         TEXT NOT NULL UNIQUE,
	owner_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	space_id      INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
	path          TEXT NOT NULL,
	is_dir        INTEGER NOT NULL,
	kind          TEXT NOT NULL DEFAULT 'download', -- download | upload
	password_hash TEXT NOT NULL DEFAULT '',
	expires_at    INTEGER NOT NULL DEFAULT 0,
	max_downloads INTEGER NOT NULL DEFAULT 0,
	downloads     INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);

CREATE TABLE notifications (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind       TEXT NOT NULL,
	data       TEXT NOT NULL DEFAULT '{}',
	read       INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX notifications_user ON notifications(user_id, id);

CREATE TABLE activity (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
	actor      TEXT NOT NULL DEFAULT '',
	space_id   INTEGER REFERENCES spaces(id) ON DELETE CASCADE,
	action     TEXT NOT NULL,
	path       TEXT NOT NULL DEFAULT '',
	details    TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX activity_space ON activity(space_id, id);

CREATE TABLE filetypes (
	ext      TEXT PRIMARY KEY,
	mime     TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL DEFAULT 'other',
	viewer   TEXT NOT NULL DEFAULT 'download',
	auto     INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE uploads (
	id           TEXT PRIMARY KEY,
	user_id      INTEGER REFERENCES users(id) ON DELETE CASCADE,
	link_id      INTEGER REFERENCES links(id) ON DELETE CASCADE,
	space_id     INTEGER REFERENCES spaces(id) ON DELETE CASCADE,
	path         TEXT NOT NULL,
	size         INTEGER NOT NULL,
	offset       INTEGER NOT NULL DEFAULT 0,
	sha256       TEXT NOT NULL DEFAULT '',
	hash_state   BLOB,
	mtime        INTEGER NOT NULL DEFAULT 0,
	conflict     TEXT NOT NULL DEFAULT 'rename',
	root_idx     INTEGER NOT NULL DEFAULT 0,
	vault_parent INTEGER,
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL
);

CREATE TABLE vault_items (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	parent_id  INTEGER NOT NULL DEFAULT 0,
	is_dir     INTEGER NOT NULL,
	name_enc   BLOB NOT NULL,
	size       INTEGER NOT NULL DEFAULT 0,
	stored     TEXT NOT NULL DEFAULT '',
	mtime      INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX vault_items_parent ON vault_items(user_id, parent_id);

CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`,
}
