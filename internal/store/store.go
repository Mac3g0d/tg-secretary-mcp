// Package store persists Telegram chats and messages in SQLite and provides
// the queries behind the MCP tools.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo), ships with FTS5
)

const _schema = `
CREATE TABLE IF NOT EXISTS chats (
    id                     INTEGER PRIMARY KEY,
    type                   TEXT    NOT NULL DEFAULT '',
    title                  TEXT    NOT NULL DEFAULT '',
    username               TEXT    NOT NULL DEFAULT '',
    business_connection_id TEXT    NOT NULL DEFAULT '',
    updated_at             INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
    pk              INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id         INTEGER NOT NULL REFERENCES chats(id),
    id              INTEGER NOT NULL,
    date            INTEGER NOT NULL,
    sender_id       INTEGER NOT NULL DEFAULT 0,
    sender_name     TEXT    NOT NULL DEFAULT '',
    sender_username TEXT    NOT NULL DEFAULT '',
    text            TEXT    NOT NULL DEFAULT '',
    media_type      TEXT    NOT NULL DEFAULT '',
    reply_to_id     INTEGER NOT NULL DEFAULT 0,
    is_outgoing     INTEGER NOT NULL DEFAULT 0,
    edited_at       INTEGER NOT NULL DEFAULT 0,
    deleted_at      INTEGER NOT NULL DEFAULT 0,
    saved_at        INTEGER NOT NULL,
    UNIQUE (chat_id, id)
);
CREATE INDEX IF NOT EXISTS messages_chat_date ON messages (chat_id, date);
CREATE INDEX IF NOT EXISTS messages_date ON messages (date);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5 (
    text, sender_name,
    content='messages', content_rowid='pk',
    tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts (rowid, text, sender_name) VALUES (new.pk, new.text, new.sender_name);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts (messages_fts, rowid, text, sender_name)
    VALUES ('delete', old.pk, old.text, old.sender_name);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE OF text, sender_name ON messages BEGIN
    INSERT INTO messages_fts (messages_fts, rowid, text, sender_name)
    VALUES ('delete', old.pk, old.text, old.sender_name);
    INSERT INTO messages_fts (rowid, text, sender_name) VALUES (new.pk, new.text, new.sender_name);
END;

CREATE TABLE IF NOT EXISTS business_connections (
    id      TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS cursors (
    name       TEXT PRIMARY KEY,
    last_pk    INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// _migrations upgrade a database created by an earlier version. Entry i moves
// the schema from version i to i+1; the version is kept in PRAGMA
// user_version. Never edit or reorder published entries, only append.
var _migrations = []string{
	// 1: forwarded messages and history imported from chat exports.
	`ALTER TABLE messages ADD COLUMN forward_from TEXT NOT NULL DEFAULT '';
	 ALTER TABLE messages ADD COLUMN forward_from_username TEXT NOT NULL DEFAULT '';
	 ALTER TABLE messages ADD COLUMN forward_date INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE messages ADD COLUMN imported INTEGER NOT NULL DEFAULT 0;`,
	// 2: chat profile pictures. A row also records that a chat was checked
	// and has no picture (data IS NULL), so it is not asked about again soon.
	`CREATE TABLE chat_photos (
	     chat_id      INTEGER PRIMARY KEY REFERENCES chats(id),
	     unique_id    TEXT    NOT NULL DEFAULT '',
	     content_type TEXT    NOT NULL DEFAULT '',
	     data         BLOB,
	     checked_at   INTEGER NOT NULL
	 );`,
}

const (
	// MaxLimit caps how many rows a single query may return.
	MaxLimit = 500

	_defaultLimit = 50
)

// Store is a handle to the SQLite database. It is safe for concurrent use.
type Store struct {
	db   *sql.DB
	path string
}

// Stats summarises the database contents.
type Stats struct {
	Chats, Messages int64
	Oldest, Newest  time.Time
	SizeBytes       int64
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	pragmas := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
	} {
		pragmas.Add("_pragma", p)
	}

	db, err := sql.Open("sqlite", "file:"+path+"?"+pragmas.Encode())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if _, err := db.Exec(_schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	// Message history is private: keep the file readable by the owner only.
	// Best effort; the data directory itself is already 0700.
	_ = os.Chmod(path, 0o600)

	return &Store{db: db, path: path}, nil
}

// migrate applies the pending entries of _migrations. It holds a write lock
// for the whole upgrade, so several processes opening the same database at
// once (for example "bot" and "stdio") cannot apply a migration twice.
func migrate(db *sql.DB) (err error) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(_migrations) {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d)",
			version, len(_migrations))
	}
	for i := version; i < len(_migrations); i++ {
		if _, err := conn.ExecContext(ctx, _migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	// PRAGMA does not accept bound parameters.
	setVersion := "PRAGMA user_version = " + strconv.Itoa(len(_migrations))
	if _, err := conn.ExecContext(ctx, setVersion); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Stats returns database totals.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var (
		st             Stats
		oldest, newest int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM chats), COUNT(*), COALESCE(MIN(date), 0), COALESCE(MAX(date), 0)
		FROM messages`).Scan(&st.Chats, &st.Messages, &oldest, &newest)
	if err != nil {
		return st, fmt.Errorf("query stats: %w", err)
	}
	st.Oldest, st.Newest = timeOrZero(oldest), timeOrZero(newest)
	if fi, err := os.Stat(s.path); err == nil {
		st.SizeBytes = fi.Size()
	}
	return st, nil
}

// ClampLimit applies a default and the MaxLimit ceiling to a requested limit.
func ClampLimit(limit, def int) int {
	if limit <= 0 {
		return def
	}
	return min(limit, MaxLimit)
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
