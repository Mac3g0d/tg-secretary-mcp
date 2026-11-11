package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrateFromV010(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	// A database as v0.1.0 left it: the base schema, version 0, some data.
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(_schema); err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		INSERT INTO chats (id, title, updated_at) VALUES (1, 'Old', 0);
		INSERT INTO messages (chat_id, id, date, text, saved_at) VALUES (1, 1, 1700000000, 'до миграции', 0);`)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()

	// Opening twice checks that the upgrade is applied exactly once.
	for i := range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		var version int
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != len(_migrations) {
			t.Errorf("open #%d: schema version = %d, want %d", i+1, version, len(_migrations))
		}
		msgs, err := s.Messages(ctx, Query{})
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		if len(msgs) != 1 || msgs[0].Text != "до миграции" || msgs[0].Forwarded() {
			t.Errorf("open #%d: old message changed: %+v", i+1, msgs)
		}
		s.Close()
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if _, err := Open(path); err == nil {
		t.Fatal("opening a database from a newer version should fail")
	}
}

func TestForwardedMessages(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpsertChat(ctx, Chat{ID: 1, Title: "Chat"}); err != nil {
		t.Fatal(err)
	}

	original := time.Unix(1700000000, 0)
	give := []Message{
		{ChatID: 1, ID: 1, Text: "своё"},
		{ChatID: 1, ID: 2, Text: "чужое", ForwardFrom: "Иван Петров",
			ForwardFromUsername: "ivan", ForwardDate: original},
		{ChatID: 1, ID: 3, Text: "скрытый автор", ForwardFrom: "Аноним", ForwardDate: original},
		{ChatID: 1, ID: 4, Text: "без имени", ForwardDate: original},
	}
	for i, m := range give {
		m.Date = time.Unix(1700001000+int64(i), 0)
		if err := s.SaveMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name    string
		give    Query
		wantIDs []int64
	}{
		{name: "all", give: Query{Ascending: true}, wantIDs: []int64{1, 2, 3, 4}},
		{name: "forwarded only", give: Query{Ascending: true, ForwardedOnly: true}, wantIDs: []int64{2, 3, 4}},
		{name: "forwarded and text", give: Query{ForwardedOnly: true, Text: "чужое"}, wantIDs: []int64{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs, err := s.Messages(ctx, tt.give)
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			for _, m := range msgs {
				got = append(got, m.ID)
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("got ids %v, want %v", got, tt.wantIDs)
			}
			for i := range got {
				if got[i] != tt.wantIDs[i] {
					t.Fatalf("got ids %v, want %v", got, tt.wantIDs)
				}
			}
		})
	}

	msgs, err := s.Context(ctx, 1, 2, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	own, fwd := msgs[0], msgs[1]
	if own.Forwarded() {
		t.Errorf("own message reported as forwarded: %+v", own)
	}
	if !fwd.Forwarded() || fwd.ForwardFrom != "Иван Петров" ||
		fwd.ForwardFromUsername != "ivan" || !fwd.ForwardDate.Equal(original) {
		t.Errorf("forward origin not stored: %+v", fwd)
	}

	// An edit must not erase where the message came from.
	edited := give[1]
	edited.Date, edited.Text = time.Unix(1700001001, 0), "чужое, с правкой"
	edited.ForwardFrom, edited.ForwardFromUsername, edited.ForwardDate = "", "", time.Time{}
	if err := s.SaveMessage(ctx, edited); err != nil {
		t.Fatal(err)
	}
	msgs, err = s.Context(ctx, 1, 2, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Text != "чужое, с правкой" || msgs[0].ForwardFrom != "Иван Петров" {
		t.Errorf("edit lost the forward origin: %+v", msgs[0])
	}
}
