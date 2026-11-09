package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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

const _token = "123:SECRET"

// fakeAPI imitates the Bot API: it hands out queued updates once and records
// sendMessage calls.
type fakeAPI struct {
	mu      sync.Mutex
	updates []string
	sent    []map[string]any
	drained chan struct{}
	once    sync.Once
}

var _ http.Handler = (*fakeAPI)(nil)

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/bot"+_token+"/")
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

var _updates = []string{
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

	api := &fakeAPI{updates: slices.Clone(_updates), drained: make(chan struct{})}
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	tg := telegram.New(_token, ts.URL)
	c := collector.New(collector.Config{
		API:          tg,
		Store:        db,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		AllowedChats: map[int64]bool{-100: true, 77: true},
		PollTimeout:  time.Second,
	})
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	// Stop the collector and wait for it before the database is closed.
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-api.drained:
	case err := <-done:
		done <- err
		t.Fatalf("collector stopped early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("collector did not drain updates")
	}

	srv := mcpserver.New(mcpserver.Options{Store: db, Sender: tg, Version: "test"})
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

// sentRequest returns the i-th sendMessage request the fake API received.
func (e *env) sentRequest(i int) map[string]any {
	e.t.Helper()
	e.api.mu.Lock()
	defer e.api.mu.Unlock()
	if i >= len(e.api.sent) {
		e.t.Fatalf("only %d sendMessage requests, want index %d", len(e.api.sent), i)
	}
	return e.api.sent[i]
}

func ids(out mcpserver.MessagesOutput) []int64 {
	r := make([]int64, 0, len(out.Messages))
	for _, m := range out.Messages {
		r = append(r, m.MessageID)
	}
	return r
}

func TestListChats(t *testing.T) {
	type chats struct {
		Chats []struct {
			ID           int64  `json:"id"`
			Title        string `json:"title"`
			MessageCount int64  `json:"message_count"`
		} `json:"chats"`
	}
	e := setup(t)

	tests := []struct {
		name      string
		give      map[string]any
		wantIDs   []int64
		wantCount []int64 // message counts, parallel to wantIDs
	}{
		// Chat 555 is outside the allow list; the service message is not counted.
		{name: "all, most recent first", wantIDs: []int64{77, -100}, wantCount: []int64{2, 3}},
		{
			name:      "case-insensitive filter",
			give:      map[string]any{"query": "АЛЬФА"},
			wantIDs:   []int64{-100},
			wantCount: []int64{3},
		},
		{
			name:      "type filter",
			give:      map[string]any{"type": "private"},
			wantIDs:   []int64{77},
			wantCount: []int64{2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out chats
			e.call("list_chats", tt.give, &out)
			var gotIDs, gotCount []int64
			for _, c := range out.Chats {
				gotIDs = append(gotIDs, c.ID)
				gotCount = append(gotCount, c.MessageCount)
			}
			if !slices.Equal(gotIDs, tt.wantIDs) || !slices.Equal(gotCount, tt.wantCount) {
				t.Fatalf("got ids %v counts %v, want ids %v counts %v",
					gotIDs, gotCount, tt.wantIDs, tt.wantCount)
			}
		})
	}

	t.Run("titles", func(t *testing.T) {
		var out chats
		e.call("list_chats", nil, &out)
		if out.Chats[0].Title != "Клиент Сидоров" || out.Chats[1].Title != "Команда Альфа" {
			t.Fatalf("unexpected titles: %+v", out.Chats)
		}
	})
}

func TestGetMessages(t *testing.T) {
	e := setup(t)

	tests := []struct {
		name     string
		give     map[string]any
		want     []int64
		wantMore bool
	}{
		{
			name: "chronological by title",
			give: map[string]any{"chat": "альфа"},
			want: []int64{10, 11, 12},
		},
		{
			name:     "limit keeps the latest",
			give:     map[string]any{"chat": "@alpha_team", "limit": 2},
			want:     []int64{11, 12},
			wantMore: true,
		},
		{
			name: "sender filter",
			give: map[string]any{"chat": "-100", "sender": "@IVAN"},
			want: []int64{10, 12},
		},
		{
			name: "deleted hidden by default",
			give: map[string]any{"chat": "77"},
			want: []int64{20, 21},
		},
		{
			name: "include deleted",
			give: map[string]any{"chat": "77", "include_deleted": true},
			want: []int64{20, 21, 22},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out mcpserver.MessagesOutput
			e.call("get_messages", tt.give, &out)
			if got := ids(out); !slices.Equal(got, tt.want) || out.HasMore != tt.wantMore {
				t.Fatalf("got %v has_more=%v, want %v has_more=%v",
					got, out.HasMore, tt.want, tt.wantMore)
			}
		})
	}

	t.Run("stored fields", func(t *testing.T) {
		var out mcpserver.MessagesOutput
		e.call("get_messages", map[string]any{"chat": "альфа"}, &out)
		first := out.Messages[0]
		if !first.Edited || !strings.Contains(first.Text, "11:00") || first.Sender != "Иван Петров" {
			t.Fatalf("edit was not applied: %+v", first)
		}
		if out.Messages[1].ReplyToID != 10 || out.Messages[2].Media != "document" ||
			out.Messages[2].Text != "смета по проекту" {
			t.Fatalf("reply/media not stored: %+v", out.Messages)
		}
	})

	t.Run("business chat flags", func(t *testing.T) {
		// The owner's reply is outgoing; the deleted message carries its flag.
		var out mcpserver.MessagesOutput
		e.call("get_messages", map[string]any{"chat": "77", "include_deleted": true}, &out)
		if out.Messages[0].Outgoing || !out.Messages[1].Outgoing || !out.Messages[2].Deleted {
			t.Fatalf("business chat: %+v", out.Messages)
		}
	})

	errTests := []struct {
		name string
		give map[string]any
	}{
		{name: "unknown chat", give: map[string]any{"chat": "нет такого"}},
		{name: "bad since", give: map[string]any{"chat": "-100", "since": "yesterday-ish"}},
		{name: "bad until", give: map[string]any{"chat": "-100", "until": "later"}},
	}
	for _, tt := range errTests {
		t.Run(tt.name, func(t *testing.T) {
			if res := e.call("get_messages", tt.give, nil); !res.IsError {
				t.Fatal("want a tool error")
			}
		})
	}
}

