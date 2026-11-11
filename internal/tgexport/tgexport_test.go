package tgexport

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

// _personalExport is a single-chat export in the layout Telegram Desktop writes.
const _personalExport = `{
 "name": "Иван Петров",
 "type": "personal_chat",
 "id": 111,
 "messages": [
  {"id": 1, "type": "service", "date": "2024-01-01T09:00:00", "date_unixtime": "1704099600",
   "actor": "Иван Петров", "actor_id": "user111", "action": "phone_call", "text": "", "text_entities": []},
  {"id": 2, "type": "message", "date": "2024-01-01T10:00:00", "date_unixtime": "1704103200",
   "from": "Иван Петров", "from_id": "user111", "text": "Привет! Договор готов?", "text_entities": []},
  {"id": 3, "type": "message", "date": "2024-01-01T10:01:00", "date_unixtime": "1704103260",
   "edited": "2024-01-01T10:05:00", "edited_unixtime": "1704103500",
   "from": "Я", "from_id": "user222", "reply_to_message_id": 2,
   "text": ["Да, смотри ", {"type": "link", "text": "https://example.com/doc"}, " — там всё"],
   "text_entities": []},
  {"id": 4, "type": "message", "date": "2024-01-01T10:02:00", "date_unixtime": "1704103320",
   "from": "Иван Петров", "from_id": "user111",
   "photo": "(File not included. Change data exporting settings to download.)",
   "width": 100, "height": 100, "text": "", "text_entities": []},
  {"id": 5, "type": "message", "date": "2024-01-01T10:03:00", "date_unixtime": "1704103380",
   "from": "Иван Петров", "from_id": "user111", "forwarded_from": "Новости проекта",
   "text": "Релиз завтра", "text_entities": []},
  {"id": 6, "type": "message", "date": "2024-01-01T10:04:00", "date_unixtime": "1704103440",
   "from": "Иван Петров", "from_id": "user111", "forwarded_from": null,
   "file": "(File not included. Change data exporting settings to download.)",
   "media_type": "voice_message", "mime_type": "audio/ogg", "duration_seconds": 3,
   "text": "", "text_entities": []},
  {"id": 7, "type": "message", "date": "2024-01-01T10:05:00", "date_unixtime": "1704103500",
   "from": null, "from_id": "user111", "text": "", "text_entities": []}
 ]
}`

const _fullExport = `{
 "about": "Here is the data you requested.",
 "personal_information": {"user_id": 222, "first_name": "Я"},
 "contacts": {"about": "…", "list": [{"first_name": "Иван", "phone_number": "+100"}]},
 "chats": {
  "about": "This page lists all chats from this export.",
  "list": [
   {"name": "Команда", "type": "private_supergroup", "id": 1234567890, "messages": [
    {"id": 10, "type": "message", "date": "2024-02-01T10:00:00", "date_unixtime": "1706781600",
     "from": "Мария", "from_id": "user333", "text": "Всем привет", "text_entities": []}
   ]},
   {"name": "Мария", "type": "personal_chat", "id": 888, "messages": [
    {"id": 1, "type": "message", "date": "2024-02-01T10:00:00", "date_unixtime": "1706781600",
     "from": "Мария", "from_id": "user888", "text": "Привет", "text_entities": []},
    {"id": 2, "type": "message", "date": "2024-02-01T10:01:00", "date_unixtime": "1706781660",
     "from": "Я", "from_id": "user222", "text": "Привет!", "text_entities": []}
   ]},
   {"name": "Дача", "type": "private_group", "id": 555, "messages": []},
   {"name": "Канал", "type": "public_channel", "id": 42, "messages": [
    {"id": 1, "type": "message", "date_unixtime": "1706781600", "from": "Канал",
     "from_id": "channel42", "text": "пост"}
   ]},
   {"name": "BotFather", "type": "bot_chat", "id": 93372553, "messages": [
    {"id": 1, "type": "message", "date_unixtime": "1706781600", "from": "Я",
     "from_id": "user222", "text": "/start"}
   ]},
   {"name": null, "type": "saved_messages", "id": 222, "messages": [
    {"id": 1, "type": "message", "date_unixtime": "1706781600", "from": "Я",
     "from_id": "user222", "text": "заметка"}
   ]},
   {"name": null, "type": "personal_chat", "id": 777, "messages": [
    {"id": 1, "type": "message", "date": "2024-02-02T10:00:00",
     "from": null, "from_id": "user777", "text": "без unixtime", "text_entities": []}
   ]}
  ]
 }
}`

func newStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func messages(t *testing.T, db *store.Store, chatID int64) []store.Message {
	t.Helper()
	msgs, err := db.Messages(context.Background(), store.Query{
		ChatID:    &chatID,
		Ascending: true,
		Limit:     store.MaxLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestImportPersonalChat(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	res, err := Import(ctx, db, strings.NewReader(_personalExport))
	if err != nil {
		t.Fatal(err)
	}
	want := ChatResult{ChatID: 111, Title: "Иван Петров", Type: "private", Imported: 5, Skipped: 2}
	if len(res.Chats) != 1 || res.Chats[0] != want {
		t.Fatalf("result = %+v, want %+v", res.Chats, want)
	}

	msgs := messages(t, db, 111)
	if len(msgs) != 5 {
		t.Fatalf("%d messages stored, want 5", len(msgs))
	}
	tests := []struct {
		name  string
		got   store.Message
		check func(store.Message) bool
	}{
		{"incoming text", msgs[0], func(m store.Message) bool {
			return m.ID == 2 && m.Text == "Привет! Договор готов?" && !m.Outgoing &&
				m.SenderID == 111 && m.SenderName == "Иван Петров" &&
				m.Date.Equal(time.Unix(1704103200, 0)) && m.ChatTitle == "Иван Петров"
		}},
		{"outgoing reply with entities and an edit", msgs[1], func(m store.Message) bool {
			return m.Text == "Да, смотри https://example.com/doc — там всё" && m.Outgoing &&
				m.ReplyToID == 2 && m.EditedAt.Equal(time.Unix(1704103500, 0))
		}},
		{"photo without caption", msgs[2], func(m store.Message) bool {
			return m.MediaType == "photo" && m.Text == ""
		}},
		{"forward", msgs[3], func(m store.Message) bool {
			return m.Forwarded() && m.ForwardFrom == "Новости проекта"
		}},
		{"forwarded voice from a deleted account", msgs[4], func(m store.Message) bool {
			return m.MediaType == "voice" && m.Forwarded() && m.ForwardFrom == "Deleted Account"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.check(tt.got) {
				t.Errorf("unexpected message: %+v", tt.got)
			}
		})
	}

	// Imported text is searchable like any other.
	found, err := db.Messages(ctx, store.Query{Text: "договор"})
	if err != nil || len(found) != 1 || found[0].ID != 2 {
		t.Errorf("search found %+v, %v", found, err)
	}
	// It is history, not news.
	live, err := db.Messages(ctx, store.Query{LiveOnly: true})
	if err != nil || len(live) != 0 {
		t.Errorf("LiveOnly returned imported rows: %+v, %v", live, err)
	}
}

func TestImportIsIdempotentAndKeepsLiveData(t *testing.T) {
	ctx := context.Background()
	db := newStore(t)

	// The bot already knows the chat and one of the messages.
	if err := db.UpsertChat(ctx, store.Chat{ID: 111, Type: "private", Title: "Ваня", Username: "ivan"}); err != nil {
		t.Fatal(err)
	}
	live := store.Message{ChatID: 111, ID: 2, Date: time.Unix(1704103200, 0), Text: "живая версия"}
	if err := db.SaveMessage(ctx, live); err != nil {
		t.Fatal(err)
	}

	for i, want := range []ChatResult{
		{ChatID: 111, Title: "Иван Петров", Type: "private", Imported: 4, AlreadyStored: 1, Skipped: 2},
		{ChatID: 111, Title: "Иван Петров", Type: "private", Imported: 0, AlreadyStored: 5, Skipped: 2},
	} {
		res, err := Import(ctx, db, strings.NewReader(_personalExport))
		if err != nil {
			t.Fatal(err)
		}
		if res.Chats[0] != want {
			t.Errorf("import #%d = %+v, want %+v", i+1, res.Chats[0], want)
		}
	}

	msgs := messages(t, db, 111)
	if len(msgs) != 5 {
		t.Fatalf("%d messages, want 5", len(msgs))
	}
	for _, m := range msgs {
		if m.ID == 2 && m.Text != "живая версия" {
			t.Errorf("import overwrote a live message: %+v", m)
		}
	}
	chat, err := db.ResolveChat(ctx, "@ivan")
	if err != nil || chat.Title != "Ваня" {
		t.Errorf("import changed the live chat: %+v, %v", chat, err)
	}
	onlyLive, err := db.Messages(ctx, store.Query{LiveOnly: true})
	if err != nil || len(onlyLive) != 1 {
		t.Errorf("LiveOnly = %+v, %v; want just the live message", onlyLive, err)
	}
}

func TestImportFullExportTakesOnlyPersonalChats(t *testing.T) {
	db := newStore(t)

	res, err := Import(context.Background(), db, strings.NewReader(_fullExport))
	if err != nil {
		t.Fatal(err)
	}
	wantChats := []ChatResult{
		{ChatID: 888, Title: "Мария", Type: "private", Imported: 2},
		{ChatID: 777, Title: "Deleted Account", Type: "private", Imported: 1},
	}
	if !slices.Equal(res.Chats, wantChats) {
		t.Errorf("chats = %+v, want %+v", res.Chats, wantChats)
	}
	wantSkipped := []UnsupportedChat{
		{Title: "Команда", Type: "private_supergroup"},
		{Title: "Дача", Type: "private_group"},
		{Title: "Канал", Type: "public_channel"},
		{Title: "BotFather", Type: "bot_chat"},
		{Title: "", Type: "saved_messages"},
	}
	if !slices.Equal(res.Unsupported, wantSkipped) {
		t.Errorf("unsupported = %+v, want %+v", res.Unsupported, wantSkipped)
	}

	stats, err := db.Stats(context.Background())
	if err != nil || stats.Chats != 2 || stats.Messages != 3 {
		t.Errorf("archive holds %d chats and %d messages (%v), want 2 and 3",
			stats.Chats, stats.Messages, err)
	}

	// The owner is known from personal_information.
	maria := messages(t, db, 888)
	if len(maria) != 2 || maria[0].Outgoing || !maria[1].Outgoing {
		t.Errorf("outgoing flags wrong: %+v", maria)
	}

	deleted := messages(t, db, 777)
	wantDate := time.Date(2024, 2, 2, 10, 0, 0, 0, time.Local)
	if len(deleted) != 1 || deleted[0].SenderName != "Deleted Account" || !deleted[0].Date.Equal(wantDate) {
		t.Errorf("deleted-account chat = %+v", deleted)
	}
}

func TestImportRejectsNonPersonalChat(t *testing.T) {
	tests := []struct {
		name string
		give string
	}{
		{name: "supergroup", give: "private_supergroup"},
		{name: "basic group", give: "private_group"},
		{name: "channel", give: "public_channel"},
		{name: "bot chat", give: "bot_chat"},
		{name: "saved messages", give: "saved_messages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newStore(t)
			export := `{"name": "Команда", "type": "` + tt.give + `", "id": 5, "messages": [
				{"id": 1, "type": "message", "date_unixtime": "1700000000",
				 "from": "Мария", "from_id": "user333", "text": "привет"}]}`

			res, err := Import(context.Background(), db, strings.NewReader(export))
			if !errors.Is(err, ErrNotPersonal) {
				t.Fatalf("error = %v, want ErrNotPersonal", err)
			}
			if !strings.Contains(err.Error(), tt.give) {
				t.Errorf("error %q should name the chat type", err)
			}
			if len(res.Chats) != 0 || len(res.Unsupported) != 1 {
				t.Errorf("result = %+v", res)
			}
			stats, err := db.Stats(context.Background())
			if err != nil || stats.Chats != 0 || stats.Messages != 0 {
				t.Errorf("nothing should be stored, got %+v, %v", stats, err)
			}
		})
	}
}

func TestImportRejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		give    string
		wantErr error  // matched with errors.Is when set
		wantMsg string // otherwise a substring of the error
	}{
		{name: "not json", give: "hello", wantMsg: "read export"},
		{name: "json array", give: `[1, 2]`, wantMsg: "unexpected token ["},
		{name: "other json", give: `{"foo": {"bar": 1}}`, wantErr: ErrNoChats},
		{name: "empty full export", give: `{"chats": {"list": []}}`, wantErr: ErrNoChats},
		{name: "truncated", give: _personalExport[:len(_personalExport)/2], wantMsg: "read export"},
		{
			name:    "messages before id",
			give:    `{"name": "x", "messages": [], "id": 1}`,
			wantMsg: "unsupported export layout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Import(context.Background(), newStore(t), strings.NewReader(tt.give))
			switch {
			case err == nil:
				t.Fatal("expected an error")
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			case tt.wantErr == nil && !strings.Contains(err.Error(), tt.wantMsg):
				t.Errorf("error = %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestImportLargeChatIsBatched(t *testing.T) {
	const total = 2*_batchSize + 7

	var b strings.Builder
	b.WriteString(`{"name": "Big", "type": "personal_chat", "id": 42, "messages": [`)
	for i := 1; i <= total; i++ {
		if i > 1 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id": `)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`, "type": "message", "date_unixtime": "1700000000", "from": "Big", "from_id": "user42", "text": "пост"}`)
	}
	b.WriteString(`]}`)

	res, err := Import(context.Background(), newStore(t), strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	want := ChatResult{ChatID: 42, Title: "Big", Type: "private", Imported: total}
	if len(res.Chats) != 1 || res.Chats[0] != want {
		t.Fatalf("result = %+v, want %+v", res.Chats, want)
	}
}

func TestImportStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Import(ctx, newStore(t), strings.NewReader(_personalExport))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
