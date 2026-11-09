// Package collector runs the Telegram bot: it long-polls the Bot API and
// archives every message the bot can see.
package collector

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
)

// Meta keys the collector maintains; the MCP status tool reads them.
const (
	MetaOffset       = "update_offset"
	MetaBotUsername  = "bot_username"
	MetaLastUpdateAt = "last_update_at"
)

// Collector archives Bot API updates into the store.
type Collector struct {
	API   *telegram.Client
	Store *store.Store
	Log   *slog.Logger
	// AllowedChats, when non-empty, restricts archiving to these chat IDs.
	AllowedChats map[int64]bool
	// PollTimeout is the long-polling timeout (default 50s).
	PollTimeout time.Duration

	botID int64
}

// Run polls until ctx is cancelled. It returns an error only when the bot
// cannot start at all (for example, an invalid token).
func (c *Collector) Run(ctx context.Context) error {
	if c.PollTimeout == 0 {
		c.PollTimeout = 50 * time.Second
	}
	me, err := c.API.GetMe(ctx)
	if err != nil {
		return err
	}
	c.botID = me.ID
	if err := c.Store.SetMeta(ctx, MetaBotUsername, me.Username); err != nil {
		return err
	}
	c.Log.Info("bot started", "username", me.Username)

	saved, err := c.Store.Meta(ctx, MetaOffset)
	if err != nil {
		return err
	}
	offset, _ := strconv.ParseInt(saved, 10, 64)

	backoff := time.Second
	for ctx.Err() == nil {
		updates, err := c.API.GetUpdates(ctx, offset, c.PollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			wait := backoff
			var apiErr *telegram.APIError
			if errors.As(err, &apiErr) {
				if apiErr.Code == 401 {
					return err
				}
				// 409 means another process polls with the same token.
				wait = max(wait, apiErr.RetryAfter)
			}
			c.Log.Warn("getUpdates failed, retrying", "error", err, "in", wait)
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		if len(updates) == 0 {
			continue
		}
		for _, u := range updates {
			if err := c.Handle(ctx, u); err != nil {
				c.Log.Error("failed to store update", "update_id", u.UpdateID, "error", err)
			}
			offset = u.UpdateID + 1
		}
		// The offset is persisted so a restart neither replays nor skips updates.
		if err := c.Store.SetMeta(ctx, MetaOffset, strconv.FormatInt(offset, 10)); err != nil {
			c.Log.Error("failed to persist offset", "error", err)
		}
		_ = c.Store.SetMeta(ctx, MetaLastUpdateAt, strconv.FormatInt(time.Now().Unix(), 10))
	}
	return nil
}

// Handle stores a single update.
func (c *Collector) Handle(ctx context.Context, u telegram.Update) error {
	switch {
	case u.BusinessConnection != nil:
		bc := u.BusinessConnection
		return c.Store.SaveBusinessConnection(ctx, bc.ID, bc.User.ID, bc.IsEnabled)
	case u.DeletedBusinessMessages != nil:
		d := u.DeletedBusinessMessages
		_, err := c.Store.MarkDeleted(ctx, d.Chat.ID, d.MessageIDs)
		return err
	}
	for _, m := range []*telegram.Message{
		u.Message, u.EditedMessage, u.ChannelPost, u.EditedChannelPost,
		u.BusinessMessage, u.EditedBusinessMessage,
	} {
		if m != nil {
			return c.SaveMessage(ctx, m)
		}
	}
	return nil
}

// SaveMessage archives one message together with its chat. Service messages
// (joins, pins, …) that carry neither text nor media are skipped.
func (c *Collector) SaveMessage(ctx context.Context, m *telegram.Message) error {
	if len(c.AllowedChats) > 0 && !c.AllowedChats[m.Chat.ID] {
		return nil
	}
	text, media := m.Content(), m.MediaType()
	if text == "" && media == "" {
		return nil
	}
	if err := c.Store.UpsertChat(ctx, store.Chat{
		ID:                   m.Chat.ID,
		Type:                 m.Chat.Type,
		Title:                m.Chat.DisplayName(),
		Username:             m.Chat.Username,
		BusinessConnectionID: m.BusinessConnectionID,
	}); err != nil {
		return err
	}

	rec := store.Message{
		ChatID:    m.Chat.ID,
		ID:        m.MessageID,
		Date:      time.Unix(m.Date, 0),
		Text:      text,
		MediaType: media,
	}
	if m.EditDate != 0 {
		rec.EditedAt = time.Unix(m.EditDate, 0)
	}
	if m.ReplyToMessage != nil {
		rec.ReplyToID = m.ReplyToMessage.MessageID
	}
	switch {
	case m.SenderChat != nil: // channel posts and anonymous group admins
		rec.SenderID = m.SenderChat.ID
		rec.SenderName = m.SenderChat.DisplayName()
		rec.SenderUsername = m.SenderChat.Username
	case m.From != nil:
		rec.SenderID = m.From.ID
		rec.SenderName = m.From.DisplayName()
		rec.SenderUsername = m.From.Username
	}

	// "Outgoing" means written by the bot itself or, in a business chat, by
	// the account owner the secretary works for.
	rec.Outgoing = rec.SenderID == c.botID && c.botID != 0
	if m.BusinessConnectionID != "" && m.From != nil {
		owner, err := c.Store.BusinessOwner(ctx, m.BusinessConnectionID)
		if err != nil {
			return err
		}
		rec.Outgoing = owner != 0 && owner == m.From.ID
	}
	return c.Store.SaveMessage(ctx, rec)
}