func TestSearchMessages(t *testing.T) {
	e := setup(t)

	tests := []struct {
		name string
		give map[string]any
		want []int64
	}{
		{
			name: "case-insensitive Cyrillic prefix",
			give: map[string]any{"query": "СОЗВОН завт"},
			want: []int64{10},
		},
		{
			name: "pre-edit text is gone from the index",
			give: map[string]any{"query": "10:00"},
		},
		{
			name: "prefix across messages, newest first",
			give: map[string]any{"query": "опла"},
			want: []int64{21, 20},
		},
		{
			name: "sender name filter",
			give: map[string]any{"query": "опла", "sender": "сидоров"},
			want: []int64{20},
		},
		{
			name: "chat filter",
			give: map[string]any{"query": "опла", "chat": "альфа"},
		},
		{
			name: "until filter",
			give: map[string]any{"query": "созвон", "until": "2020-01-01"},
		},
		{
			name: "FTS operators are plain words",
			give: map[string]any{"query": `"смета AND (NOT`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out mcpserver.MessagesOutput
			e.call("search_messages", tt.give, &out)
			if got := ids(out); !slices.Equal(got, tt.want) || out.Count != len(tt.want) {
				t.Fatalf("got %v (count %d), want %v", got, out.Count, tt.want)
			}
		})
	}

	t.Run("empty query is an error", func(t *testing.T) {
		if res := e.call("search_messages", map[string]any{"query": "  "}, nil); !res.IsError {
			t.Fatal("want a tool error")
		}
	})
}

