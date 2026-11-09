// Package store persists Telegram chats and messages in SQLite and provides
// the queries behind the MCP tools.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo), ships with FTS5
)

const schema = `
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

// MaxLimit caps how many rows a single query may return.
const MaxLimit = 500

// Store is a handle to the SQLite database. It is safe for concurrent use.
type Store struct {
	db   *sql.DB
	path string
}

// Chat is a Telegram chat known to the bot.
type Chat struct {
	ID                   int64
	Type                 string // private, group, supergroup, channel
	Title                string
	Username             string
	BusinessConnectionID string
}

// ChatSummary is a chat with aggregate information about its stored messages.
type ChatSummary struct {
	Chat
	MessageCount int64
	LastDate     time.Time
	LastText     string
}

// Message is a stored message. PK is the local insertion order, ID is the
// Telegram message_id (unique only within a chat).
type Message struct {
	PK             int64
	ChatID         int64
	ChatTitle      string
	ID             int64
	Date           time.Time
	SenderID       int64
	SenderName     string
	SenderUsername string
	Text           string
	MediaType      string
	ReplyToID      int64
	Outgoing       bool
	EditedAt       time.Time
	DeletedAt      time.Time
}

// Query filters stored messages. Zero values mean "no filter".
type Query struct {
	ChatID         *int64
	Text           string // full-text search over message text
	Sender         string // "@username" (exact) or part of the display name
	Since, Until   time.Time
	AfterPK        int64
	IncomingOnly   bool
	IncludeDeleted bool
	Limit          int
	Ascending      bool // oldest first; default is newest first
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
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		pragmas.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+pragmas.Encode())
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Message history is private: keep the file readable by the owner only.
	_ = os.Chmod(path, 0o600)
	return &Store{db: db, path: path}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// UpsertChat inserts or refreshes a chat. An empty BusinessConnectionID keeps
// the previously stored one.
func (s *Store) UpsertChat(ctx context.Context, c Chat) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats (id, type, title, username, business_connection_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
		    type = excluded.type, title = excluded.title, username = excluded.username,
		    business_connection_id = CASE WHEN excluded.business_connection_id != ''
		        THEN excluded.business_connection_id ELSE chats.business_connection_id END,
		    updated_at = excluded.updated_at`,
		c.ID, c.Type, c.Title, c.Username, c.BusinessConnectionID, time.Now().Unix())
	return err
}

// SaveMessage inserts a message or, if it is already stored, applies an edit.
// The chat must exist. PK and ChatTitle of m are ignored.
func (s *Store) SaveMessage(ctx context.Context, m Message) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (chat_id, id, date, sender_id, sender_name, sender_username, text,
		                      media_type, reply_to_id, is_outgoing, edited_at, saved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chat_id, id) DO UPDATE SET
		    text = excluded.text, media_type = excluded.media_type, edited_at = excluded.edited_at,
		    sender_name = excluded.sender_name, sender_username = excluded.sender_username`,
		m.ChatID, m.ID, m.Date.Unix(), m.SenderID, m.SenderName, m.SenderUsername, m.Text,
		m.MediaType, m.ReplyToID, m.Outgoing, unixOrZero(m.EditedAt), time.Now().Unix())
	return err
}

// MarkDeleted flags messages as deleted (they stay searchable with
// IncludeDeleted) and reports how many rows changed.
func (s *Store) MarkDeleted(ctx context.Context, chatID int64, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := []any{time.Now().Unix(), chatID}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE messages SET deleted_at = ? WHERE chat_id = ? AND deleted_at = 0 AND id IN (`+
			placeholders(len(ids))+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const messageColumns = `m.pk, m.chat_id, c.title, m.id, m.date, m.sender_id, m.sender_name,
	m.sender_username, m.text, m.media_type, m.reply_to_id, m.is_outgoing, m.edited_at, m.deleted_at`

