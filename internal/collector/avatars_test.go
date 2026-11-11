package collector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
)

const _testToken = "1:SECRET"

// _png is a valid 1x1 PNG.
var _png = mustDecode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustDecode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// photoAPI imitates the Bot API methods behind avatars. photos maps a chat
// ID to the unique ID of its picture; a missing chat is unknown to the bot.
type photoAPI struct {
	mu      sync.Mutex
	photos  map[int64]string
	files   map[string][]byte // served content by file path
	calls   map[string]int
	tooMany bool
}

func (a *photoAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if path, ok := strings.CutPrefix(r.URL.Path, "/file/bot"+_testToken+"/"); ok {
		a.calls["download"]++
		w.Write(a.files[path])
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/bot"+_testToken+"/")
	a.calls[method]++
	body, _ := io.ReadAll(r.Body)
	var params struct {
		ChatID int64  `json:"chat_id"`
		FileID string `json:"file_id"`
	}
	_ = json.Unmarshal(body, &params)

	switch method {
	case "getChat":
		if a.tooMany {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`)
			return
		}
		unique, known := a.photos[params.ChatID]
		switch {
		case !known:
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
		case unique == "":
			io.WriteString(w, `{"ok":true,"result":{"id":1}}`)
		default:
			io.WriteString(w, `{"ok":true,"result":{"id":1,"photo":{"small_file_id":"id-`+unique+
				`","small_file_unique_id":"`+unique+`"}}}`)
		}
	case "getFile":
		io.WriteString(w, `{"ok":true,"result":{"file_path":"photos/`+params.FileID+`.jpg"}}`)
	default:
		http.NotFound(w, r)
	}
}

func newAvatarCollector(t *testing.T, api *photoAPI) (*Collector, *store.Store) {
	t.Helper()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	c, db := newTestCollector(t, nil)
	c.api = telegram.New(_testToken, ts.URL)
	return c, db
}

func TestRefreshAvatars(t *testing.T) {
	ctx := context.Background()
	api := &photoAPI{
		photos: map[int64]string{1: "v1", 2: "", 4: "html"},
		files: map[string][]byte{
			"photos/id-v1.jpg":   _png,
			"photos/id-v2.jpg":   append(_png[:len(_png):len(_png)], 0),
			"photos/id-html.jpg": []byte("<html><script>alert(1)</script></html>"),
		},
		calls: map[string]int{},
	}
	c, db := newAvatarCollector(t, api)
	for id, title := range map[int64]string{1: "с фото", 2: "без фото", 3: "бот не видит", 4: "не картинка"} {
		if err := db.UpsertChat(ctx, store.Chat{ID: id, Title: title}); err != nil {
			t.Fatal(err)
		}
	}

	if wait := c.refreshAvatars(ctx); wait != 0 {
		t.Fatalf("retryAfter = %v, want 0", wait)
	}
	tests := []struct {
		name   string
		chatID int64
		want   bool
	}{
		{name: "picture is stored", chatID: 1, want: true},
		{name: "chat without a picture", chatID: 2, want: false},
		{name: "chat the bot cannot see", chatID: 3, want: false},
		{name: "non-image content is refused", chatID: 4, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			photo, ok, err := db.Photo(ctx, tt.chatID)
			if err != nil || ok != tt.want {
				t.Fatalf("Photo() ok = %v, %v; want %v", ok, err, tt.want)
			}
			if ok && (photo.UniqueID != "v1" || photo.ContentType != "image/png" || len(photo.Data) != len(_png)) {
				t.Errorf("stored photo = %s %s %d bytes", photo.UniqueID, photo.ContentType, len(photo.Data))
			}
		})
	}

	// Everything was looked up, so a second round asks Telegram nothing.
	before := api.calls["getChat"]
	c.refreshAvatars(ctx)
	if api.calls["getChat"] != before {
		t.Errorf("fresh chats were looked up again: %d extra getChat calls", api.calls["getChat"]-before)
	}

	// A day later: the unchanged picture is not downloaded again, a new one is,
	// and a removed one disappears.
	expire := func() {
		t.Helper()
		due, err := db.PhotosToCheck(ctx, time.Now().Add(time.Hour), 100)
		if err != nil || len(due) != 4 {
			t.Fatalf("PhotosToCheck = %+v, %v; want all 4 chats", due, err)
		}
	}
	expire()
	downloads := api.calls["download"]
	refreshAll := func() {
		t.Helper()
		due, err := db.PhotosToCheck(ctx, time.Now().Add(time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, chat := range due {
			if err := c.refreshAvatar(ctx, chat); err != nil && chat.ChatID == 1 {
				t.Fatal(err)
			}
		}
	}
	refreshAll()
	if api.calls["download"] != downloads+1 { // only the non-image one is retried
		t.Errorf("unchanged picture was downloaded again (%d downloads)", api.calls["download"]-downloads)
	}

	api.mu.Lock()
	api.photos[1] = "v2"
	api.mu.Unlock()
	refreshAll()
	if photo, ok, _ := db.Photo(ctx, 1); !ok || photo.UniqueID != "v2" || len(photo.Data) != len(_png)+1 {
		t.Errorf("changed picture was not replaced: %+v %v", photo.UniqueID, ok)
	}

	api.mu.Lock()
	api.photos[1] = ""
	api.mu.Unlock()
	refreshAll()
	if _, ok, _ := db.Photo(ctx, 1); ok {
		t.Error("removed picture is still stored")
	}
}

func TestRefreshAvatarsBacksOffWhenRateLimited(t *testing.T) {
	ctx := context.Background()
	api := &photoAPI{photos: map[int64]string{}, calls: map[string]int{}, tooMany: true}
	c, db := newAvatarCollector(t, api)
	for id := int64(1); id <= 3; id++ {
		if err := db.UpsertChat(ctx, store.Chat{ID: id}); err != nil {
			t.Fatal(err)
		}
	}

	if wait := c.refreshAvatars(ctx); wait != 7*time.Second {
		t.Fatalf("retryAfter = %v, want 7s", wait)
	}
	if api.calls["getChat"] != 1 {
		t.Errorf("%d getChat calls after a 429, want 1", api.calls["getChat"])
	}
	// Nothing was marked as checked, so the chats are retried later.
	due, err := db.PhotosToCheck(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil || len(due) != 3 {
		t.Errorf("PhotosToCheck = %+v, %v; want 3 chats still due", due, err)
	}
}

func TestRunStopsAvatarFetcher(t *testing.T) {
	api := &photoAPI{photos: map[int64]string{}, calls: map[string]int{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"id":999,"is_bot":true,"username":"b"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			io.WriteString(w, `{"ok":true,"result":[]}`)
		default:
			api.ServeHTTP(w, r)
		}
	}))
	defer ts.Close()

	_, db := newTestCollector(t, nil)
	if err := db.UpsertChat(context.Background(), store.Chat{ID: 1}); err != nil {
		t.Fatal(err)
	}
	c := New(Config{
		API:            telegram.New(_testToken, ts.URL),
		Store:          db,
		Avatars:        true,
		AvatarInterval: 5 * time.Millisecond,
		PollTimeout:    time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for {
		api.mu.Lock()
		looked := api.calls["getChat"] > 0
		api.mu.Unlock()
		if looked {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the avatar fetcher never ran")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}
