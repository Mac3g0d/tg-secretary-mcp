package web

import (
	"strconv"
	"strings"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

const (
	_dateLayout    = "02.01.2006 15:04"
	_previewLength = 80
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

// chatViews renders the chat list and returns the title of the chat with ID
// active, or "" when it is not in the list.
func chatViews(chats []store.ChatSummary, active int64) (views []chatView, activeTitle string) {
	views = make([]chatView, 0, len(chats))
	for _, c := range chats {
		title := c.Chat.Title
		if title == "" {
			title = strconv.FormatInt(c.Chat.ID, 10)
		}
		isActive := c.Chat.ID == active
		if isActive {
			activeTitle = title
		}
		views = append(views, chatView{
			Title:   title,
			Count:   c.MessageCount,
			Preview: truncate(c.LastText, _previewLength),
			URL:     request{chatID: c.Chat.ID}.url(),
			Active:  isActive,
		})
	}
	return views, activeTitle
}

// messageViews renders msgs for req. chronological tells whether the list is a
// single chat read top to bottom, in which case each message omits its chat.
func messageViews(msgs []store.Message, req request, chronological bool) []messageView {
	terms := strings.Fields(strings.ToLower(req.query))
	views := make([]messageView, 0, len(msgs))
	for _, m := range msgs {
		v := messageView{
			Sender:   m.SenderName,
			Chat:     m.ChatTitle,
			ShowChat: !chronological && req.around == 0,
			Date:     m.Date.Format(_dateLayout),
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
			v.ContextURL = request{
				chatID:  m.ChatID,
				around:  m.ID,
				deleted: req.deleted,
			}.url()
		}
		views = append(views, v)
	}
	return views
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
