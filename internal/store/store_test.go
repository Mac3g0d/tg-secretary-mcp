package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestOpen(t *testing.T) {
	t.Run("creates directory and 0600 file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("file modes are not meaningful on Windows")
		}
		path := filepath.Join(t.TempDir(), "nested", "data", "t.db")
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer s.Close()

		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat db: %v", err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("file mode = %o, want 600", got)
		}
		di, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if got := di.Mode().Perm(); got != 0o700 {
			t.Errorf("dir mode = %o, want 700", got)
		}
	})

	t.Run("reopen keeps data", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "t.db")
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := s.SetMeta(ctx, "k", "v"); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if got, _ := s.Meta(ctx, "k"); got != "v" {
			t.Errorf("Meta = %q, want v", got)
		}
	})
}

func TestClampLimit(t *testing.T) {
	tests := []struct {
		name      string
		giveLimit int
		giveDef   int
		want      int
	}{
		{"zero uses default", 0, 50, 50},
		{"negative uses default", -3, 20, 20},
		{"within range", 10, 50, 10},
		{"at max", MaxLimit, 50, MaxLimit},
		{"above max", MaxLimit + 1, 50, MaxLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClampLimit(tt.giveLimit, tt.giveDef); got != tt.want {
				t.Errorf("ClampLimit(%d, %d) = %d, want %d",
					tt.giveLimit, tt.giveDef, got, tt.want)
			}
		})
	}
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	empty, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Chats != 0 || empty.Messages != 0 || !empty.Oldest.IsZero() || !empty.Newest.IsZero() {
		t.Errorf("empty stats = %+v", empty)
	}

	mustUpsertChat(t, s, Chat{ID: 1, Type: "private", Title: "A"})
	mustUpsertChat(t, s, Chat{ID: 2, Type: "group", Title: "B"})
	mustSave(t, s, Message{ChatID: 1, ID: 1, Date: _testBase, Text: "one"})
	mustSave(t, s, Message{ChatID: 2, ID: 1, Date: _testBase.Add(time.Hour), Text: "two"})

	got, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Chats != 2 || got.Messages != 2 {
		t.Errorf("counts = %d chats, %d messages; want 2, 2", got.Chats, got.Messages)
	}
	if !got.Oldest.Equal(_testBase) || !got.Newest.Equal(_testBase.Add(time.Hour)) {
		t.Errorf("range = %v..%v", got.Oldest, got.Newest)
	}
	if got.SizeBytes <= 0 {
		t.Errorf("SizeBytes = %d, want > 0", got.SizeBytes)
	}
}

func TestCursor(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, ok, err := s.Cursor(ctx, "c"); err != nil || ok {
		t.Fatalf("missing cursor: ok=%v err=%v", ok, err)
	}

	tests := []struct {
		name string
		give int64
	}{
		{"first set", 5},
		{"overwrite", 42},
		{"back to zero", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.SetCursor(ctx, "c", tt.give); err != nil {
				t.Fatal(err)
			}
			got, ok, err := s.Cursor(ctx, "c")
			if err != nil || !ok || got != tt.give {
				t.Errorf("Cursor = %d, %v, %v; want %d, true, nil", got, ok, err, tt.give)
			}
		})
	}

	if _, ok, _ := s.Cursor(ctx, "other"); ok {
		t.Error("cursors must be independent per name")
	}
}

func TestMaxPK(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if got, err := s.MaxPK(ctx); err != nil || got != 0 {
		t.Fatalf("empty MaxPK = %d, %v", got, err)
	}
	mustUpsertChat(t, s, Chat{ID: 1})
	mustSave(t, s, Message{ChatID: 1, ID: 1, Date: _testBase})
	mustSave(t, s, Message{ChatID: 1, ID: 2, Date: _testBase})
	if got, err := s.MaxPK(ctx); err != nil || got != 2 {
		t.Errorf("MaxPK = %d, %v; want 2", got, err)
	}
}

func TestMeta(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if got, err := s.Meta(ctx, "missing"); err != nil || got != "" {
		t.Fatalf("missing Meta = %q, %v", got, err)
	}

	tests := []struct {
		name      string
		giveKey   string
		giveValue string
	}{
		{"new key", "a", "1"},
		{"overwrite", "a", "2"},
		{"empty value", "b", ""},
		{"unicode", "ключ", "значение"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.SetMeta(ctx, tt.giveKey, tt.giveValue); err != nil {
				t.Fatal(err)
			}
			got, err := s.Meta(ctx, tt.giveKey)
			if err != nil || got != tt.giveValue {
				t.Errorf("Meta(%q) = %q, %v; want %q", tt.giveKey, got, err, tt.giveValue)
			}
		})
	}
}

func TestBusinessOwner(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	tests := []struct {
		name       string
		giveSave   *int64 // nil: do not save
		giveLookup string
		want       int64
	}{
		{"unknown connection", nil, "nope", 0},
		{"saved connection", ptr(int64(77)), "bc1", 77},
		{"updated owner", ptr(int64(88)), "bc1", 88},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.giveSave != nil {
				if err := s.SaveBusinessConnection(ctx, "bc1", *tt.giveSave, true); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.BusinessOwner(ctx, tt.giveLookup)
			if err != nil || got != tt.want {
				t.Errorf("BusinessOwner(%q) = %d, %v; want %d", tt.giveLookup, got, err, tt.want)
			}
		})
	}
}
