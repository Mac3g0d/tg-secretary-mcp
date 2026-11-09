package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const _previewLength = 120

type chatInfo struct {
	ID           int64  `json:"id"`
	Type         string `json:"type" jsonschema:"private, group, supergroup or channel"`
	Title        string `json:"title"`
	Username     string `json:"username,omitempty"`
	MessageCount int64  `json:"message_count"`
	LastDate     string `json:"last_message_date,omitempty"`
	LastText     string `json:"last_message_preview,omitempty"`
}

type listChatsInput struct {
	Query string `json:"query,omitempty" jsonschema:"optional case-insensitive part of the chat title or username"`
	Type  string `json:"type,omitempty" jsonschema:"optional chat type filter: private, group, supergroup or channel"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of chats (default 50, max 500)"`
}

type listChatsOutput struct {
	Count int        `json:"count"`
	Chats []chatInfo `json:"chats"`
}

func (t *tools) addListChats(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_chats",
		Description: "List archived Telegram chats, most recently active first, " +
			"with message counts and a preview of the last message.",
		Annotations: readOnlyAnnotations(),
	}, t.listChats)
}

func (t *tools) listChats(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in listChatsInput,
) (*mcp.CallToolResult, listChatsOutput, error) {
	chats, err := t.store.Chats(ctx, in.Query, in.Type, in.Limit)
	if err != nil {
		return nil, listChatsOutput{}, fmt.Errorf("list chats: %w", err)
	}

	out := listChatsOutput{
		Count: len(chats),
		Chats: make([]chatInfo, 0, len(chats)),
	}
	for _, c := range chats {
		out.Chats = append(out.Chats, chatInfo{
			ID:           c.Chat.ID,
			Type:         c.Chat.Type,
			Title:        c.Chat.Title,
			Username:     c.Chat.Username,
			MessageCount: c.MessageCount,
			LastDate:     formatTime(c.LastDate),
			LastText:     truncate(c.LastText, _previewLength),
		})
	}
	return nil, out, nil
}
