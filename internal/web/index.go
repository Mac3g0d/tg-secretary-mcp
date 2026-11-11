package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

const (
	_pageSize = 50
	// _contextWindow is how many messages are shown on each side of a
	// message opened from the search results.
	_contextWindow = 25
)

// index renders the page for the request's query parameters.
func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	req, ok := parseRequest(r.URL.Query())
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	chats, err := h.store.Chats(ctx, "", "", store.MaxLimit)
	if err != nil {
		h.fail(w, fmt.Errorf("list chats: %w", err))
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
	var activeTitle string
	p.Chats, activeTitle = chatViews(chats, req.chatID)
	if activeTitle != "" {
		p.Title = activeTitle
	}

	// Every view reads top to bottom like a messenger, newest at the bottom.
	// Only a single chat without a search can omit the chat name per message.
	singleChat := req.chatID != 0 && req.query == ""
	var msgs []store.Message
	if req.around != 0 {
		msgs, err = h.store.Context(ctx, req.chatID, req.around, _contextWindow, _contextWindow)
		if err != nil {
			http.Error(w, "message not found", http.StatusNotFound)
			return
		}
		p.Notice = "Показан фрагмент переписки."
	} else if msgs, err = h.listMessages(ctx, req, &p); err != nil {
		h.fail(w, err)
		return
	}
	p.Messages = messageViews(msgs, req, singleChat)

	h.render(w, p)
}

// listMessages loads one page of messages for req, in presentation order, and
// sets the pager links and, for a search, the title and empty text on p.
func (h *handler) listMessages(
	ctx context.Context,
	req request,
	p *page,
) ([]store.Message, error) {
	searching := req.query != ""
	q := store.Query{
		Text:           req.query,
		IncludeDeleted: req.deleted,
		Offset:         req.offset,
		Limit:          _pageSize + 1, // one extra row tells whether more exist
	}
	if req.chatID != 0 && (!searching || !req.everywhere) {
		q.ChatID = &req.chatID
	}
	msgs, err := h.store.Messages(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	hasMore := len(msgs) > _pageSize
	if hasMore {
		msgs = msgs[:_pageSize]
	}

	// Rows are always selected newest first, so a larger offset is older.
	var olderURL, newerURL string
	if hasMore {
		older := req
		older.offset += _pageSize
		olderURL = older.url()
	}
	if req.offset > 0 {
		newer := req
		newer.offset = max(req.offset-_pageSize, 0)
		newerURL = newer.url()
	}
	// ...and shown oldest first, so the latest message ends up at the bottom.
	slices.Reverse(msgs)
	p.Top = pagerLink(olderURL, "↑ Более ранние")
	p.Bottom = pagerLink(newerURL, "Более поздние ↓")
	if searching {
		p.Title = "Поиск «" + req.query + "»"
		p.Empty = "Ничего не найдено"
	}
	return msgs, nil
}

// render executes the page template into a buffer first so a template error
// cannot produce half a page.
func (h *handler) render(w http.ResponseWriter, p page) {
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, "index.html.tmpl", p); err != nil {
		h.fail(w, fmt.Errorf("render page: %w", err))
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
