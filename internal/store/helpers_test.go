package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

var _testBase = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustUpsertChat(t *testing.T, s *Store, c Chat) {
	t.Helper()
	if err := s.UpsertChat(context.Background(), c); err != nil {
		t.Fatalf("UpsertChat(%+v): %v", c, err)
	}
}

func mustSave(t *testing.T, s *Store, m Message) {
	t.Helper()
	if err := s.SaveMessage(context.Background(), m); err != nil {
		t.Fatalf("SaveMessage(%+v): %v", m, err)
	}
}

func ptr[T any](v T) *T { return &v }

// ids returns the Telegram message IDs of ms, in order.
func ids(ms []Message) []int64 {
	out := make([]int64, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }
