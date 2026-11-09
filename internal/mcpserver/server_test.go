package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/collector"
	"github.com/mac3g0d/tg-secretary-mcp/internal/mcpserver"
	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
)

const token = "123:SECRET"

// fakeAPI imitates the Bot API: it hands out queued updates once and records
// sendMessage calls.
type fakeAPI struct {
	mu      sync.Mutex
	updates []string
	sent    []map[string]any
	drained chan struct{}
	once    sync.Once
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/bot"+token+"/")
	body, _ := io.ReadAll(r.Body)
	var params map[string]any
	_ = json.Unmarshal(body, &params)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "getMe":
		io.WriteString(w, `{"ok":true,"result":{"id":999,"is_bot":true,"first_name":"Secretary","username":"sec_bot"}}`)
	case "getUpdates":
		if len(f.updates) == 0 {
			f.once.Do(func() { close(f.drained) })
			io.WriteString(w, `{"ok":true,"result":[]}`)
			return
		}
		io.WriteString(w, `{"ok":true,"result":[`+strings.Join(f.updates, ",")+`]}`)
		f.updates = nil
	case "sendMessage":
		f.sent = append(f.sent, params)
		io.WriteString(w, `{"ok":true,"result":{"message_id":500,"date":1767225900,"text":"принято",
			"chat":{"id":-100,"type":"supergroup","title":"Команда Альфа"},
			"from":{"id":999,"is_bot":true,"first_name":"Secretary","username":"sec_bot"}}}`)
	default:
		http.Error(w, `{"ok":false,"error_code":404,"description":"Not Found"}`, http.StatusNotFound)
	}
}

var updates = []string{
	`{"update_id":1,"message":{"message_id":10,"date":1767225600,"text":"Привет! Созвон завтра в 10:00",
		"chat":{"id":-100,"type":"supergroup","title":"Команда Альфа","username":"alpha_team"},
		"from":{"id":1,"first_name":"Иван","last_name":"Петров","username":"ivan"}}}`,
	`{"update_id":2,"message":{"message_id":11,"date":1767225660,"text":"Ок, буду",
		"reply_to_message":{"message_id":10,"date":1767225600,"chat":{"id":-100,"type":"supergroup"}},
		"chat":{"id":-100,"type":"supergroup","title":"Команда Альфа","username":"alpha_team"},
		"from":{"id":2,"first_name":"Мария","username":"masha"}}}`,
	`{"update_id":3,"message":{"message_id":12,"date":1767225700,"caption":"смета по проекту","document":{"file_id":"x"},
		"chat":{"id":-100,"type":"supergroup","title":"Команда Альфа","username":"alpha_team"},
		"from":{"id":1,"first_name":"Иван","last_name":"Петров","username":"ivan"}}}`,
	`{"update_id":4,"edited_message":{"message_id":10,"date":1767225600,"edit_date":1767225720,"text":"Привет! Созвон завтра в 11:00",
		"chat":{"id":-100,"type":"supergroup","title":"Команда Альфа","username":"alpha_team"},
		"from":{"id":1,"first_name":"Иван","last_name":"Петров","username":"ivan"}}}`,
	`{"update_id":5,"message":{"message_id":13,"date":1767225730,"new_chat_members":[{"id":3,"first_name":"X"}],
		"chat":{"id":-100,"type":"supergroup","title":"Команда Альфа"},"from":{"id":3,"first_name":"X"}}}`,
	`{"update_id":6,"business_connection":{"id":"bc1","user":{"id":42,"first_name":"Owner"},"is_enabled":true}}`,
	`{"update_id":7,"business_message":{"message_id":20,"date":1767225740,"text":"Добрый день, счёт оплачен?","business_connection_id":"bc1",
		"chat":{"id":77,"type":"private","first_name":"Клиент","last_name":"Сидоров"},"from":{"id":77,"first_name":"Клиент","last_name":"Сидоров"}}}`,
	`{"update_id":8,"business_message":{"message_id":21,"date":1767225750,"text":"Да, оплатили вчера","business_connection_id":"bc1",
		"chat":{"id":77,"type":"private","first_name":"Клиент","last_name":"Сидоров"},"from":{"id":42,"first_name":"Owner"}}}`,
	`{"update_id":9,"business_message":{"message_id":22,"date":1767225760,"text":"ошибочное сообщение","business_connection_id":"bc1",
		"chat":{"id":77,"type":"private","first_name":"Клиент","last_name":"Сидоров"},"from":{"id":77,"first_name":"Клиент","last_name":"Сидоров"}}}`,
	`{"update_id":10,"deleted_business_messages":{"business_connection_id":"bc1","chat":{"id":77,"type":"private"},"message_ids":[22]}}`,
	`{"update_id":11,"message":{"message_id":1,"date":1767225770,"text":"spam","chat":{"id":555,"type":"private","first_name":"Stranger"},"from":{"id":555,"first_name":"Stranger"}}}`,
}

