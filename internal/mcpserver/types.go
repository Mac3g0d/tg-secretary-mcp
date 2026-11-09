package mcpserver

import (
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

// Message is the tool-facing representation of a stored message.
type Message struct {
	ChatID         int64  `json:"chat_id"`
	Chat           string `json:"chat"`
	MessageID      int64  `json:"message_id"`
	Date           string `json:"date" jsonschema:"RFC 3339 timestamp"`
	Sender         string `json:"sender,omitempty"`
	SenderUsername string `json:"sender_username,omitempty"`
	SenderID       int64  `json:"sender_id,omitempty"`
	Text           string `json:"text"`
	Media          string `json:"media,omitempty" jsonschema:"attachment kind such as photo or voice; attachments themselves are not stored"`
	ReplyToID      int64  `json:"reply_to_message_id,omitempty"`
	Outgoing       bool   `json:"outgoing,omitempty" jsonschema:"true if written by the bot or the account owner"`
	Edited         bool   `json:"edited,omitempty"`
	Deleted        bool   `json:"deleted,omitempty"`
}

// MessagesOutput is the result of the message-returning tools.
type MessagesOutput struct {
	Count    int       `json:"count"`
	Messages []Message `json:"messages"`
	HasMore  bool      `json:"has_more" jsonschema:"true if more messages matched than were returned"`
}

// convert maps stored messages to their tool-facing form.
func convert(msgs []store.Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, Message{
			ChatID:         m.ChatID,
			Chat:           m.ChatTitle,
			MessageID:      m.ID,
			Date:           formatTime(m.Date),
			Sender:         m.SenderName,
			SenderUsername: m.SenderUsername,
			SenderID:       m.SenderID,
			Text:           m.Text,
			Media:          m.MediaType,
			ReplyToID:      m.ReplyToID,
			Outgoing:       m.Outgoing,
			Edited:         !m.EditedAt.IsZero(),
			Deleted:        !m.DeletedAt.IsZero(),
		})
	}
	return out
}

// newMessagesOutput converts msgs and reports whether more rows were fetched
// than limit. The extra row is dropped.
func newMessagesOutput(msgs []store.Message, limit int) MessagesOutput {
	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}
	out := MessagesOutput{
		Messages: convert(msgs),
		HasMore:  hasMore,
	}
	out.Count = len(out.Messages)
	return out
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