func TestMessageContext(t *testing.T) {
	e := setup(t)

	tests := []struct {
		name string
		give map[string]any
		want []int64
	}{
		{
			name: "explicit window",
			give: map[string]any{"chat": "-100", "message_id": 11, "before": 1, "after": 0},
			want: []int64{10, 11},
		},
		{
			name: "default window",
			give: map[string]any{"chat": "-100", "message_id": 11},
			want: []int64{10, 11, 12},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out mcpserver.MessagesOutput
			e.call("get_message_context", tt.give, &out)
			if got := ids(out); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("missing message is an error", func(t *testing.T) {
		give := map[string]any{"chat": "-100", "message_id": 999}
		if res := e.call("get_message_context", give, nil); !res.IsError {
			t.Fatal("want a tool error")
		}
	})
}

func TestNewMessagesFeed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	// The fixtures are older than the 24h window of a consumer's first call.
	var out mcpserver.MessagesOutput
	e.call("get_new_messages", map[string]any{"consumer": "old"}, &out)
	if out.Count != 0 {
		t.Fatalf("messages older than 24h must not flood a new consumer: %v", ids(out))
	}

	save := func(id int64, text string, outgoing bool) {
		t.Helper()
		err := e.store.SaveMessage(ctx, store.Message{
			ChatID:   -100,
			ID:       id,
			Date:     time.Now(),
			Text:     text,
			Outgoing: outgoing,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	save(30, "новое 1", false)
	save(31, "мой ответ", true)
	save(32, "новое 2", false)

	// The steps depend on each other: each one moves the consumer's position.
	steps := []struct {
		name     string
		give     map[string]any
		want     []int64
		wantMore bool
		before   func()
	}{
		{name: "peek does not advance", give: map[string]any{"peek": true}, want: []int64{30, 32}},
		{
			name:     "first page",
			give:     map[string]any{"limit": 1},
			want:     []int64{30},
			wantMore: true,
		},
		{name: "second page", want: []int64{32}},
		{name: "drained"},
		{
			name:   "new message after drain",
			want:   []int64{33},
			before: func() { save(33, "новое 3", false) },
		},
		{
			name: "independent consumer sees everything stored since",
			give: map[string]any{"consumer": "old", "include_outgoing": true},
			want: []int64{30, 31, 32, 33},
		},
	}
	for _, step := range steps {
		if step.before != nil {
			step.before()
		}
		e.call("get_new_messages", step.give, &out)
		if got := ids(out); !slices.Equal(got, step.want) || out.HasMore != step.wantMore {
			t.Fatalf("%s: got %v has_more=%v, want %v has_more=%v",
				step.name, got, out.HasMore, step.want, step.wantMore)
		}
	}
}

func TestSendMessageAndStatus(t *testing.T) {
	e := setup(t)
	var sent struct {
		Sent mcpserver.Message `json:"sent"`
	}

	e.call("send_message", map[string]any{
		"chat":                "альфа",
		"text":                "принято",
		"reply_to_message_id": 12,
	}, &sent)
	if sent.Sent.MessageID != 500 || !sent.Sent.Outgoing {
		t.Fatalf("sent: %+v", sent.Sent)
	}
	req := e.sentRequest(0)
	reply, _ := req["reply_parameters"].(map[string]any)
	if req["chat_id"] != float64(-100) || req["text"] != "принято" ||
		reply["message_id"] != float64(12) {
		t.Fatalf("unexpected sendMessage request: %v", req)
	}

	// Replies in a business chat go through the business connection.
	e.call("send_message", map[string]any{"chat": "77", "text": "ok"}, &sent)
	if req := e.sentRequest(1); req["business_connection_id"] != "bc1" {
		t.Fatalf("business_connection_id missing: %v", req)
	}

	var msgs mcpserver.MessagesOutput
	e.call("get_messages", map[string]any{"chat": "-100"}, &msgs)
	if want := []int64{10, 11, 12, 500}; !slices.Equal(ids(msgs), want) {
		t.Fatalf("sent message was not archived: %v, want %v", ids(msgs), want)
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

func TestSendMessageErrors(t *testing.T) {
	e := setup(t)
	tests := []struct {
		name string
		give map[string]any
	}{
		{name: "blank text", give: map[string]any{"chat": "альфа", "text": "  "}},
		{name: "unknown chat", give: map[string]any{"chat": "нет такого", "text": "hi"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if res := e.call("send_message", tt.give, nil); !res.IsError {
				t.Fatal("want a tool error")
			}
		})
	}
}

func TestToolsExposed(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	readTools := []string{
		"get_message_context",
		"get_messages",
		"get_new_messages",
		"get_status",
		"list_chats",
		"search_messages",
	}
	tests := []struct {
		name string
		opts mcpserver.Options
		want []string
	}{
		{
			name: "send_message is opt-in",
			opts: mcpserver.Options{Store: db},
			want: readTools,
		},
		{
			name: "send_message with a sender",
			opts: mcpserver.Options{Store: db, Sender: telegram.New(_token, "")},
			want: append([]string{"send_message"}, readTools...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st, ct := mcp.NewInMemoryTransports()
			if _, err := mcpserver.New(tt.opts).Connect(ctx, st, nil); err != nil {
				t.Fatal(err)
			}
			session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			res, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(res.Tools))
			for _, tool := range res.Tools {
				got = append(got, tool.Name)
			}
			slices.Sort(got)
			want := slices.Sorted(slices.Values(tt.want))
			if !slices.Equal(got, want) {
				t.Fatalf("tools = %v, want %v", got, want)
			}
		})
	}
}

func TestServerVersion(t *testing.T) {
	tests := []struct {
		name string
		give string
		want string
	}{
		{name: "injected", give: "v1.2.3", want: "v1.2.3"},
		{name: "default", give: "", want: "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			ctx := context.Background()
			st, ct := mcp.NewInMemoryTransports()
			srv := mcpserver.New(mcpserver.Options{Store: db, Version: tt.give})
			if _, err := srv.Connect(ctx, st, nil); err != nil {
				t.Fatal(err)
			}
			session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			if got := session.InitializeResult().ServerInfo.Version; got != tt.want {
				t.Fatalf("server version = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOffsetIsPersisted(t *testing.T) {
	e := setup(t)
	offset, err := e.store.Meta(context.Background(), collector.MetaOffset)
	if err != nil || offset != "12" {
		t.Fatalf("offset = %q, %v; want 12", offset, err)
	}
}
