package mcpserver_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/mcpserver"
	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

const _export = `{"name": "Иван", "type": "personal_chat", "id": 111, "messages": [
 {"id": 1, "type": "message", "date_unixtime": "1704103200", "from": "Иван", "from_id": "user111",
  "text": "старое сообщение"},
 {"id": 2, "type": "message", "date_unixtime": "1704103260", "from": "Иван", "from_id": "user111",
  "forwarded_from": "Новости", "text": "пересланное"},
 {"id": 3, "type": "service", "date_unixtime": "1704103300", "action": "phone_call", "text": ""}
]}`

// newImportEnv connects a client to a server whose import directory is a
// fresh temporary directory, and returns that directory.
func newImportEnv(t *testing.T) (*env, string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	dir := filepath.Join(t.TempDir(), "import")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	srv := mcpserver.New(mcpserver.Options{Store: db, ImportDir: dir})
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return &env{t: t, store: db, client: session}, dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type importResult struct {
	Directory string   `json:"directory"`
	Files     []string `json:"available_files"`
	Chats     []struct {
		ChatID        int64  `json:"chat_id"`
		Chat          string `json:"chat"`
		Imported      int64  `json:"imported"`
		AlreadyStored int64  `json:"already_stored"`
		Skipped       int64  `json:"skipped"`
	} `json:"chats"`
}

func TestImportChatExport(t *testing.T) {
	e, dir := newImportEnv(t)
	write(t, filepath.Join(dir, "result.json"), _export)
	write(t, filepath.Join(dir, "ivan", "result.json"), _export)
	write(t, filepath.Join(dir, "notes.txt"), "not an export")

	var listed importResult
	e.call("import_chat_export", nil, &listed)
	if listed.Directory != dir || !slices.Equal(listed.Files, []string{"ivan/result.json", "result.json"}) {
		t.Fatalf("listing = %+v", listed)
	}

	var first importResult
	e.call("import_chat_export", map[string]any{"file": "result.json"}, &first)
	if len(first.Chats) != 1 || first.Chats[0].ChatID != 111 || first.Chats[0].Chat != "Иван" ||
		first.Chats[0].Imported != 2 || first.Chats[0].Skipped != 1 {
		t.Fatalf("first import = %+v", first.Chats)
	}
	var again importResult
	e.call("import_chat_export", map[string]any{"file": "ivan/result.json"}, &again)
	if again.Chats[0].Imported != 0 || again.Chats[0].AlreadyStored != 2 {
		t.Fatalf("repeated import = %+v", again.Chats)
	}

	// The history is readable and searchable, and forwards are marked.
	var msgs mcpserver.MessagesOutput
	e.call("get_messages", map[string]any{"chat": "Иван"}, &msgs)
	if !slices.Equal(ids(msgs), []int64{1, 2}) {
		t.Fatalf("imported messages: %v", ids(msgs))
	}
	if msgs.Messages[0].ForwardedFrom != "" || msgs.Messages[1].ForwardedFrom != "Новости" {
		t.Errorf("forwarded_from = %q, %q", msgs.Messages[0].ForwardedFrom, msgs.Messages[1].ForwardedFrom)
	}
	e.call("get_messages", map[string]any{"chat": "111", "forwarded_only": true}, &msgs)
	if !slices.Equal(ids(msgs), []int64{2}) {
		t.Errorf("forwarded_only: %v", ids(msgs))
	}
	e.call("search_messages", map[string]any{"query": "старое"}, &msgs)
	if !slices.Equal(ids(msgs), []int64{1}) {
		t.Errorf("search over imported text: %v", ids(msgs))
	}

	// Imported history must not flood the inbox feed, even for a consumer
	// that already has a position.
	e.call("get_new_messages", map[string]any{"consumer": "known"}, &msgs)
	live := store.Message{ChatID: 111, ID: 50, Date: time.Now(), Text: "живое"}
	if err := e.store.SaveMessage(context.Background(), live); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "late.json"), strings.ReplaceAll(_export, `"id": 1,`, `"id": 60,`))
	e.call("import_chat_export", map[string]any{"file": "late.json"}, &again)
	if again.Chats[0].Imported != 1 {
		t.Fatalf("late import = %+v", again.Chats)
	}
	e.call("get_new_messages", map[string]any{"consumer": "known"}, &msgs)
	if !slices.Equal(ids(msgs), []int64{50}) {
		t.Errorf("feed after import = %v, want only the live message", ids(msgs))
	}
}

func TestImportChatExportIsConfinedToItsDirectory(t *testing.T) {
	e, dir := newImportEnv(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.json")
	write(t, outside, _export)
	if err := os.Symlink(outside, filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "broken.json"), `{"name": "x"`)

	tests := []struct {
		name string
		give string
	}{
		{name: "parent directory", give: "../outside.json"},
		{name: "absolute path", give: outside},
		{name: "symlink out of the directory", give: "link.json"},
		{name: "missing file", give: "nope.json"},
		{name: "not an export", give: "broken.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := e.call("import_chat_export", map[string]any{"file": tt.give}, nil)
			if !res.IsError {
				t.Fatalf("import of %q should fail", tt.give)
			}
		})
	}

	stats, err := e.store.Stats(context.Background())
	if err != nil || stats.Messages != 0 {
		t.Fatalf("nothing should be imported, got %+v, %v", stats, err)
	}
}

func TestImportToolNeedsADirectory(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name      string
		importDir string
		want      bool
	}{
		{name: "disabled", importDir: "", want: false},
		{name: "enabled", importDir: t.TempDir(), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, ct := mcp.NewInMemoryTransports()
			srv := mcpserver.New(mcpserver.Options{Store: db, ImportDir: tt.importDir})
			if _, err := srv.Connect(ctx, st, nil); err != nil {
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
			got := slices.ContainsFunc(res.Tools, func(tool *mcp.Tool) bool {
				return tool.Name == "import_chat_export"
			})
			if got != tt.want {
				t.Errorf("import_chat_export offered = %v, want %v", got, tt.want)
			}
		})
	}
}