type env struct {
	t      *testing.T
	api    *fakeAPI
	store  *store.Store
	client *mcp.ClientSession
}

// setup runs the collector against the fake API until all updates are
// stored, then connects an MCP client to the server in memory.
func setup(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	api := &fakeAPI{updates: append([]string(nil), updates...), drained: make(chan struct{})}
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	tg := telegram.New(token, ts.URL)
	c := &collector.Collector{
		API: tg, Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		AllowedChats: map[int64]bool{-100: true, 77: true}, PollTimeout: time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-api.drained:
	case err := <-done:
		t.Fatalf("collector stopped early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("collector did not drain updates")
	}

	srv := mcpserver.New(mcpserver.Options{Store: db, Sender: tg})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, api: api, store: db, client: session}
}

func (e *env) call(name string, args map[string]any, out any) *mcp.CallToolResult {
	e.t.Helper()
	res, err := e.client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	if out != nil {
		if res.IsError {
			e.t.Fatalf("%s returned an error: %+v", name, res.Content[0])
		}
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s: decode %s: %v", name, raw, err)
		}
	}
	return res
}

func ids(out mcpserver.MessagesOutput) []int64 {
	var r []int64
	for _, m := range out.Messages {
		r = append(r, m.MessageID)
	}
	return r
}

func equal(a []int64, b ...int64) bool {
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

func TestListChats(t *testing.T) {
	e := setup(t)
	var out struct {
		Chats []struct {
			ID           int64  `json:"id"`
			Title        string `json:"title"`
			MessageCount int64  `json:"message_count"`
		} `json:"chats"`
	}
	e.call("list_chats", nil, &out)
	// Chat 555 is outside the allow list; the service message is not counted.
	if len(out.Chats) != 2 || out.Chats[0].ID != 77 || out.Chats[1].ID != -100 {
		t.Fatalf("unexpected chats: %+v", out.Chats)
	}
	if out.Chats[0].Title != "Клиент Сидоров" || out.Chats[0].MessageCount != 2 || out.Chats[1].MessageCount != 3 {
		t.Fatalf("unexpected chat details: %+v", out.Chats)
	}
	e.call("list_chats", map[string]any{"query": "АЛЬФА"}, &out)
	if len(out.Chats) != 1 || out.Chats[0].ID != -100 {
		t.Fatalf("case-insensitive filter failed: %+v", out.Chats)
	}
}

func TestGetMessages(t *testing.T) {
	e := setup(t)
	var out mcpserver.MessagesOutput
	e.call("get_messages", map[string]any{"chat": "альфа"}, &out)
	if !equal(ids(out), 10, 11, 12) {
		t.Fatalf("want chronological 10,11,12, got %v", ids(out))
	}
	first := out.Messages[0]
	if !first.Edited || !strings.Contains(first.Text, "11:00") || first.Sender != "Иван Петров" {
		t.Fatalf("edit was not applied: %+v", first)
	}
	if out.Messages[1].ReplyToID != 10 || out.Messages[2].Media != "document" || out.Messages[2].Text != "смета по проекту" {
		t.Fatalf("reply/media not stored: %+v", out.Messages)
	}

	e.call("get_messages", map[string]any{"chat": "@alpha_team", "limit": 2}, &out)
	if !equal(ids(out), 11, 12) || !out.HasMore {
		t.Fatalf("limit should keep the latest messages: %v has_more=%v", ids(out), out.HasMore)
	}
	e.call("get_messages", map[string]any{"chat": "-100", "sender": "@IVAN"}, &out)
	if !equal(ids(out), 10, 12) {
		t.Fatalf("sender filter: %v", ids(out))
	}

	// Business chat: the owner's reply is outgoing, the deleted message is hidden by default.
	e.call("get_messages", map[string]any{"chat": "77"}, &out)
	if !equal(ids(out), 20, 21) || out.Messages[0].Outgoing || !out.Messages[1].Outgoing {
		t.Fatalf("business chat: %+v", out.Messages)
	}
	e.call("get_messages", map[string]any{"chat": "77", "include_deleted": true}, &out)
	if !equal(ids(out), 20, 21, 22) || !out.Messages[2].Deleted {
		t.Fatalf("include_deleted: %+v", out.Messages)
	}

	if res := e.call("get_messages", map[string]any{"chat": "нет такого"}, nil); !res.IsError {
		t.Fatal("unknown chat should be a tool error")
	}
	if res := e.call("get_messages", map[string]any{"chat": "-100", "since": "yesterday-ish"}, nil); !res.IsError {
		t.Fatal("bad time should be a tool error")
	}
}

func TestSearchMessages(t *testing.T) {
	e := setup(t)
	var out mcpserver.MessagesOutput
	// Case-insensitive Cyrillic prefix search; the pre-edit text must be gone from the index.
	e.call("search_messages", map[string]any{"query": "СОЗВОН завт"}, &out)
	if !equal(ids(out), 10) {
		t.Fatalf("search: %v", ids(out))
	}
	e.call("search_messages", map[string]any{"query": "10:00"}, &out)
	if out.Count != 0 {
		t.Fatalf("stale text is still searchable: %+v", out.Messages)
	}
	e.call("search_messages", map[string]any{"query": "опла"}, &out)
	if !equal(ids(out), 21, 20) {
		t.Fatalf("prefix search across messages, newest first: %v", ids(out))
	}
	e.call("search_messages", map[string]any{"query": "опла", "sender": "сидоров"}, &out)
	if !equal(ids(out), 20) {
		t.Fatalf("sender name filter: %v", ids(out))
	}
	e.call("search_messages", map[string]any{"query": "опла", "chat": "альфа"}, &out)
	if out.Count != 0 {
		t.Fatalf("chat filter: %v", ids(out))
	}
	e.call("search_messages", map[string]any{"query": "созвон", "until": "2020-01-01"}, &out)
	if out.Count != 0 {
		t.Fatalf("until filter: %v", ids(out))
	}
	// FTS syntax in user input must not break the query.
	e.call("search_messages", map[string]any{"query": `"смета AND (NOT`}, &out)
	if out.Count != 0 {
		t.Fatalf("operators are plain words: %v", ids(out))
	}
}

func TestMessageContext(t *testing.T) {
	e := setup(t)
	var out mcpserver.MessagesOutput
	e.call("get_message_context", map[string]any{"chat": "-100", "message_id": 11, "before": 1, "after": 0}, &out)
	if !equal(ids(out), 10, 11) {
		t.Fatalf("context: %v", ids(out))
	}
	e.call("get_message_context", map[string]any{"chat": "-100", "message_id": 11}, &out)
	if !equal(ids(out), 10, 11, 12) {
		t.Fatalf("default context: %v", ids(out))
	}
	if res := e.call("get_message_context", map[string]any{"chat": "-100", "message_id": 999}, nil); !res.IsError {
		t.Fatal("missing message should be a tool error")
	}
}

func TestNewMessagesFeed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// Make the fixtures "recent" for the 24h first-call window.
	var out mcpserver.MessagesOutput
	e.call("get_new_messages", map[string]any{"consumer": "old"}, &out)
	if out.Count != 0 {
		t.Fatalf("messages older than 24h must not flood a new consumer: %v", ids(out))
	}

	save := func(id int64, text string, outgoing bool) {
		t.Helper()
		if err := e.store.SaveMessage(ctx, store.Message{ChatID: -100, ID: id, Date: time.Now(), Text: text, Outgoing: outgoing}); err != nil {
			t.Fatal(err)
		}
	}
	save(30, "новое 1", false)
	save(31, "мой ответ", true)
	save(32, "новое 2", false)

	e.call("get_new_messages", map[string]any{"peek": true}, &out)
	if !equal(ids(out), 30, 32) {
		t.Fatalf("peek: %v", ids(out))
	}
	e.call("get_new_messages", map[string]any{"limit": 1}, &out)
	if !equal(ids(out), 30) || !out.HasMore {
		t.Fatalf("first page: %v has_more=%v", ids(out), out.HasMore)
	}
	e.call("get_new_messages", nil, &out)
	if !equal(ids(out), 32) || out.HasMore {
		t.Fatalf("second page: %v", ids(out))
	}
	e.call("get_new_messages", nil, &out)
	if out.Count != 0 {
		t.Fatalf("feed should be drained: %v", ids(out))
	}
	save(33, "новое 3", false)
	e.call("get_new_messages", nil, &out)
	if !equal(ids(out), 33) {
		t.Fatalf("new message after drain: %v", ids(out))
	}
	// The "old" consumer has its own position and sees everything stored since.
	e.call("get_new_messages", map[string]any{"consumer": "old", "include_outgoing": true}, &out)
	if !equal(ids(out), 30, 31, 32, 33) {
		t.Fatalf("independent consumer: %v", ids(out))
	}
}

