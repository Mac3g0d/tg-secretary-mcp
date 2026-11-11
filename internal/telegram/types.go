package telegram

import (
	"encoding/json"
	"strings"
)

// User is a Telegram user or bot.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// DisplayName returns the user's full name, falling back to the username.
func (u *User) DisplayName() string {
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return name
	}
	return u.Username
}

// Chat is a Telegram chat.
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// DisplayName returns the chat title, or the peer's name for private chats.
func (c *Chat) DisplayName() string {
	if c.Title != "" {
		return c.Title
	}
	if name := strings.TrimSpace(c.FirstName + " " + c.LastName); name != "" {
		return name
	}
	return c.Username
}

// MessageOrigin describes where a forwarded message originally came from.
// Type is one of "user", "hidden_user", "chat" or "channel" and decides which
// of the other fields are set.
type MessageOrigin struct {
	Type string `json:"type"`
	// Date is when the original message was sent (Unix time).
	Date int64 `json:"date"`
	// SenderUser is set for type "user".
	SenderUser *User `json:"sender_user"`
	// SenderUserName is set for type "hidden_user": the author hides their
	// account, so only a display name is available.
	SenderUserName string `json:"sender_user_name"`
	// SenderChat is set for type "chat" (a message sent on behalf of a chat).
	SenderChat *Chat `json:"sender_chat"`
	// Chat is set for type "channel".
	Chat *Chat `json:"chat"`
}

// Name returns the display name of the original author.
func (o *MessageOrigin) Name() string {
	switch {
	case o.SenderUser != nil:
		return o.SenderUser.DisplayName()
	case o.SenderChat != nil:
		return o.SenderChat.DisplayName()
	case o.Chat != nil:
		return o.Chat.DisplayName()
	default:
		return o.SenderUserName
	}
}

// Username returns the @username of the original author without the "@",
// or "" when the origin has none (for example a hidden user).
func (o *MessageOrigin) Username() string {
	switch {
	case o.SenderUser != nil:
		return o.SenderUser.Username
	case o.SenderChat != nil:
		return o.SenderChat.Username
	case o.Chat != nil:
		return o.Chat.Username
	default:
		return ""
	}
}

// Message is the subset of the Bot API Message object the secretary stores.
// Media payloads are kept raw: only their presence matters.
type Message struct {
	MessageID            int64          `json:"message_id"`
	Date                 int64          `json:"date"`
	EditDate             int64          `json:"edit_date"`
	Chat                 Chat           `json:"chat"`
	From                 *User          `json:"from"`
	SenderChat           *Chat          `json:"sender_chat"`
	BusinessConnectionID string         `json:"business_connection_id"`
	Text                 string         `json:"text"`
	Caption              string         `json:"caption"`
	ReplyToMessage       *Message       `json:"reply_to_message"`
	ForwardOrigin        *MessageOrigin `json:"forward_origin"`

	Photo     json.RawMessage `json:"photo"`
	Video     json.RawMessage `json:"video"`
	VideoNote json.RawMessage `json:"video_note"`
	Voice     json.RawMessage `json:"voice"`
	Audio     json.RawMessage `json:"audio"`
	Animation json.RawMessage `json:"animation"`
	Document  json.RawMessage `json:"document"`
	Sticker   json.RawMessage `json:"sticker"`
	Poll      json.RawMessage `json:"poll"`
	Location  json.RawMessage `json:"location"`
	Contact   json.RawMessage `json:"contact"`
}

// Content returns the text of the message or the caption of its media.
func (m *Message) Content() string {
	if m.Text != "" {
		return m.Text
	}
	return m.Caption
}

// MediaType names the attachment kind ("" for plain text messages).
func (m *Message) MediaType() string {
	// Animation precedes document: GIFs carry both fields.
	for _, f := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"photo", m.Photo},
		{"video", m.Video},
		{"video_note", m.VideoNote},
		{"voice", m.Voice},
		{"audio", m.Audio},
		{"animation", m.Animation},
		{"document", m.Document},
		{"sticker", m.Sticker},
		{"poll", m.Poll},
		{"location", m.Location},
		{"contact", m.Contact},
	} {
		if len(f.raw) > 0 {
			return f.name
		}
	}
	return ""
}

// BusinessConnection links the bot to a Telegram Business account.
type BusinessConnection struct {
	ID        string `json:"id"`
	User      User   `json:"user"`
	IsEnabled bool   `json:"is_enabled"`
}

// BusinessMessagesDeleted reports messages removed from a business chat.
type BusinessMessagesDeleted struct {
	BusinessConnectionID string  `json:"business_connection_id"`
	Chat                 Chat    `json:"chat"`
	MessageIDs           []int64 `json:"message_ids"`
}

// Update is an incoming Bot API update.
type Update struct {
	UpdateID                int64                    `json:"update_id"`
	Message                 *Message                 `json:"message"`
	EditedMessage           *Message                 `json:"edited_message"`
	ChannelPost             *Message                 `json:"channel_post"`
	EditedChannelPost       *Message                 `json:"edited_channel_post"`
	BusinessConnection      *BusinessConnection      `json:"business_connection"`
	BusinessMessage         *Message                 `json:"business_message"`
	EditedBusinessMessage   *Message                 `json:"edited_business_message"`
	DeletedBusinessMessages *BusinessMessagesDeleted `json:"deleted_business_messages"`
}
