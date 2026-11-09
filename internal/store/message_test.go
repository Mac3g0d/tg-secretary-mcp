package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSaveMessage(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	mustUpsertChat(t, s, Chat{ID: 1, Title: "Chat"})

	orig := Message{
		ChatID: 1, ID: 10, Date: _testBase, SenderID: 5, SenderName: "Bob",
		SenderUsername: "bob", Text: "hello", ReplyToID: 3, Outgoing: true,
	}
	mustSave(t, s, orig)

	got, err := s.Messages(ctx, Query{})
	if err != nil || len(got) != 1 {
		t.Fatalf("Messages: %v, %v", got, err)
	}
	m := got[0]
	if m.ChatTitle != "Chat" || m.Text != "hello" || !m.Outgoing || m.ReplyToID != 3 ||
		m.SenderName != "Bob" || m.SenderUsername != "bob" || !m.Date.Equal(_testBase) ||
		!m.EditedAt.IsZero() || !m.DeletedAt.IsZero() {
		t.Errorf("stored message = %+v", m)
	}

	edit := orig
	edit.Text = "hello (edited)"
	edit.MediaType = "photo"
	edit.EditedAt = _testBase.Add(time.Minute)
	edit.SenderName = "Robert"
	mustSave(t, s, edit)

	got, err = s.Messages(ctx, Query{})
	if err != nil || len(got) != 1 {
		t.Fatalf("after edit Messages: %v, %v", got, err)
	}
	m = got[0]
	if m.Text != "hello (edited)" || m.MediaType != "photo" ||
		!m.EditedAt.Equal(edit.EditedAt) || m.SenderName != "Robert" {
		t.Errorf("edited message = %+v", m)
	}

	// The FTS index must follow the edit.
	for _, tt := range []struct {
		name string
		q    Query
		want int
	}{
		{"old text gone", Query{Text: "hello (edited)"}, 1},
		{"new sender indexed", Query{Sender: "Robert"}, 1},
		{"old sender gone", Query{Sender: "Bob"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := s.Messages(ctx, tt.q)
			if err != nil || len(res) != tt.want {
				t.Errorf("got %d results, %v; want %d", len(res), err, tt.want)
			}
		})
	}
}

func TestMarkDeleted(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		giveChat   int64
		giveIDs    []int64
		want       int64
		wantLeft   []int64
		wantHidden []int64
	}{
		{"none", 1, nil, 0, []int64{3, 2, 1}, nil},
		{"some", 1, []int64{1, 3}, 2, []int64{2}, []int64{3, 1}},
		{"unknown ids ignored", 1, []int64{99}, 0, []int64{3, 2, 1}, nil},
		{"other chat untouched", 2, []int64{1}, 1, []int64{3, 2, 1}, nil},
		{"duplicates counted once", 1, []int64{2, 2}, 1, []int64{3, 1}, []int64{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			mustUpsertChat(t, s, Chat{ID: 1})
			mustUpsertChat(t, s, Chat{ID: 2})
			for i := int64(1); i <= 3; i++ {
				mustSave(t, s, Message{ChatID: 1, ID: i, Date: _testBase.Add(time.Duration(i) * time.Second)})
			}
			mustSave(t, s, Message{ChatID: 2, ID: 1, Date: _testBase})

			n, err := s.MarkDeleted(ctx, tt.giveChat, tt.giveIDs)
			if err != nil || n != tt.want {
				t.Fatalf("MarkDeleted = %d, %v; want %d", n, err, tt.want)
			}
			left, err := s.Messages(ctx, Query{ChatID: ptr(int64(1))})
			if err != nil {
				t.Fatal(err)
			}
			if !equalInts(ids(left), tt.wantLeft) {
				t.Errorf("visible = %v, want %v", ids(left), tt.wantLeft)
			}

			// Deleting again is a no-op.
			if n, _ := s.MarkDeleted(ctx, tt.giveChat, tt.giveIDs); n != 0 {
				t.Errorf("second MarkDeleted = %d, want 0", n)
			}

			all, _ := s.Messages(ctx, Query{ChatID: ptr(int64(1)), IncludeDeleted: true})
			if len(all) != 3 {
				t.Errorf("IncludeDeleted returned %d, want 3", len(all))
			}
			for _, m := range all {
				hidden := false
				for _, id := range tt.wantHidden {
					hidden = hidden || id == m.ID
				}
				if hidden && m.DeletedAt.IsZero() {
					t.Errorf("message %d should have DeletedAt set", m.ID)
				}
			}
		})
	}
}

