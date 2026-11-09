// Package telegram is a minimal Telegram Bot API client covering only what the
// secretary needs: long polling for updates and sending text messages.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the public Bot API endpoint.
const DefaultBaseURL = "https://api.telegram.org"

// AllowedUpdates lists the update types the secretary subscribes to.
var AllowedUpdates = []string{
	"message", "edited_message", "channel_post", "edited_channel_post",
	"business_connection", "business_message", "edited_business_message", "deleted_business_messages",
}

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

// Message is the subset of the Bot API Message object the secretary stores.
// Media payloads are kept raw: only their presence matters.
type Message struct {
	MessageID            int64    `json:"message_id"`
	Date                 int64    `json:"date"`
	EditDate             int64    `json:"edit_date"`
	Chat                 Chat     `json:"chat"`
	From                 *User    `json:"from"`
	SenderChat           *Chat    `json:"sender_chat"`
	BusinessConnectionID string   `json:"business_connection_id"`
	Text                 string   `json:"text"`
	Caption              string   `json:"caption"`
	ReplyToMessage       *Message `json:"reply_to_message"`

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
		{"photo", m.Photo}, {"video", m.Video}, {"video_note", m.VideoNote}, {"voice", m.Voice},
		{"audio", m.Audio}, {"animation", m.Animation}, {"document", m.Document},
		{"sticker", m.Sticker}, {"poll", m.Poll}, {"location", m.Location}, {"contact", m.Contact},
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

// APIError is an error response from the Bot API.
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %s (%d)", e.Description, e.Code)
}

// Client talks to the Bot API.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// New creates a client. An empty baseURL selects the public Bot API.
func New(token, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{token: token, baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{}}
}

// GetMe returns the bot's own account.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	return &u, c.call(ctx, "getMe", struct{}{}, &u)
}

// GetUpdates long-polls for updates with update_id >= offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	// Give the server time to answer before the HTTP request is abandoned.
	ctx, cancel := context.WithTimeout(ctx, timeout+15*time.Second)
	defer cancel()
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": AllowedUpdates,
	}, &updates)
	return updates, err
}

// SendParams are the arguments of SendMessage.
type SendParams struct {
	ChatID               int64
	Text                 string
	ReplyToMessageID     int64
	BusinessConnectionID string
}

// SendMessage sends a plain-text message.
func (c *Client) SendMessage(ctx context.Context, p SendParams) (*Message, error) {
	body := map[string]any{"chat_id": p.ChatID, "text": p.Text}
	if p.ReplyToMessageID != 0 {
		body["reply_parameters"] = map[string]any{"message_id": p.ReplyToMessageID}
	}
	if p.BusinessConnectionID != "" {
		body["business_connection_id"] = p.BusinessConnectionID
	}
	var m Message
	return &m, c.call(ctx, "sendMessage", body, &m)
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return c.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return c.redact(err)
	}
	defer resp.Body.Close()

	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("telegram: %s: bad response (HTTP %d): %w", method, resp.StatusCode, err)
	}
	if !envelope.OK {
		return &APIError{
			Code:        envelope.ErrorCode,
			Description: envelope.Description,
			RetryAfter:  time.Duration(envelope.Parameters.RetryAfter) * time.Second,
		}
	}
	return json.Unmarshal(envelope.Result, result)
}

// redact removes the bot token from transport errors, which embed the URL.
func (c *Client) redact(err error) error {
	if c.token == "" || !strings.Contains(err.Error(), c.token) {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), c.token, "<token>")
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s: %w", msg, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", msg, context.DeadlineExceeded)
	}
	return errors.New(msg)
}