func TestSendMessageAndStatus(t *testing.T) {
	e := setup(t)
	var sent struct {
		Sent mcpserver.Message `json:"sent"`
	}
	e.call("send_message", map[string]any{"chat": "альфа", "text": "принято", "reply_to_message_id": 12}, &sent)
	if sent.Sent.MessageID != 500 || !sent.Sent.Outgoing {
		t.Fatalf("sent: %+v", sent.Sent)
	}
	e.api.mu.Lock()
	req := e.api.sent[0]
	e.api.mu.Unlock()
	if req["chat_id"].(float64) != -100 || req["text"] != "принято" ||
		req["reply_parameters"].(map[string]any)["message_id"].(float64) != 12 {
		t.Fatalf("unexpected sendMessage request: %v", req)
	}

	// Replies in a business chat go through the business connection.
	e.call("send_message", map[string]any{"chat": "77", "text": "ok"}, &sent)
	e.api.mu.Lock()
	req = e.api.sent[1]
	e.api.mu.Unlock()
	if req["business_connection_id"] != "bc1" {
		t.Fatalf("business_connection_id missing: %v", req)
	}

	var msgs mcpserver.MessagesOutput
	e.call("get_messages", map[string]any{"chat": "-100"}, &msgs)
	if !equal(ids(msgs), 10, 11, 12, 500) {
		t.Fatalf("sent message was not archived: %v", ids(msgs))
	}

	var status struct {
		Chats       int64  `json:"chats"`
		Messages    int64  `json:"messages"`
		Bot         string `json:"bot_username"`
		LastUpdate  string `json:"last_update_received_at"`
		SendEnabled bool   `json:"send_enabled"`
	}
	e.call("get_status", nil, &status)
	if status.Chats != 2 || status.Bot != "sec_bot" || status.LastUpdate == "" || !status.SendEnabled {
		t.Fatalf("status: %+v", status)
	}
}

func TestSendToolIsOptIn(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(mcpserver.Options{Store: db}).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "send_message" {
			t.Fatal("send_message must not be exposed without a sender")
		}
	}
	if len(res.Tools) != 6 {
		t.Fatalf("want 6 read tools, got %d", len(res.Tools))
	}
}

func TestOffsetIsPersisted(t *testing.T) {
	e := setup(t)
	offset, err := e.store.Meta(context.Background(), collector.MetaOffset)
	if err != nil || offset != "12" {
		t.Fatalf("offset = %q, %v; want 12", offset, err)
	}
}
