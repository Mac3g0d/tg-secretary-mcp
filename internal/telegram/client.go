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

// _pollGrace is the extra time a long-poll HTTP request gets beyond the
// server-side timeout, so the server can answer before we give up.
const _pollGrace = 15 * time.Second

var _ error = (*APIError)(nil)

// allowedUpdates lists the update types the secretary subscribes to. It
// returns a fresh slice so callers cannot mutate shared state.
func allowedUpdates() []string {
	return []string{
		"message",
		"edited_message",
		"channel_post",
		"edited_channel_post",
		"business_connection",
		"business_message",
		"edited_business_message",
		"deleted_business_messages",
	}
}

// APIError is an error response from the Bot API.
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

// Error implements the error interface.
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
	return &Client{
		token:   token,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{},
	}
}

// GetMe returns the bot's own account.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", struct{}{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUpdates long-polls for updates with update_id >= offset.
func (c *Client) GetUpdates(
	ctx context.Context, offset int64, timeout time.Duration,
) ([]Update, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout+_pollGrace)
	defer cancel()

	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": allowedUpdates(),
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
	if err := c.call(ctx, "sendMessage", body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram: %s: encode request: %w", method, err)
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
		return fmt.Errorf("telegram: %s: bad response (HTTP %d): %w",
			method, resp.StatusCode, err)
	}
	if !envelope.OK {
		return &APIError{
			Code:        envelope.ErrorCode,
			Description: envelope.Description,
			RetryAfter:  time.Duration(envelope.Parameters.RetryAfter) * time.Second,
		}
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("telegram: %s: decode result: %w", method, err)
	}
	return nil
}

// redact removes the bot token from transport errors, which embed the URL.
// Context cancellation stays detectable with errors.Is.
func (c *Client) redact(err error) error {
	if c.token == "" || !strings.Contains(err.Error(), c.token) {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), c.token, "<token>")
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, sentinel) {
			return fmt.Errorf("%s: %w", msg, sentinel)
		}
	}
	return errors.New(msg)
}
