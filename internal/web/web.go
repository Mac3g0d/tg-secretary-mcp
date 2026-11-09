// Package web serves a minimal, JavaScript-free browser UI for viewing and
// searching the message archive. Pages are rendered on the server with
// html/template, which escapes the (untrusted) message content.
package web

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

var (
	//go:embed static
	staticFiles embed.FS
	//go:embed templates
	templateFiles embed.FS

	wordRe = regexp.MustCompile(`[\p{L}\p{N}_]+`)
)

const (
	pageSize = 50
	// contextWindow is how many messages are shown on each side of a
	// message opened from the search results.
	contextWindow = 25
	dateLayout    = "02.01.2006 15:04"
	previewLength = 80
)

// page is the data rendered by templates/index.html.tmpl.
type page struct {
	Title      string
	Chats      []chatView
	ChatID     int64 // 0 when no chat is selected
	Query      string
	Everywhere bool // search all chats although one is selected
	Deleted    bool
	Messages   []messageView
	Notice     string
	Empty      string // shown instead of an empty message list
	Top        *link  // pager above the messages
	Bottom     *link  // pager below the messages
}

type link struct {
	URL   string
	Label string
}

// pagerLink returns nil for an empty URL so the template can skip it.
func pagerLink(url, label string) *link {
	if url == "" {
		return nil
	}
	return &link{URL: url, Label: label}
}

type chatView struct {
	Title   string
	Count   int64
	Preview string
	URL     string
	Active  bool
}

type messageView struct {
	Sender     string
	Chat       string
	ShowChat   bool
	Date       string
	DateISO    string
	Media      string
	Segments   []segment
	ContextURL string
	Outgoing   bool
	Edited     bool
	Deleted    bool
	Target     bool
}

// segment is a piece of message text; Mark is set for search hits.
type segment struct {
	Text string
	Mark bool
}

type handler struct {
	store *store.Store
	log   *slog.Logger
	tmpl  *template.Template
}

// NewHandler returns the UI handler. It serves the page at / and its
// stylesheet under /static/. The page is driven by query parameters:
//
//	chat        numeric chat id to show
//	q           full-text search
//	everywhere  search all chats even though chat is set
//	deleted     include deleted messages
//	offset      paging
//	around      message id to show in context (requires chat)
func NewHandler(s *store.Store, log *slog.Logger) (http.Handler, error) {
	tmpl, err := template.ParseFS(templateFiles, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}
	h := &handler{store: s, log: log, tmpl: tmpl}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	return securityHeaders(mux), nil
}

// securityHeaders locks the page down: nothing but same-origin resources may
// load, and no script runs at all.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// request is the parsed query string of the page.
type request struct {
	chatID     int64
	query      string
	everywhere bool
	deleted    bool
	offset     int
	around     int64
}

func parseRequest(v url.Values) (request, bool) {
	r := request{
		query:      strings.TrimSpace(v.Get("q")),
		everywhere: v.Get("everywhere") == "1",
		deleted:    v.Get("deleted") == "1",
	}
	var err error
	if s := v.Get("chat"); s != "" {
		if r.chatID, err = strconv.ParseInt(s, 10, 64); err != nil {
			return r, false
		}
	}
	if s := v.Get("around"); s != "" {
		if r.around, err = strconv.ParseInt(s, 10, 64); err != nil || r.chatID == 0 {
			return r, false
		}
	}
	if s := v.Get("offset"); s != "" {
		if r.offset, err = strconv.Atoi(s); err != nil || r.offset < 0 {
			return r, false
		}
	}
	return r, true
}