// Messages returns the messages matching q.
func (s *Store) Messages(ctx context.Context, q Query) ([]Message, error) {
	from := `FROM messages m JOIN chats c ON c.id = m.chat_id`
	var where []string
	var args []any

	var match []string
	if t := ftsTerms(q.Text); t != "" {
		match = append(match, "{text} : ("+t+")")
	}
	if name, ok := strings.CutPrefix(q.Sender, "@"); ok {
		where = append(where, "m.sender_username = ? COLLATE NOCASE")
		args = append(args, name)
	} else if t := ftsTerms(q.Sender); t != "" {
		match = append(match, "{sender_name} : ("+t+")")
	}
	if len(match) > 0 {
		from += ` JOIN messages_fts ON messages_fts.rowid = m.pk`
		where = append(where, "messages_fts MATCH ?")
		args = append(args, strings.Join(match, " AND "))
	}
	if q.ChatID != nil {
		where = append(where, "m.chat_id = ?")
		args = append(args, *q.ChatID)
	}
	if !q.Since.IsZero() {
		where = append(where, "m.date >= ?")
		args = append(args, q.Since.Unix())
	}
	if !q.Until.IsZero() {
		where = append(where, "m.date <= ?")
		args = append(args, q.Until.Unix())
	}
	if q.AfterPK > 0 {
		where = append(where, "m.pk > ?")
		args = append(args, q.AfterPK)
	}
	if q.IncomingOnly {
		where = append(where, "m.is_outgoing = 0")
	}
	if !q.IncludeDeleted {
		where = append(where, "m.deleted_at = 0")
	}

	query := "SELECT " + messageColumns + " " + from
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if q.Ascending {
		query += " ORDER BY m.pk ASC"
	} else {
		query += " ORDER BY m.date DESC, m.pk DESC"
	}
	query += " LIMIT ?"
	args = append(args, ClampLimit(q.Limit, 50))
	return s.queryMessages(ctx, query, args...)
}

// Context returns the message with the given Telegram ID together with up to
// `before` earlier and `after` later messages of the same chat, oldest first.
func (s *Store) Context(ctx context.Context, chatID, messageID int64, before, after int) ([]Message, error) {
	base := "SELECT " + messageColumns + " FROM messages m JOIN chats c ON c.id = m.chat_id WHERE m.chat_id = ? AND "
	prev, err := s.queryMessages(ctx, base+"m.id < ? ORDER BY m.id DESC LIMIT ?", chatID, messageID, before)
	if err != nil {
		return nil, err
	}
	rest, err := s.queryMessages(ctx, base+"m.id >= ? ORDER BY m.id ASC LIMIT ?", chatID, messageID, after+1)
	if err != nil {
		return nil, err
	}
	if len(rest) == 0 || rest[0].ID != messageID {
		return nil, fmt.Errorf("message %d not found in chat %d", messageID, chatID)
	}
	reverse(prev)
	return append(prev, rest...), nil
}

func (s *Store) queryMessages(ctx context.Context, query string, args ...any) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var date, edited, deleted int64
		if err := rows.Scan(&m.PK, &m.ChatID, &m.ChatTitle, &m.ID, &date, &m.SenderID, &m.SenderName,
			&m.SenderUsername, &m.Text, &m.MediaType, &m.ReplyToID, &m.Outgoing, &edited, &deleted); err != nil {
			return nil, err
		}
		m.Date, m.EditedAt, m.DeletedAt = timeOrZero(date), timeOrZero(edited), timeOrZero(deleted)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Chats lists chats ordered by most recent message. filter matches a part of
// the title or username (case-insensitive); chatType filters by exact type.
func (s *Store) Chats(ctx context.Context, filter, chatType string, limit int) ([]ChatSummary, error) {
	return s.chats(ctx, filter, chatType, ClampLimit(limit, 50))
}

