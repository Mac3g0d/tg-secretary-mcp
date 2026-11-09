package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// _maxAmbiguousNames bounds how many candidates an ambiguity error lists.
const _maxAmbiguousNames = 10

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
	Chat         Chat
	MessageCount int64
	LastDate     time.Time
	LastText     string
}

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
	if err != nil {
		return fmt.Errorf("upsert chat %d: %w", c.ID, err)
	}
	return nil
}

// Chats lists chats ordered by most recent message. filter matches a part of
// the title or username (case-insensitive); chatType filters by exact type.
func (s *Store) Chats(
	ctx context.Context, filter, chatType string, limit int,
) ([]ChatSummary, error) {
	return s.chats(ctx, filter, chatType, ClampLimit(limit, _defaultLimit))
}

// chats is Chats without limit clamping; limit <= 0 means unlimited.
func (s *Store) chats(
	ctx context.Context, filter, chatType string, limit int,
) ([]ChatSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.type, c.title, c.username, c.business_connection_id,
		       (SELECT COUNT(*) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0),
		       COALESCE(l.date, 0), COALESCE(l.text, '')
		FROM chats c
		LEFT JOIN messages l ON l.pk = (
		    SELECT pk FROM messages WHERE chat_id = c.id AND deleted_at = 0 ORDER BY date DESC, pk DESC LIMIT 1)
		ORDER BY COALESCE(l.date, 0) DESC, c.id`)
	if err != nil {
		return nil, fmt.Errorf("query chats: %w", err)
	}
	defer rows.Close()

	filter = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(filter), "@"))

	var out []ChatSummary
	for (limit <= 0 || len(out) < limit) && rows.Next() {
		var (
			c    ChatSummary
			last int64
		)
		err := rows.Scan(&c.Chat.ID, &c.Chat.Type, &c.Chat.Title, &c.Chat.Username,
			&c.Chat.BusinessConnectionID, &c.MessageCount, &last, &c.LastText)
		if err != nil {
			return nil, fmt.Errorf("scan chat: %w", err)
		}
		c.LastDate = timeOrZero(last)

		if chatType != "" && c.Chat.Type != chatType {
			continue
		}
		// Filtering happens here because SQLite's LIKE is case-insensitive
		// for ASCII only.
		if filter != "" && !strings.Contains(strings.ToLower(c.Chat.Title), filter) &&
			!strings.Contains(strings.ToLower(c.Chat.Username), filter) {
			continue
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chats: %w", err)
	}
	return out, nil
}

// ResolveChat finds a chat by numeric ID, @username, or (part of) its title.
func (s *Store) ResolveChat(ctx context.Context, ref string) (Chat, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Chat{}, ErrChatRequired
	}

	all, err := s.chats(ctx, "", "", 0)
	if err != nil {
		return Chat{}, err
	}

	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, c := range all {
			if c.Chat.ID == id {
				return c.Chat, nil
			}
		}
		return Chat{}, &described{
			msg: fmt.Sprintf("chat %d is not in the archive", id),
			err: ErrChatNotFound,
		}
	}

	explicitUsername := strings.HasPrefix(ref, "@")
	needle := strings.ToLower(strings.TrimPrefix(ref, "@"))

	var partial []Chat
	for _, c := range all {
		title, username := strings.ToLower(c.Chat.Title), strings.ToLower(c.Chat.Username)
		if username == needle || (title == needle && !explicitUsername) {
			return c.Chat, nil
		}
		if !explicitUsername && strings.Contains(title, needle) {
			partial = append(partial, c.Chat)
		}
	}

	switch len(partial) {
	case 0:
		return Chat{}, &described{
			msg: fmt.Sprintf("no chat matches %q; use list_chats to see known chats", ref),
			err: ErrChatNotFound,
		}
	case 1:
		return partial[0], nil
	}

	names := make([]string, 0, min(len(partial), _maxAmbiguousNames+1))
	for i, c := range partial {
		if i == _maxAmbiguousNames {
			names = append(names, "…")
			break
		}
		names = append(names, fmt.Sprintf("%q (id %d)", c.Title, c.ID))
	}
	return Chat{}, &described{
		msg: fmt.Sprintf("%q matches several chats, pass the id instead: %s",
			ref, strings.Join(names, ", ")),
		err: ErrAmbiguousChat,
	}
}