func seedMessages(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	mustUpsertChat(t, s, Chat{ID: 1, Title: "One"})
	mustUpsertChat(t, s, Chat{ID: 2, Title: "Two"})
	at := func(min int) time.Time { return _testBase.Add(time.Duration(min) * time.Minute) }
	mustSave(t, s, Message{ChatID: 1, ID: 1, Date: at(0), SenderName: "Иван Петров", SenderUsername: "ivan", Text: "Привет мир"})
	mustSave(t, s, Message{ChatID: 1, ID: 2, Date: at(1), SenderName: "Me", Text: "hello world", Outgoing: true})
	mustSave(t, s, Message{ChatID: 1, ID: 3, Date: at(2), SenderName: "Ivan Other", SenderUsername: "ivano", Text: "Meeting at noon"})
	mustSave(t, s, Message{ChatID: 2, ID: 1, Date: at(3), SenderName: "Zed", SenderUsername: "Zed_X", Text: "world cup"})
	mustSave(t, s, Message{ChatID: 2, ID: 2, Date: at(4), SenderName: "Zed", SenderUsername: "Zed_X", Text: "deleted one"})
	if _, err := s.MarkDeleted(context.Background(), 2, []int64{2}); err != nil {
		t.Fatal(err)
	}
	return s
}

type key struct{ chat, id int64 }

