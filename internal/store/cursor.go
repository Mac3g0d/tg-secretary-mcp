package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Cursor returns the last acknowledged message PK for a consumer.
func (s *Store) Cursor(ctx context.Context, name string) (pk int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT last_pk FROM cursors WHERE name = ?`, name).Scan(&pk)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query cursor %q: %w", name, err)
	}
	return pk, true, nil
}

// SetCursor stores the last acknowledged message PK for a consumer.
func (s *Store) SetCursor(ctx context.Context, name string, pk int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cursors (name, last_pk, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET last_pk = excluded.last_pk, updated_at = excluded.updated_at`,
		name, pk, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("save cursor %q: %w", name, err)
	}
	return nil
}

// MaxPK returns the PK of the most recently stored message (0 if none).
func (s *Store) MaxPK(ctx context.Context) (pk int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(pk), 0) FROM messages`).Scan(&pk)
	if err != nil {
		return 0, fmt.Errorf("query max pk: %w", err)
	}
	return pk, nil
}
