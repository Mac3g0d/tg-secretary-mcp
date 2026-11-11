package collector

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
)

func newTestCollector(t *testing.T, allowed map[int64]bool) (*Collector, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	c := New(Config{Store: db, AllowedChats: allowed})
	c.botID = 999
	return c, db
}

func TestHandle(t *testing.T) {
	chat := telegram.Chat{ID: -100, Type: "supergroup", Title: "Team"}
	user := &telegram.User{ID: 1, FirstName: "Ann"}

	tests := []struct {
		name         string
		allowed      map[int64]bool
		give         telegram.Update
		wantStored   int
		wantOutgoing bool
	}{
		{
			name: "text message",
			give: telegram.Update{Message: &telegram.Message{
				MessageID: 1, Chat: chat, From: user, Text: "hello",
			}},
			wantStored: 1,
		},
		{
			name: "message from the bot is outgoing",
			give: telegram.Update{Message: &telegram.Message{
				MessageID: 1, Chat: chat, From: &telegram.User{ID: 999}, Text: "hi",
			}},
			wantStored:   1,
			wantOutgoing: true,
		},
		{
			name: "service message is skipped",
			give: telegram.Update{Message: &telegram.Message{
				MessageID: 1, Chat: chat, From: user,
			}},
		},
		{
			name:    "chat outside the allow list is skipped",
			allowed: map[int64]bool{42: true},
			give: telegram.Update{Message: &telegram.Message{
				MessageID: 1, Chat: chat, From: user, Text: "hello",
			}},
		},
		{
			name:    "chat inside the allow list is stored",
			allowed: map[int64]bool{-100: true},
			give: telegram.Update{Message: &telegram.Message{
				MessageID: 1, Chat: chat, From: user, Text: "hello",
			}},
			wantStored: 1,
		},
		{
			name: "channel post",
			give: telegram.Update{ChannelPost: &telegram.Message{
				MessageID: 1, Chat: chat, SenderChat: &chat, Text: "news",
			}},
			wantStored: 1,
		},
		{
			name: "update without payload",
			give: telegram.Update{UpdateID: 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, db := newTestCollector(t, tt.allowed)
			ctx := context.Background()
			if err := c.handle(ctx, tt.give); err != nil {
				t.Fatalf("handle: %v", err)
			}

			msgs, err := db.Messages(ctx, store.Query{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != tt.wantStored {
				t.Fatalf("stored %d messages, want %d", len(msgs), tt.wantStored)
			}
			if len(msgs) > 0 && msgs[0].Outgoing != tt.wantOutgoing {
				t.Errorf("Outgoing = %v, want %v", msgs[0].Outgoing, tt.wantOutgoing)
			}
		})
	}
}

func TestForwardedMessageKeepsItsOrigin(t *testing.T) {
	tests := []struct {
		name         string
		give         *telegram.MessageOrigin
		wantFrom     string
		wantUsername string
		wantForward  bool
	}{
		{name: "not forwarded"},
		{
			name: "from a user",
			give: &telegram.MessageOrigin{
				Type: "user", Date: 1700000000,
				SenderUser: &telegram.User{ID: 7, FirstName: "Иван", Username: "ivan"},
			},
			wantFrom: "Иван", wantUsername: "ivan", wantForward: true,
		},
		{
			name:     "from a hidden user",
			give:     &telegram.MessageOrigin{Type: "hidden_user", Date: 1700000000, SenderUserName: "Аноним"},
			wantFrom: "Аноним", wantForward: true,
		},
		{
			name: "from a channel",
			give: &telegram.MessageOrigin{
				Type: "channel", Date: 1700000000,
				Chat: &telegram.Chat{ID: -1001, Type: "channel", Title: "Новости", Username: "news"},
			},
			wantFrom: "Новости", wantUsername: "news", wantForward: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			c, db := newTestCollector(t, nil)
			err := c.handle(ctx, telegram.Update{Message: &telegram.Message{
				MessageID:     1,
				Date:          1700000500,
				Chat:          telegram.Chat{ID: 5, Type: "private", FirstName: "Ann"},
				From:          &telegram.User{ID: 5, FirstName: "Ann"},
				Text:          "смотри",
				ForwardOrigin: tt.give,
			}})
			if err != nil {
				t.Fatal(err)
			}
			msgs, err := db.Messages(ctx, store.Query{})
			if err != nil || len(msgs) != 1 {
				t.Fatalf("stored %d messages, %v", len(msgs), err)
			}
			m := msgs[0]
			if m.Forwarded() != tt.wantForward || m.ForwardFrom != tt.wantFrom ||
				m.ForwardFromUsername != tt.wantUsername {
				t.Errorf("forward = %v from %q (@%s)", m.Forwarded(), m.ForwardFrom, m.ForwardFromUsername)
			}
			// The sender stays the person who forwarded it.
			if m.SenderName != "Ann" {
				t.Errorf("sender = %q, want the forwarding user", m.SenderName)
			}
			if tt.wantForward && m.ForwardDate.Unix() != 1700000000 {
				t.Errorf("forward date = %v", m.ForwardDate)
			}
		})
	}
}
