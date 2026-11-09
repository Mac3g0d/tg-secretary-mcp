package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/collector"
)

type statusOutput struct {
	Chats         int64  `json:"chats"`
	Messages      int64  `json:"messages"`
	OldestMessage string `json:"oldest_message_date,omitempty"`
	NewestMessage string `json:"newest_message_date,omitempty"`
	DatabaseBytes int64  `json:"database_bytes"`
	Bot           string `json:"bot_username,omitempty"`
	LastUpdateAt  string `json:"last_update_received_at,omitempty" jsonschema:"when the bot last received anything from Telegram"`
	SendEnabled   bool   `json:"send_enabled"`
}

func (t *tools) addGetStatus(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_status",
		Description: "Report archive statistics and whether the Telegram bot " +
			"is collecting messages.",
		Annotations: readOnlyAnnotations(),
	}, t.getStatus)
}

func (t *tools) getStatus(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ struct{},
) (*mcp.CallToolResult, statusOutput, error) {
	st, err := t.store.Stats(ctx)
	if err != nil {
		return nil, statusOutput{}, fmt.Errorf("load archive stats: %w", err)
	}
	out := statusOutput{
		Chats:         st.Chats,
		Messages:      st.Messages,
		OldestMessage: formatTime(st.Oldest),
		NewestMessage: formatTime(st.Newest),
		DatabaseBytes: st.SizeBytes,
		SendEnabled:   t.sender != nil,
	}

	if out.Bot, err = t.store.Meta(ctx, collector.MetaBotUsername); err != nil {
		return nil, statusOutput{}, fmt.Errorf("load bot username: %w", err)
	}
	last, err := t.store.Meta(ctx, collector.MetaLastUpdateAt)
	if err != nil {
		return nil, statusOutput{}, fmt.Errorf("load last update time: %w", err)
	}
	if sec, _ := strconv.ParseInt(last, 10, 64); sec > 0 {
		out.LastUpdateAt = formatTime(time.Unix(sec, 0))
	}
	return nil, out, nil
}
