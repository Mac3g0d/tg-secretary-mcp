package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
)

type sendInput struct {
	Chat             string `json:"chat" jsonschema:"chat id, @username or (part of) the chat title; the chat must be in the archive"`
	Text             string `json:"text" jsonschema:"plain message text (up to 4096 characters)"`
	ReplyToMessageID int64  `json:"reply_to_message_id,omitempty" jsonschema:"optional message_id to reply to"`
}

type sendOutput struct {
	Sent Message `json:"sent"`
}

func (t *tools) addSendMessage(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "send_message",
		Description: "Send a text message to a chat on behalf of the bot. " +
			"The bot can only write to chats where it is a member " +
			"or to users who have started it. " +
			"Confirm with the user before sending unless explicitly told otherwise.",
	}, t.sendMessage)
}

func (t *tools) sendMessage(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in sendInput,
) (*mcp.CallToolResult, sendOutput, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, sendOutput{}, errors.New("text is required")
	}
	chat, err := t.store.ResolveChat(ctx, in.Chat)
	if err != nil {
		return nil, sendOutput{}, fmt.Errorf("resolve chat: %w", err)
	}

	sent, err := t.sender.SendMessage(ctx, telegram.SendParams{
		ChatID:               chat.ID,
		Text:                 in.Text,
		ReplyToMessageID:     in.ReplyToMessageID,
		BusinessConnectionID: chat.BusinessConnectionID,
	})
	if err != nil {
		return nil, sendOutput{}, fmt.Errorf("send message: %w", err)
	}

	// Bots do not receive their own messages as updates, so archive it here.
	rec := store.Message{
		ChatID:    chat.ID,
		ChatTitle: chat.Title,
		ID:        sent.MessageID,
		Date:      time.Unix(sent.Date, 0),
		Text:      sent.Content(),
		ReplyToID: in.ReplyToMessageID,
		Outgoing:  true,
	}
	if sent.From != nil {
		rec.SenderID = sent.From.ID
		rec.SenderName = sent.From.DisplayName()
		rec.SenderUsername = sent.From.Username
	}
	if err := t.store.SaveMessage(ctx, rec); err != nil {
		return nil, sendOutput{}, fmt.Errorf("message was sent but could not be archived: %w", err)
	}
	return nil, sendOutput{Sent: convert([]store.Message{rec})[0]}, nil
}