func (s *Store) chats(ctx context.Context, filter, chatType string, limit int) ([]ChatSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.type, c.title, c.username, c.business_connection_id,
		       (SELECT COUNT(*) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0),
		       COALESCE(l.date, 0), COALESCE(l.text, '')
		FROM chats c
		LEFT JOIN messages l ON l.pk = (
		    SELECT pk FROM messages WHERE chat_id = c.id AND deleted_at = 0 ORDER BY date DESC, pk DESC LIMIT 1)
		ORDER BY COALESCE(l.date, 0) DESC, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	filter = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(filter), "@"))
	var out []ChatSummary
	for (limit <= 0 || len(out) < limit) && rows.Next() {
		var c ChatSummary
		var last int64
		if err := rows.Scan(&c.ID, &c.Type, &c.Title, &c.Username, &c.BusinessConnectionID,
			&c.MessageCount, &last, &c.LastText); err != nil {
			return nil, err
		}
		c.LastDate = timeOrZero(last)
		if chatType != "" && c.Type != chatType {
			continue
		}
		// Filtering happens here because SQLite's LIKE is case-insensitive for ASCII only.
		if filter != "" && !strings.Contains(strings.ToLower(c.Title), filter) &&
			!strings.Contains(strings.ToLower(c.Username), filter) {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveChat finds a chat by numeric ID, @username, or (part of) its title.
func (s *Store) ResolveChat(ctx context.Context, ref string) (Chat, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Chat{}, errors.New("chat is required")
	}
	all, err := s.chats(ctx, "", "", 0)
	if err != nil {
		return Chat{}, err
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, c := range all {
			if c.ID == id {
				return c.Chat, nil
			}
		}
		return Chat{}, fmt.Errorf("chat %d is not in the archive", id)
	}
	needle := strings.ToLower(strings.TrimPrefix(ref, "@"))
	var partial []Chat
	for _, c := range all {
		title, username := strings.ToLower(c.Title), strings.ToLower(c.Username)
		if username == needle || (title == needle && !strings.HasPrefix(ref, "@")) {
			return c.Chat, nil
		}
		if !strings.HasPrefix(ref, "@") && strings.Contains(title, needle) {
			partial = append(partial, c.Chat)
		}
	}
	switch len(partial) {
	case 0:
		return Chat{}, fmt.Errorf("no chat matches %q; use list_chats to see known chats", ref)
	case 1:
		return partial[0], nil
	}
	var names []string
	for i, c := range partial {
		if i == 10 {
			names = append(names, "…")
			break
		}
		names = append(names, fmt.Sprintf("%q (id %d)", c.Title, c.ID))
	}
	return Chat{}, fmt.Errorf("%q matches several chats, pass the id instead: %s", ref, strings.Join(names, ", "))
}

// Cursor returns the last acknowledged message PK for a consumer.
func (s *Store) Cursor(ctx context.Context, name string) (pk int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT last_pk FROM cursors WHERE name = ?`, name).Scan(&pk)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return pk, err == nil, err
}

// SetCursor stores the last acknowledged message PK for a consumer.
func (s *Store) SetCursor(ctx context.Context, name string, pk int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cursors (name, last_pk, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET last_pk = excluded.last_pk, updated_at = excluded.updated_at`,
		name, pk, time.Now().Unix())
	return err
}

// MaxPK returns the PK of the most recently stored message (0 if none).
func (s *Store) MaxPK(ctx context.Context) (pk int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(pk), 0) FROM messages`).Scan(&pk)
	return pk, err
}

// SaveBusinessConnection records which user a business connection belongs to.
func (s *Store) SaveBusinessConnection(ctx context.Context, id string, userID int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO business_connections (id, user_id, enabled) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET user_id = excluded.user_id, enabled = excluded.enabled`,
		id, userID, enabled)
	return err
}

// BusinessOwner returns the user ID owning a business connection (0 if unknown).
func (s *Store) BusinessOwner(ctx context.Context, id string) (userID int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT user_id FROM business_connections WHERE id = ?`, id).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return userID, err
}

// Meta reads a key from the key-value table ("" if absent).
func (s *Store) Meta(ctx context.Context, key string) (value string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetMeta writes a key to the key-value table.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Stats returns database totals.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	var oldest, newest int64
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM chats), COUNT(*), COALESCE(MIN(date), 0), COALESCE(MAX(date), 0)
		FROM messages`).Scan(&st.Chats, &st.Messages, &oldest, &newest)
	if err != nil {
		return st, err
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

// ftsTerms turns free-form user input into a safe FTS5 expression: every word
// becomes a quoted prefix term and all words must match.
func ftsTerms(input string) string {
	var terms []string
	for _, w := range strings.Fields(input) {
		w = strings.ReplaceAll(w, `"`, "")
		if w != "" {
			terms = append(terms, `"`+w+`"*`)
		}
	}
	return strings.Join(terms, " ")
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
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

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
