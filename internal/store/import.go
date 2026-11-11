package store

import (
	"context"
	"fmt"
	"time"
)

// ImportMessages adds historical messages of one chat, for example from a
// chat export, in a single transaction and reports how many were new.
//
// Unlike SaveMessage it never changes what is already stored: an existing
// chat keeps its live title and username, and a message whose ID is already
// present in the chat is skipped. That makes a repeated import harmless.
// Imported rows are flagged so Query.LiveOnly can leave them out.
func (s *Store) ImportMessages(ctx context.Context, chat Chat, msgs []Message) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin import: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful commit

	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO chats (id, type, title, username, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`,
		chat.ID, chat.Type, chat.Title, chat.Username, now)
	if err != nil {
		return 0, fmt.Errorf("import chat %d: %w", chat.ID, err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO messages (chat_id, id, date, sender_id, sender_name, sender_username, text,
		                      media_type, reply_to_id, is_outgoing, edited_at, saved_at,
		                      forward_from, forward_from_username, forward_date, imported)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT (chat_id, id) DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("prepare import: %w", err)
	}
	defer stmt.Close()

	var inserted int64
	for _, m := range msgs {
		res, err := stmt.ExecContext(ctx,
			chat.ID, m.ID, m.Date.Unix(), m.SenderID, m.SenderName, m.SenderUsername, m.Text,
			m.MediaType, m.ReplyToID, m.Outgoing, unixOrZero(m.EditedAt), now,
			m.ForwardFrom, m.ForwardFromUsername, unixOrZero(m.ForwardDate))
		if err != nil {
			return 0, fmt.Errorf("import message %d into chat %d: %w", m.ID, chat.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("count imported rows: %w", err)
		}
		inserted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit import: %w", err)
	}
	return inserted, nil
}
