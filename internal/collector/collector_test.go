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
