package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SaveBusinessConnection records which user a business connection belongs to.
func (s *Store) SaveBusinessConnection(
	ctx context.Context, id string, userID int64, enabled bool,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO business_connections (id, user_id, enabled) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET user_id = excluded.user_id, enabled = excluded.enabled`,
		id, userID, enabled)
	if err != nil {
		return fmt.Errorf("save business connection: %w", err)
	}
	return nil
}

// BusinessOwner returns the user ID owning a business connection (0 if unknown).
func (s *Store) BusinessOwner(ctx context.Context, id string) (userID int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT user_id FROM business_connections WHERE id = ?`, id).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("query business owner: %w", err)
	}
	return userID, nil
}

// Meta reads a key from the key-value table ("" if absent).
func (s *Store) Meta(ctx context.Context, key string) (value string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query meta %q: %w", key, err)
	}
	return value, nil
}

// SetMeta writes a key to the key-value table.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("save meta %q: %w", key, err)
	}
	return nil
}
