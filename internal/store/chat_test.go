package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUpsertChat(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name  string
		first Chat
		then  Chat
		want  Chat
	}{
		{
			name:  "business connection id preserved when empty",
			first: Chat{ID: 1, Type: "private", Title: "Old", BusinessConnectionID: "bc"},
			then:  Chat{ID: 1, Type: "private", Title: "New"},
			want:  Chat{ID: 1, Type: "private", Title: "New", BusinessConnectionID: "bc"},
		},
		{
			name:  "business connection id replaced when set",
			first: Chat{ID: 1, BusinessConnectionID: "bc"},
			then:  Chat{ID: 1, BusinessConnectionID: "bc2"},
			want:  Chat{ID: 1, BusinessConnectionID: "bc2"},
		},
		{
			name:  "fields refreshed",
			first: Chat{ID: 1, Type: "group", Title: "T", Username: "u"},
			then:  Chat{ID: 1, Type: "supergroup", Title: "T2", Username: "u2"},
			want:  Chat{ID: 1, Type: "supergroup", Title: "T2", Username: "u2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			mustUpsertChat(t, s, tt.first)
			mustUpsertChat(t, s, tt.then)
			got, err := s.ResolveChat(ctx, "1")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("chat = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func seedChats(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	mustUpsertChat(t, s, Chat{ID: 1, Type: "private", Title: "Alice Smith", Username: "alice"})
	mustUpsertChat(t, s, Chat{ID: 2, Type: "group", Title: "Работа команда"})
	mustUpsertChat(t, s, Chat{ID: 3, Type: "group", Title: "Alice Fan Club", Username: "fans"})
	mustUpsertChat(t, s, Chat{ID: 4, Type: "channel", Title: "News", Username: "News_Daily"})
	mustSave(t, s, Message{ChatID: 1, ID: 1, Date: _testBase, Text: "old"})
	mustSave(t, s, Message{ChatID: 2, ID: 1, Date: _testBase.Add(time.Hour), Text: "newest"})
	mustSave(t, s, Message{ChatID: 3, ID: 1, Date: _testBase.Add(30 * time.Minute), Text: "mid"})
	return s
}

func TestChats(t *testing.T) {
	s := seedChats(t)
	ctx := context.Background()

	tests := []struct {
		name       string
		giveFilter string
		giveType   string
		giveLimit  int
		want       []int64 // chat IDs in order
	}{
		{"all ordered by recency then id", "", "", 0, []int64{2, 3, 1, 4}},
		{"filter by title case-insensitive", "ALICE", "", 0, []int64{3, 1}},
		{"filter by cyrillic title", "РАБОТА", "", 0, []int64{2}},
		{"filter by username with at", "@news_dai", "", 0, []int64{4}},
		{"filter by type", "", "group", 0, []int64{2, 3}},
		{"filter and type", "alice", "private", 0, []int64{1}},
		{"limit", "", "", 2, []int64{2, 3}},
		{"no match", "zzz", "", 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Chats(ctx, tt.giveFilter, tt.giveType, tt.giveLimit)
			if err != nil {
				t.Fatal(err)
			}
			var gotIDs []int64
			for _, c := range got {
				gotIDs = append(gotIDs, c.Chat.ID)
			}
			if !equalInts(gotIDs, tt.want) {
				t.Errorf("ids = %v, want %v", gotIDs, tt.want)
			}
		})
	}

	t.Run("summary fields", func(t *testing.T) {
		got, err := s.Chats(ctx, "", "group", 1)
		if err != nil || len(got) != 1 {
			t.Fatalf("Chats: %v, %v", got, err)
		}
		c := got[0]
		if c.MessageCount != 1 || c.LastText != "newest" || !c.LastDate.Equal(_testBase.Add(time.Hour)) {
			t.Errorf("summary = %+v", c)
		}
	})
}

func TestResolveChat(t *testing.T) {
	s := seedChats(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		give    string
		wantID  int64
		wantErr error
		wantMsg string // substring of the error text
	}{
		{name: "numeric id", give: "2", wantID: 2},
		{name: "numeric id with spaces", give: "  2 ", wantID: 2},
		{name: "username with at", give: "@alice", wantID: 1},
		{name: "username case-insensitive", give: "@NEWS_daily", wantID: 4},
		{name: "bare username", give: "fans", wantID: 3},
		{name: "exact title", give: "alice smith", wantID: 1},
		{name: "partial title cyrillic", give: "команда", wantID: 2},
		{name: "unique partial", give: "club", wantID: 3},
		{
			name: "ambiguous", give: "alic", wantErr: ErrAmbiguousChat,
			wantMsg: `"alic" matches several chats, pass the id instead: `,
		},
		{
			name: "missing numeric id", give: "999", wantErr: ErrChatNotFound,
			wantMsg: "chat 999 is not in the archive",
		},
		{
			name: "missing title", give: "nothing", wantErr: ErrChatNotFound,
			wantMsg: `no chat matches "nothing"; use list_chats to see known chats`,
		},
		{
			name: "at prefix never matches title", give: "@Alice Smith",
			wantErr: ErrChatNotFound,
		},
		{name: "empty", give: "  ", wantErr: ErrChatRequired, wantMsg: "chat is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ResolveChat(ctx, tt.give)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want errors.Is %v", err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantMsg) {
					t.Errorf("err text = %q, want containing %q", err, tt.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tt.wantID {
				t.Errorf("ID = %d, want %d", got.ID, tt.wantID)
			}
		})
	}

	t.Run("ambiguity lists candidates", func(t *testing.T) {
		_, err := s.ResolveChat(ctx, "alic")
		if err == nil || !strings.Contains(err.Error(), `"Alice Fan Club" (id 3)`) ||
			!strings.Contains(err.Error(), `"Alice Smith" (id 1)`) {
			t.Errorf("err = %v", err)
		}
	})
}
