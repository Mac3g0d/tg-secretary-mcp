package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Photo is a chat's profile picture.
type Photo struct {
	// UniqueID identifies this version of the picture.
	UniqueID string
	// ContentType is the image media type, for example "image/jpeg".
	ContentType string
	Data        []byte
}

// PhotoCheck is a chat whose picture should be looked up again.
type PhotoCheck struct {
	ChatID int64
	// UniqueID is the version currently stored, or "" if there is none.
	UniqueID string
}

// PhotosToCheck returns up to limit chats whose picture has never been
// looked up or was last looked up before the given time, the most overdue
// first.
func (s *Store) PhotosToCheck(
	ctx context.Context, before time.Time, limit int,
) ([]PhotoCheck, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, COALESCE(CASE WHEN p.data IS NOT NULL THEN p.unique_id END, '')
		FROM chats c
		LEFT JOIN chat_photos p ON p.chat_id = c.id
		WHERE p.chat_id IS NULL OR p.checked_at < ?
		ORDER BY COALESCE(p.checked_at, 0), c.id
		LIMIT ?`, before.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("query chats to check photos: %w", err)
	}
	defer rows.Close()

	var out []PhotoCheck
	for rows.Next() {
		var c PhotoCheck
		if err := rows.Scan(&c.ChatID, &c.UniqueID); err != nil {
			return nil, fmt.Errorf("scan photo check: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate photo checks: %w", err)
	}
	return out, nil
}

// SavePhoto stores the chat's current picture, replacing any previous one.
func (s *Store) SavePhoto(ctx context.Context, chatID int64, p Photo) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_photos (chat_id, unique_id, content_type, data, checked_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (chat_id) DO UPDATE SET
		    unique_id = excluded.unique_id, content_type = excluded.content_type,
		    data = excluded.data, checked_at = excluded.checked_at`,
		chatID, p.UniqueID, p.ContentType, p.Data, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("save photo of chat %d: %w", chatID, err)
	}
	return nil
}

// ClearPhoto records that the chat has no picture and drops a stored one.
func (s *Store) ClearPhoto(ctx context.Context, chatID int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_photos (chat_id, checked_at) VALUES (?, ?)
		ON CONFLICT (chat_id) DO UPDATE SET
		    unique_id = '', content_type = '', data = NULL, checked_at = excluded.checked_at`,
		chatID, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("clear photo of chat %d: %w", chatID, err)
	}
	return nil
}

// TouchPhoto records that the chat's picture was looked up just now without
// changing what is stored. It is used when the picture is unchanged or the
// lookup failed.
func (s *Store) TouchPhoto(ctx context.Context, chatID int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_photos (chat_id, checked_at) VALUES (?, ?)
		ON CONFLICT (chat_id) DO UPDATE SET checked_at = excluded.checked_at`,
		chatID, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("touch photo of chat %d: %w", chatID, err)
	}
	return nil
}

// Photo returns the chat's stored picture. The boolean is false when there
// is none.
func (s *Store) Photo(ctx context.Context, chatID int64) (Photo, bool, error) {
	var p Photo
	err := s.db.QueryRowContext(ctx, `
		SELECT unique_id, content_type, data FROM chat_photos
		WHERE chat_id = ? AND data IS NOT NULL`, chatID).
		Scan(&p.UniqueID, &p.ContentType, &p.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return Photo{}, false, nil
	}
	if err != nil {
		return Photo{}, false, fmt.Errorf("load photo of chat %d: %w", chatID, err)
	}
	return p, true, nil
}
