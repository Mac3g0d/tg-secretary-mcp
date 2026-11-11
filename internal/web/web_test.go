package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d, want %d: %s", path, res.StatusCode, wantStatus, body)
	}
	return string(body), res.Header
}

var (
	_textRe = regexp.MustCompile(`(?s)<article.*?</article>`)
	_tagRe  = regexp.MustCompile(`<[^>]+>`)
	_bodyRe = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
)

// texts returns the message bodies on the page, in order, without markup.
func texts(html string) []string {
	out := make([]string, 0, 64)
	for _, article := range _textRe.FindAllString(html, -1) {
		if m := _bodyRe.FindStringSubmatch(article); m != nil {
			out = append(out, _tagRe.ReplaceAllString(m[1], ""))
		}
	}
	return out
}

func TestPages(t *testing.T) {
	ts := newServer(t)
	q := "%D0%B4%D0%BE%D0%B3%D0%BE%D0%B2" // "догов"
	const script = "&lt;script&gt;alert(1)&lt;/script&gt;"
	tests := []struct {
		name string
		give string
		want []string
	}{
		{
			name: "chat is chronological",
			give: "/?chat=1",
			want: []string{"привет, как дела", "договор готов", script},
		},
		{
			name: "deleted hidden",
			give: "/?chat=2",
			want: []string{"Договор подпишем завтра"},
		},
		{
			name: "deleted shown",
			give: "/?chat=2&deleted=1",
			want: []string{"Договор подпишем завтра", "секрет"},
		},
		{
			name: "search is chronological too",
			give: "/?q=" + q,
			want: []string{"договор готов", "Договор подпишем завтра"},
		},
		{
			name: "search in chat",
			give: "/?chat=1&q=" + q,
			want: []string{"договор готов"},
		},
		{
			name: "search everywhere from chat",
			give: "/?chat=1&everywhere=1&q=" + q,
			want: []string{"договор готов", "Договор подпишем завтра"},
		},
		{
			name: "context",
			give: "/?chat=1&around=2",
			want: []string{"привет, как дела", "договор готов", script},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := get(t, ts, tt.give, http.StatusOK)
			if got := texts(body); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEscapingAndHeaders(t *testing.T) {
	ts := newServer(t)
	body, header := get(t, ts, "/?chat=1", http.StatusOK)
	if strings.Contains(body, "<script") {
		t.Error("message text was not escaped")
	}

	headerTests := []struct {
		name   string
		header string
		want   string
	}{
		{"CSP forbids scripts", "Content-Security-Policy", "script-src 'none'"},
		{"no sniffing", "X-Content-Type-Options", "nosniff"},
		{"no referrer", "Referrer-Policy", "no-referrer"},
		{"no caching", "Cache-Control", "no-store"},
	}
	for _, tt := range headerTests {
		t.Run(tt.name, func(t *testing.T) {
			if got := header.Get(tt.header); !strings.Contains(got, tt.want) {
				t.Errorf("%s = %q, want it to contain %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestHighlightInPage(t *testing.T) {
	ts := newServer(t)
	body, _ := get(t, ts, "/?q=%D0%94%D0%9E%D0%93%D0%9E%D0%92", http.StatusOK) // "ДОГОВ"

	wants := []string{
		"<mark>Договор</mark> подпишем",
		"<mark>договор</mark> готов",
		`href="/?around=1&amp;chat=2#target"`, // results link to the message in context
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q:\n%s", want,
				strings.Join(_textRe.FindAllString(body, -1), "\n"))
		}
	}
}

func TestPaging(t *testing.T) {
	ts := newServer(t)
	tests := []struct {
		name      string
		give      string
		wantCount int
		wantFirst string
		wantLast  string
		wantLinks []string
		noLinks   []string
	}{
		{
			name:      "chat view: latest 50 chronologically, older via top link",
			give:      "/?chat=3",
			wantCount: 50,
			wantFirst: "флуд номер 71",
			wantLast:  "флуд номер 120",
			wantLinks: []string{`<a href="/?chat=3&amp;offset=50">↑ Более ранние</a>`},
			noLinks:   []string{"Более поздние"},
		},
		{
			name:      "chat view: last page links only to newer",
			give:      "/?chat=3&offset=100",
			wantCount: 20,
			wantFirst: "флуд номер 1",
			wantLast:  "флуд номер 20",
			wantLinks: []string{`<a href="/?chat=3&amp;offset=50">Более поздние ↓</a>`},
			noLinks:   []string{"Более ранние"},
		},
		{
			name:      "feed: newest at the bottom, older via top link",
			give:      "/",
			wantCount: 50,
			wantFirst: "флуд номер 71",
			wantLast:  "флуд номер 120",
			wantLinks: []string{`<a href="/?offset=50">↑ Более ранние</a>`},
			noLinks:   []string{"Более поздние"},
		},
		{
			name:      "feed: second page links both ways",
			give:      "/?offset=50",
			wantCount: 50,
			wantFirst: "флуд номер 21",
			wantLast:  "флуд номер 70",
			wantLinks: []string{
				`<a href="/?offset=100">↑ Более ранние</a>`,
				`<a href="/">Более поздние ↓</a>`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := get(t, ts, tt.give, http.StatusOK)
			got := texts(body)
			if len(got) != tt.wantCount {
				t.Fatalf("%d messages, want %d", len(got), tt.wantCount)
			}
			if got[0] != tt.wantFirst || got[len(got)-1] != tt.wantLast {
				t.Fatalf("messages %q … %q, want %q … %q",
					got[0], got[len(got)-1], tt.wantFirst, tt.wantLast)
			}
			for _, want := range tt.wantLinks {
				if !strings.Contains(body, want) {
					t.Errorf("missing pager link %s", want)
				}
			}
			for _, bad := range tt.noLinks {
				if strings.Contains(body, bad) {
					t.Errorf("unexpected pager link %q", bad)
				}
			}
		})
	}
}

func TestStatuses(t *testing.T) {
	ts := newServer(t)
	tests := []struct {
		give string
		want int
	}{
		{"/?chat=abc", http.StatusBadRequest},
		{"/?around=1", http.StatusBadRequest},
		{"/?offset=-1", http.StatusBadRequest},
		{"/?chat=1&around=99", http.StatusNotFound},
		{"/nope", http.StatusNotFound},
		{"/static/style.css", http.StatusOK},
		{"/static/", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			get(t, ts, tt.give, tt.want)
		})
	}

	t.Run("POST is not allowed", func(t *testing.T) {
		res, err := http.Post(ts.URL+"/", "text/plain", nil)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status %d, want 405", res.StatusCode)
		}
	})
}

func TestHighlight(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		terms []string
		want  []segment
	}{
		{name: "empty text", text: "", terms: []string{"a"}, want: nil},
		{name: "no terms", text: "plain", terms: nil, want: []segment{{Text: "plain"}}},
		{
			name:  "word prefix, case-insensitive",
			text:  "Счёт оплачен, счета нет",
			terms: []string{"счёт"},
			want:  []segment{{Text: "Счёт", Mark: true}, {Text: " оплачен, счета нет"}},
		},
		{
			name:  "several terms",
			text:  "a bc abc",
			terms: []string{"ab", "b"},
			want: []segment{
				{Text: "a "},
				{Text: "bc", Mark: true},
				{Text: " "},
				{Text: "abc", Mark: true},
			},
		},
		{name: "no match", text: "hello", terms: []string{"x"}, want: []segment{{Text: "hello"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := highlight(tt.text, tt.terms)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("highlight(%q, %q) = %+v, want %+v", tt.text, tt.terms, got, tt.want)
			}
		})
	}
}

func TestParseRequest(t *testing.T) {
	tests := []struct {
		name   string
		give   string
		want   request
		wantOK bool
	}{
		{name: "empty", give: "", want: request{}, wantOK: true},
		{
			name:   "all parameters",
			give:   "chat=-5&q=+hi+&everywhere=1&deleted=1&offset=100&around=7",
			want:   request{chatID: -5, query: "hi", everywhere: true, deleted: true, offset: 100, around: 7},
			wantOK: true,
		},
		{name: "bad chat", give: "chat=x"},
		{name: "around without chat", give: "around=3"},
		{name: "bad around", give: "chat=1&around=x"},
		{name: "negative offset", give: "offset=-1"},
		{name: "bad offset", give: "offset=x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := url.ParseQuery(tt.give)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := parseRequest(v)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRequestURL(t *testing.T) {
	tests := []struct {
		name string
		give request
		want string
	}{
		{name: "empty", give: request{}, want: "/"},
		{name: "chat", give: request{chatID: 3}, want: "/?chat=3"},
		{
			name: "everywhere needs query and chat",
			give: request{everywhere: true},
			want: "/",
		},
		{
			name: "everywhere with query and chat",
			give: request{chatID: 1, query: "a", everywhere: true},
			want: "/?chat=1&everywhere=1&q=a",
		},
		{
			name: "around adds a fragment",
			give: request{chatID: 2, around: 9, deleted: true},
			want: "/?around=9&chat=2&deleted=1#target",
		},
		{name: "offset", give: request{offset: 50}, want: "/?offset=50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.give.url(); got != tt.want {
				t.Errorf("url() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestForwardLabel(t *testing.T) {
	date := time.Date(2026, 1, 2, 15, 4, 0, 0, time.Local)
	tests := []struct {
		name string
		give store.Message
		want string
	}{
		{
			name: "name and date",
			give: store.Message{ForwardFrom: "Иван", ForwardDate: date},
			want: "переслано от Иван, 02.01.2026 15:04",
		},
		{
			name: "username only",
			give: store.Message{ForwardFromUsername: "news", ForwardDate: date},
			want: "переслано от @news, 02.01.2026 15:04",
		},
		{
			name: "no origin name",
			give: store.Message{ForwardDate: date},
			want: "переслано, 02.01.2026 15:04",
		},
		{
			name: "no date",
			give: store.Message{ForwardFrom: "Иван"},
			want: "переслано от Иван",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forwardLabel(tt.give); got != tt.want {
				t.Errorf("forwardLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestForwardedMessageIsMarked(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.UpsertChat(ctx, store.Chat{ID: 1, Title: "Чат"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.Message{
		{ChatID: 1, ID: 1, Date: time.Now(), Text: "своё"},
		{ChatID: 1, ID: 2, Date: time.Now(), Text: "чужое",
			ForwardFrom: "<b>Иван</b>", ForwardDate: time.Now()},
	} {
		if err := db.SaveMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	h, err := NewHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	body, _ := get(t, ts, "/?chat=1", http.StatusOK)
	if got := strings.Count(body, `class="forward"`); got != 1 {
		t.Errorf("%d forward labels, want 1", got)
	}
	if !strings.Contains(body, "переслано от &lt;b&gt;Иван&lt;/b&gt;") {
		t.Error("forward origin is missing or not escaped")
	}
}

func TestAvatars(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, c := range []store.Chat{{ID: 1, Title: "анна"}, {ID: -100200, Title: "«Проект»"}} {
		if err := db.UpsertChat(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	picture := []byte("\x89PNG\r\n\x1a\n-fake-")
	err = db.SavePhoto(ctx, 1, store.Photo{UniqueID: "v 1", ContentType: "image/png", Data: picture})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	page, _ := get(t, ts, "/", http.StatusOK)
	if !strings.Contains(page, `<img class="avatar" src="/avatar/1?v=v&#43;1"`) {
		t.Errorf("chat with a picture should show it:\n%s", page)
	}
	// No picture: the first letter of the title, skipping punctuation.
	if !strings.Contains(page, `aria-hidden="true">П</span>`) {
		t.Error("chat without a picture should show its initial")
	}

	body, header := get(t, ts, "/avatar/1?v=anything", http.StatusOK)
	if body != string(picture) || header.Get("Content-Type") != "image/png" {
		t.Errorf("avatar response: %q, %s", body, header.Get("Content-Type"))
	}
	if cc := header.Get("Cache-Control"); !strings.Contains(cc, "private") || !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("avatar responses must not be sniffed")
	}

	for _, path := range []string{"/avatar/-100200", "/avatar/999", "/avatar/abc", "/avatar/"} {
		get(t, ts, path, http.StatusNotFound)
	}
}

func TestInitial(t *testing.T) {
	tests := []struct {
		give string
		want string
	}{
		{give: "анна", want: "А"},
		{give: "«Проект» 2", want: "П"},
		{give: "42-й чат", want: "4"},
		{give: "🙂 smile", want: "S"},
		{give: "…", want: "#"},
		{give: "", want: "#"},
	}
	for _, tt := range tests {
		if got := initial(tt.give); got != tt.want {
			t.Errorf("initial(%q) = %q, want %q", tt.give, got, tt.want)
		}
	}
}
