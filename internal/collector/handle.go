package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
)

// handle stores a single update.
func (c *Collector) handle(ctx context.Context, u telegram.Update) error {
	switch {
	case u.BusinessConnection != nil:
		bc := u.BusinessConnection
		err := c.store.SaveBusinessConnection(ctx, bc.ID, bc.User.ID, bc.IsEnabled)
		if err != nil {
			return fmt.Errorf("save business connection: %w", err)
		}
		return nil
	case u.DeletedBusinessMessages != nil:
		d := u.DeletedBusinessMessages
		if _, err := c.store.MarkDeleted(ctx, d.Chat.ID, d.MessageIDs); err != nil {
			return fmt.Errorf("mark messages deleted: %w", err)
		}
		return nil
	}

	for _, m := range []*telegram.Message{
		u.Message,
		u.EditedMessage,
		u.ChannelPost,
		u.EditedChannelPost,
		u.BusinessMessage,
		u.EditedBusinessMessage,
	} {
		if m != nil {
			return c.saveMessage(ctx, m)
		}
	}
	return nil
}

// saveMessage archives one message together with its chat. Service messages
// (joins, pins, …) that carry neither text nor media are skipped.
func (c *Collector) saveMessage(ctx context.Context, m *telegram.Message) error {
	if len(c.allowedChats) > 0 && !c.allowedChats[m.Chat.ID] {
		return nil
	}
	text, media := m.Content(), m.MediaType()
	if text == "" && media == "" {
		return nil
	}

	err := c.store.UpsertChat(ctx, store.Chat{
		ID:                   m.Chat.ID,
		Type:                 m.Chat.Type,
		Title:                m.Chat.DisplayName(),
		Username:             m.Chat.Username,
		BusinessConnectionID: m.BusinessConnectionID,
	})
	if err != nil {
		return fmt.Errorf("upsert chat %d: %w", m.Chat.ID, err)
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
	if o := m.ForwardOrigin; o != nil {
		rec.ForwardFrom = o.Name()
		rec.ForwardFromUsername = o.Username()
		if o.Date != 0 {
			rec.ForwardDate = time.Unix(o.Date, 0)
		}
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
		owner, err := c.store.BusinessOwner(ctx, m.BusinessConnectionID)
		if err != nil {
			return fmt.Errorf("look up business owner: %w", err)
		}
		rec.Outgoing = owner != 0 && owner == m.From.ID
	}

	if err := c.store.SaveMessage(ctx, rec); err != nil {
		return fmt.Errorf("save message %d/%d: %w", rec.ChatID, rec.ID, err)
	}
	return nil
}