func TestMessages(t *testing.T) {
	s := seedMessages(t)
	ctx := context.Background()
	at := func(min int) time.Time { return _testBase.Add(time.Duration(min) * time.Minute) }

	// Results are identified by (chat, id) pairs encoded as chat*100+id.
	tests := []struct {
		name string
		give Query
		want []int64
	}{
		{"default newest first, hides deleted", Query{}, []int64{201, 103, 102, 101}},
		{"ascending", Query{Ascending: true}, []int64{101, 102, 103, 201}},
		{"include deleted", Query{IncludeDeleted: true}, []int64{202, 201, 103, 102, 101}},
		{"chat filter", Query{ChatID: ptr(int64(1))}, []int64{103, 102, 101}},
		{"incoming only", Query{IncomingOnly: true}, []int64{201, 103, 101}},
		{"text word", Query{Text: "world"}, []int64{201, 102}},
		{"text prefix", Query{Text: "meet"}, []int64{103}},
		{"text multiple words all required", Query{Text: "hello world"}, []int64{102}},
		{"cyrillic lowercase vs capitalised", Query{Text: "привет"}, []int64{101}},
		{"cyrillic uppercase query", Query{Text: "ПРИВ"}, []int64{101}},
		{"text with chat", Query{Text: "world", ChatID: ptr(int64(2))}, []int64{201}},
		{"sender display name", Query{Sender: "ivan"}, []int64{103}},
		{"sender cyrillic name", Query{Sender: "петров"}, []int64{101}},
		{"sender @username exact", Query{Sender: "@ivan"}, []int64{101}},
		{"sender @username not prefix", Query{Sender: "@iva"}, nil},
		{"sender @username case-insensitive", Query{Sender: "@zed_x"}, []int64{201}},
		{"sender and text", Query{Sender: "ivan", Text: "meeting"}, []int64{103}},
		{"since inclusive", Query{Since: at(2)}, []int64{201, 103}},
		{"until inclusive", Query{Until: at(1)}, []int64{102, 101}},
		{"since and until", Query{Since: at(1), Until: at(2)}, []int64{103, 102}},
		{"limit", Query{Limit: 2}, []int64{201, 103}},
		{"offset", Query{Limit: 2, Offset: 1}, []int64{103, 102}},
		{"negative offset ignored", Query{Limit: 1, Offset: -5}, []int64{201}},
		{"offset past end", Query{Offset: 100}, nil},
		{"no results", Query{Text: "absent"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Messages(ctx, tt.give)
			if err != nil {
				t.Fatalf("Messages: %v", err)
			}
			enc := make([]int64, 0, len(got))
			for _, m := range got {
				enc = append(enc, m.ChatID*100+m.ID)
			}
			if !equalInts(enc, tt.want) {
				t.Errorf("got %v, want %v", enc, tt.want)
			}
		})
	}

	t.Run("AfterPK", func(t *testing.T) {
		all, err := s.Messages(ctx, Query{Ascending: true, IncludeDeleted: true})
		if err != nil || len(all) != 5 {
			t.Fatalf("Messages: %d, %v", len(all), err)
		}
		got, err := s.Messages(ctx, Query{AfterPK: all[2].PK, Ascending: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].PK != all[3].PK {
			t.Errorf("got %+v, want only pk %d", got, all[3].PK)
		}
	})

	t.Run("limit clamped to MaxLimit", func(t *testing.T) {
		if _, err := s.Messages(ctx, Query{Limit: MaxLimit * 10}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMessagesFTSInputIsSafe(t *testing.T) {
	s := seedMessages(t)
	ctx := context.Background()

	tests := []struct {
		name string
		give string
	}{
		{"quote", `"`},
		{"unbalanced quote", `hello "world`},
		{"operators", `hello AND OR NOT world`},
		{"near", `NEAR(a b)`},
		{"parens", `(hello`},
		{"column filter", `text:hello`},
		{"star", `*`},
		{"caret", `^hello`},
		{"minus", `-hello`},
		{"punctuation only", `!!! ??? ---`},
		{"sql injection", `'; DROP TABLE messages; --`},
		{"whitespace only", "   \t "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Messages(ctx, Query{Text: tt.give}); err != nil {
				t.Errorf("Text %q: %v", tt.give, err)
			}
			if _, err := s.Messages(ctx, Query{Sender: tt.give}); err != nil {
				t.Errorf("Sender %q: %v", tt.give, err)
			}
		})
	}
	if got, err := s.Messages(ctx, Query{}); err != nil || len(got) != 4 {
		t.Errorf("table damaged: %d, %v", len(got), err)
	}
}

func TestContext(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustUpsertChat(t, s, Chat{ID: 1})
	mustUpsertChat(t, s, Chat{ID: 2})
	for i := int64(1); i <= 7; i++ {
		mustSave(t, s, Message{ChatID: 1, ID: i * 10, Date: _testBase.Add(time.Duration(i) * time.Second)})
	}
	mustSave(t, s, Message{ChatID: 2, ID: 35, Date: _testBase})

	tests := []struct {
		name       string
		giveChat   int64
		giveID     int64
		giveBefore int
		giveAfter  int
		want       []int64
		wantErr    error
	}{
		{"middle", 1, 40, 2, 2, []int64{20, 30, 40, 50, 60}, nil},
		{"only target", 1, 40, 0, 0, []int64{40}, nil},
		{"start clipped", 1, 10, 3, 1, []int64{10, 20}, nil},
		{"end clipped", 1, 70, 1, 5, []int64{60, 70}, nil},
		{"missing id", 1, 35, 1, 1, nil, ErrMessageNotFound},
		{"id in other chat", 2, 40, 1, 1, nil, ErrMessageNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Context(ctx, tt.giveChat, tt.giveID, tt.giveBefore, tt.giveAfter)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				want := "message " + formatInt(tt.giveID) + " not found in chat " + formatInt(tt.giveChat)
				if err.Error() != want {
					t.Errorf("err text = %q, want %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !equalInts(ids(got), tt.want) {
				t.Errorf("ids = %v, want %v", ids(got), tt.want)
			}
		})
	}
}