// url builds a link to the page for this request.
func (r request) url() string {
	v := url.Values{}
	if r.chatID != 0 {
		v.Set("chat", strconv.FormatInt(r.chatID, 10))
	}
	if r.query != "" {
		v.Set("q", r.query)
	}
	if r.everywhere && r.query != "" && r.chatID != 0 {
		v.Set("everywhere", "1")
	}
	if r.deleted {
		v.Set("deleted", "1")
	}
	if r.offset > 0 {
		v.Set("offset", strconv.Itoa(r.offset))
	}
	if r.around != 0 {
		v.Set("around", strconv.FormatInt(r.around, 10))
	}
	if len(v) == 0 {
		return "/"
	}
	link := "/?" + v.Encode()
	if r.around != 0 {
		link += "#target"
	}
	return link
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	req, ok := parseRequest(r.URL.Query())
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	chats, err := h.store.Chats(ctx, "", "", store.MaxLimit)
	if err != nil {
		h.fail(w, err)
		return
	}
	p := page{
		Title:      "Все сообщения",
		ChatID:     req.chatID,
		Query:      req.query,
		Everywhere: req.everywhere,
		Deleted:    req.deleted,
		Empty:      "Сообщений пока нет",
	}
	for _, c := range chats {
		title := c.Title
		if title == "" {
			title = strconv.FormatInt(c.ID, 10)
		}
		active := c.ID == req.chatID
		if active {
			p.Title = title
		}
		p.Chats = append(p.Chats, chatView{
			Title:   title,
			Count:   c.MessageCount,
			Preview: truncate(c.LastText, previewLength),
			URL:     request{chatID: c.ID}.url(),
			Active:  active,
		})
	}

	// A chat without a search reads top to bottom like a messenger; search
	// results and the combined feed are newest first.
	searching := req.query != ""
	chronological := req.chatID != 0 && !searching
	var msgs []store.Message
	switch {
	case req.around != 0:
		msgs, err = h.store.Context(ctx, req.chatID, req.around, contextWindow, contextWindow)
		if err != nil {
			http.Error(w, "message not found", http.StatusNotFound)
			return
		}
		p.Notice = "Показан фрагмент переписки."
	default:
		q := store.Query{
			Text:           req.query,
			IncludeDeleted: req.deleted,
			Offset:         req.offset,
			Limit:          pageSize + 1, // one extra row tells whether more exist
		}
		if req.chatID != 0 && !(searching && req.everywhere) {
			q.ChatID = &req.chatID
		}
		if msgs, err = h.store.Messages(ctx, q); err != nil {
			h.fail(w, err)
			return
		}
		hasMore := len(msgs) > pageSize
		if hasMore {
			msgs = msgs[:pageSize]
		}
		// Rows are always selected newest first, so a larger offset is older.
		var olderURL, newerURL string
		if hasMore {
			older := req
			older.offset += pageSize
			olderURL = older.url()
		}
		if req.offset > 0 {
			newer := req
			newer.offset = max(req.offset-pageSize, 0)
			newerURL = newer.url()
		}
		if chronological {
			reverse(msgs)
			p.Top, p.Bottom = pagerLink(olderURL, "← Более ранние"), pagerLink(newerURL, "Более поздние →")
		} else {
			p.Top, p.Bottom = pagerLink(newerURL, "← Более поздние"), pagerLink(olderURL, "Более ранние →")
		}
		if searching {
			p.Title = "Поиск «" + req.query + "»"
			p.Empty = "Ничего не найдено"
		}
	}

	terms := strings.Fields(strings.ToLower(req.query))
	for _, m := range msgs {
		v := messageView{
			Sender:   m.SenderName,
			Chat:     m.ChatTitle,
			ShowChat: !chronological && req.around == 0,
			Date:     m.Date.Format(dateLayout),
			DateISO:  m.Date.Format(time.RFC3339),
			Media:    m.MediaType,
			Segments: highlight(m.Text, terms),
			Outgoing: m.Outgoing,
			Edited:   !m.EditedAt.IsZero(),
			Deleted:  !m.DeletedAt.IsZero(),
			Target:   req.around != 0 && m.ID == req.around,
		}
		if v.Sender == "" {
			v.Sender = m.SenderUsername
		}
		if v.ShowChat {
			v.ContextURL = request{chatID: m.ChatID, around: m.ID, deleted: req.deleted}.url()
		}
		p.Messages = append(p.Messages, v)
	}

	// Render into a buffer so a template error cannot produce half a page.
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, "index.html.tmpl", p); err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := buf.WriteTo(w); err != nil {
		h.log.Warn("web: write response", "error", err)
	}
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	h.log.Error("web: request failed", "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// highlight splits text into segments, marking words that start with one of
// the lower-cased search terms (the search itself matches word prefixes).
func highlight(text string, terms []string) []segment {
	if text == "" {
		return nil
	}
	if len(terms) == 0 {
		return []segment{{Text: text}}
	}
	var out []segment
	last := 0
	for _, loc := range wordRe.FindAllStringIndex(text, -1) {
		word := strings.ToLower(text[loc[0]:loc[1]])
		matched := false
		for _, t := range terms {
			if strings.HasPrefix(word, t) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if loc[0] > last {
			out = append(out, segment{Text: text[last:loc[0]]})
		}
		out = append(out, segment{Text: text[loc[0]:loc[1]], Mark: true})
		last = loc[1]
	}
	if last < len(text) {
		out = append(out, segment{Text: text[last:]})
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
