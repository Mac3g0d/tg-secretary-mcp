package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

const (
	_defaultConsumer  = "default"
	_defaultFeedLimit = 100
	_firstFeedWindow  = 24 * time.Hour
)

type newMessagesInput struct {
	Consumer        string `json:"consumer,omitempty" jsonschema:"name of the reader whose position is tracked (default: default); use distinct names for independent agents"`
	Limit           int    `json:"limit,omitempty" jsonschema:"maximum number of messages (default 100, max 500)"`
	IncludeOutgoing bool   `json:"include_outgoing,omitempty" jsonschema:"also return messages written by the bot or the account owner"`
	Peek            bool   `json:"peek,omitempty" jsonschema:"if true, do not advance the position: the same messages are returned next time"`
}

func (t *tools) addGetNewMessages(srv *mcp.Server) {
	closedWorld := false
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_new_messages",
		Description: "Inbox feed: return messages that arrived since the previous call " +
			"(oldest first) and remember the position. " +
			"The first call for a consumer returns the last 24 hours. " +
			"Call again while has_more is true.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
			OpenWorldHint:  &closedWorld,
		},
	}, t.getNewMessages)
}

func (t *tools) getNewMessages(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in newMessagesInput,
) (*mcp.CallToolResult, MessagesOutput, error) {
	consumer := strings.TrimSpace(in.Consumer)
	if consumer == "" {
		consumer = _defaultConsumer
	}
	limit := store.ClampLimit(in.Limit, _defaultFeedLimit)
	q := store.Query{
		Ascending:    true,
		IncomingOnly: !in.IncludeOutgoing,
		LiveOnly:     true, // imported history is not news
		Limit:        limit + 1,
	}

	cursor, known, err := t.store.Cursor(ctx, consumer)
	if err != nil {
		return nil, MessagesOutput{}, fmt.Errorf("load feed position: %w", err)
	}
	if known {
		q.AfterPK = cursor
	} else {
		q.Since = time.Now().Add(-_firstFeedWindow)
	}

	// Read before querying so a message arriving in between is not skipped.
	maxPK, err := t.store.MaxPK(ctx)
	if err != nil {
		return nil, MessagesOutput{}, fmt.Errorf("load newest message position: %w", err)
	}
	msgs, err := t.store.Messages(ctx, q)
	if err != nil {
		return nil, MessagesOutput{}, fmt.Errorf("query new messages: %w", err)
	}

	out := newMessagesOutput(msgs, limit)
	if in.Peek {
		return nil, out, nil
	}
	// With nothing left to page through, also skip past filtered-out rows.
	shown := msgs[:out.Count]
	next := maxPK
	switch {
	case out.HasMore:
		next = shown[len(shown)-1].PK
	case len(shown) > 0:
		next = max(next, shown[len(shown)-1].PK)
	}
	if err := t.store.SetCursor(ctx, consumer, next); err != nil {
		return nil, MessagesOutput{}, fmt.Errorf("save feed position: %w", err)
	}
	return nil, out, nil
}
