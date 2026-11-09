package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	for _, c := range []store.Chat{
		{ID: 1, Type: "private", Title: "Анна"},
		{ID: 2, Type: "group", Title: "Проект"},
		{ID: 3, Type: "group", Title: "Флуд"},
	} {
		if err := db.UpsertChat(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-24 * time.Hour)
	msgs := []store.Message{
		{ChatID: 1, ID: 1, Text: "привет, как дела"},
		{ChatID: 1, ID: 2, Text: "договор готов"},
		{ChatID: 1, ID: 3, Text: "<script>alert(1)</script>"},
		{ChatID: 2, ID: 1, Text: "Договор подпишем завтра"},
		{ChatID: 2, ID: 2, Text: "секрет"},
	}
	for i := 1; i <= 120; i++ {
		msgs = append(msgs, store.Message{ChatID: 3, ID: int64(i), Text: "флуд номер " + strconv.Itoa(i)})
	}
	for i, m := range msgs {
		m.Date = base.Add(time.Duration(i) * time.Minute)
		m.SenderName = "Тест"
		if err := db.SaveMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.MarkDeleted(ctx, 2, []int64{2}); err != nil {
		t.Fatal(err)
	}

	h, err := NewHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, ts *httptest.Server, path string, wantStatus int) (string, http.Header) {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d, want %d: %s", path, res.StatusCode, wantStatus, body)
	}
	return string(body), res.Header
}

var (
	textRe = regexp.MustCompile(`(?s)<article.*?</article>`)
	tagRe  = regexp.MustCompile(`<[^>]+>`)
	bodyRe = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
)

// texts returns the message bodies on the page, in order, without markup.
func texts(html string) []string {
	var out []string
	for _, article := range textRe.FindAllString(html, -1) {
		if m := bodyRe.FindStringSubmatch(article); m != nil {
			out = append(out, tagRe.ReplaceAllString(m[1], ""))
		}
	}
	return out
}

func TestPages(t *testing.T) {
	ts := newServer(t)
	q := "%D0%B4%D0%BE%D0%B3%D0%BE%D0%B2" // "догов"
	tests := []struct {
		name, path string
		want       []string
	}{
		{"chat is chronological", "/?chat=1", []string{"привет, как дела", "договор готов", "&lt;script&gt;alert(1)&lt;/script&gt;"}},
		{"deleted hidden", "/?chat=2", []string{"Договор подпишем завтра"}},
		{"deleted shown", "/?chat=2&deleted=1", []string{"Договор подпишем завтра", "секрет"}},
		{"search newest first", "/?q=" + q, []string{"Договор подпишем завтра", "договор готов"}},
		{"search in chat", "/?chat=1&q=" + q, []string{"договор готов"}},
		{"search everywhere from chat", "/?chat=1&everywhere=1&q=" + q, []string{"Договор подпишем завтра", "договор готов"}},
		{"context", "/?chat=1&around=2", []string{"привет, как дела", "договор готов", "&lt;script&gt;alert(1)&lt;/script&gt;"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := get(t, ts, tt.path, http.StatusOK)
			if got := texts(body); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEscapingAndHighlight(t *testing.T) {
	ts := newServer(t)
	body, header := get(t, ts, "/?chat=1", http.StatusOK)
	if strings.Contains(body, "<script") {
		t.Error("message text was not escaped")
	}
	if csp := header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	body, _ = get(t, ts, "/?q=%D0%94%D0%9E%D0%93%D0%9E%D0%92", http.StatusOK) // "ДОГОВ"
	if !strings.Contains(body, "<mark>Договор</mark> подпишем") || !strings.Contains(body, "<mark>договор</mark> готов") {
		t.Errorf("search hits are not highlighted:\n%s", strings.Join(textRe.FindAllString(body, -1), "\n"))
	}
	if !strings.Contains(body, `href="/?around=1&amp;chat=2#target"`) {
		t.Error("search results should link to the message in context")
	}
}

func TestPaging(t *testing.T) {
	ts := newServer(t)
	// Chat view: the latest 50 in chronological order, older pages via the top link.
	body, _ := get(t, ts, "/?chat=3", http.StatusOK)
	got := texts(body)
	if len(got) != 50 || got[0] != "флуд номер 71" || got[49] != "флуд номер 120" {
		t.Fatalf("first page: %d messages, %q … %q", len(got), got[0], got[len(got)-1])
	}
	if !strings.Contains(body, `<a href="/?chat=3&amp;offset=50">← Более ранние</a>`) || strings.Contains(body, "Более поздние") {
		t.Error("first page should only link to older messages")
	}
	body, _ = get(t, ts, "/?chat=3&offset=100", http.StatusOK)
	got = texts(body)
	if len(got) != 20 || got[0] != "флуд номер 1" || got[19] != "флуд номер 20" {
		t.Fatalf("last page: %d messages, %q … %q", len(got), got[0], got[len(got)-1])
	}
	if strings.Contains(body, "Более ранние") || !strings.Contains(body, `<a href="/?chat=3&amp;offset=50">Более поздние →</a>`) {
		t.Error("last page should only link to newer messages")
	}
	// Feed: newest first, older pages via the bottom link.
	body, _ = get(t, ts, "/", http.StatusOK)
	got = texts(body)
	if len(got) != 50 || got[0] != "флуд номер 120" {
		t.Fatalf("feed: %d messages, first %q", len(got), got[0])
	}
	if !strings.Contains(body, `<a href="/?offset=50">Более ранние →</a>`) {
		t.Error("feed should link to older messages")
	}
}

func TestBadRequests(t *testing.T) {
	ts := newServer(t)
	for path, status := range map[string]int{
		"/?chat=abc":         http.StatusBadRequest,
		"/?around=1":         http.StatusBadRequest,
		"/?offset=-1":        http.StatusBadRequest,
		"/?chat=1&around=99": http.StatusNotFound,
		"/nope":              http.StatusNotFound,
		"/static/style.css":  http.StatusOK,
		"/static/":           http.StatusOK,
	} {
		get(t, ts, path, status)
	}
	res, err := http.Post(ts.URL+"/", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST: status %d, want 405", res.StatusCode)
	}
}

func TestHighlight(t *testing.T) {
	tests := []struct {
		text  string
		terms []string
		want  []segment
	}{
		{"", []string{"a"}, nil},
		{"plain", nil, []segment{{Text: "plain"}}},
		{"Счёт оплачен, счета нет", []string{"счёт"}, []segment{{Text: "Счёт", Mark: true}, {Text: " оплачен, счета нет"}}},
		{"a bc abc", []string{"ab", "b"}, []segment{{Text: "a "}, {Text: "bc", Mark: true}, {Text: " "}, {Text: "abc", Mark: true}}},
	}
	for _, tt := range tests {
		if got := highlight(tt.text, tt.terms); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("highlight(%q, %q) = %+v, want %+v", tt.text, tt.terms, got, tt.want)
		}
	}
}
