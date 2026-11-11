package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

const _messageColumns = `m.pk, m.chat_id, c.title, m.id, m.date, m.sender_id, m.sender_name,
	m.sender_username, m.text, m.media_type, m.reply_to_id, m.is_outgoing, m.edited_at, m.deleted_at,
	m.forward_from, m.forward_from_username, m.forward_date`

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

	// ForwardFrom is the display name of the original author when the
	// message was forwarded; ForwardDate is when the original was sent.
	// A forwarded message always has a non-zero ForwardDate.
	ForwardFrom         string
	ForwardFromUsername string
	ForwardDate         time.Time
}

// Forwarded reports whether the message was forwarded from elsewhere.
func (m Message) Forwarded() bool {
	return !m.ForwardDate.IsZero() || m.ForwardFrom != ""
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
	ForwardedOnly  bool // only messages forwarded from elsewhere
	LiveOnly       bool // skip history loaded by ImportMessages
	Limit          int
	Offset         int  // rows to skip, for paging
	Ascending      bool // oldest first; default is newest first
}

// SaveMessage inserts a message or, if it is already stored, applies an edit.
// The chat must exist. PK and ChatTitle of m are ignored.
func (s *Store) SaveMessage(ctx context.Context, m Message) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (chat_id, id, date, sender_id, sender_name, sender_username, text,
		                      media_type, reply_to_id, is_outgoing, edited_at, saved_at,
		                      forward_from, forward_from_username, forward_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chat_id, id) DO UPDATE SET
		    text = excluded.text, media_type = excluded.media_type, edited_at = excluded.edited_at,
		    sender_name = excluded.sender_name, sender_username = excluded.sender_username`,
		m.ChatID, m.ID, m.Date.Unix(), m.SenderID, m.SenderName, m.SenderUsername, m.Text,
		m.MediaType, m.ReplyToID, m.Outgoing, unixOrZero(m.EditedAt), time.Now().Unix(),
		m.ForwardFrom, m.ForwardFromUsername, unixOrZero(m.ForwardDate))
	if err != nil {
		return fmt.Errorf("save message %d in chat %d: %w", m.ID, m.ChatID, err)
	}
	return nil
}

// MarkDeleted flags messages as deleted (they stay searchable with
// IncludeDeleted) and reports how many rows changed.
func (s *Store) MarkDeleted(ctx context.Context, chatID int64, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	args := make([]any, 0, len(ids)+2)
	args = append(args, time.Now().Unix(), chatID)
	for _, id := range ids {
		args = append(args, id)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE messages SET deleted_at = ? WHERE chat_id = ? AND deleted_at = 0 AND id IN (`+
			placeholders(len(ids))+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("mark messages deleted in chat %d: %w", chatID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted messages: %w", err)
	}
	return n, nil
}

// Messages returns the messages matching q.
func (s *Store) Messages(ctx context.Context, q Query) ([]Message, error) {
	var (
		from  = `FROM messages m JOIN chats c ON c.id = m.chat_id`
		where []string
		args  []any
		match []string
	)

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
	if q.LiveOnly {
		where = append(where, "m.imported = 0")
	}
	if q.ForwardedOnly {
		where = append(where, "(m.forward_date != 0 OR m.forward_from != '')")
	}
	if !q.IncludeDeleted {
		where = append(where, "m.deleted_at = 0")
	}

	query := "SELECT " + _messageColumns + " " + from
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if q.Ascending {
		query += " ORDER BY m.pk ASC"
	} else {
		query += " ORDER BY m.date DESC, m.pk DESC"
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, ClampLimit(q.Limit, _defaultLimit), max(q.Offset, 0))

	return s.queryMessages(ctx, query, args...)
}

// Context returns the message with the given Telegram ID together with up to
// `before` earlier and `after` later messages of the same chat, oldest first.
func (s *Store) Context(
	ctx context.Context, chatID, messageID int64, before, after int,
) ([]Message, error) {
	const base = "SELECT " + _messageColumns +
		" FROM messages m JOIN chats c ON c.id = m.chat_id WHERE m.chat_id = ? AND "

	prev, err := s.queryMessages(ctx,
		base+"m.id < ? ORDER BY m.id DESC LIMIT ?", chatID, messageID, before)
	if err != nil {
		return nil, err
	}
	rest, err := s.queryMessages(ctx,
		base+"m.id >= ? ORDER BY m.id ASC LIMIT ?", chatID, messageID, after+1)
	if err != nil {
		return nil, err
	}
	if len(rest) == 0 || rest[0].ID != messageID {
		return nil, &described{
			msg: fmt.Sprintf("message %d not found in chat %d", messageID, chatID),
			err: ErrMessageNotFound,
		}
	}

	slices.Reverse(prev)
	return append(prev, rest...), nil
}

func (s *Store) queryMessages(
	ctx context.Context, query string, args ...any,
) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var (
			m                                Message
			date, edited, deleted, forwarded int64
		)
		err := rows.Scan(&m.PK, &m.ChatID, &m.ChatTitle, &m.ID, &date, &m.SenderID,
			&m.SenderName, &m.SenderUsername, &m.Text, &m.MediaType, &m.ReplyToID,
			&m.Outgoing, &edited, &deleted,
			&m.ForwardFrom, &m.ForwardFromUsername, &forwarded)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Date, m.EditedAt, m.DeletedAt = timeOrZero(date), timeOrZero(edited), timeOrZero(deleted)
		m.ForwardDate = timeOrZero(forwarded)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return out, nil
}
