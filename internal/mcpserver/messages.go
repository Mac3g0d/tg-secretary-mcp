package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

const (
	_defaultMessageLimit = 50
	_defaultWindow       = 5
	_maxWindow           = 50
)

type getMessagesInput struct {
	Chat           string `json:"chat" jsonschema:"chat id, @username or (part of) the chat title"`
	Limit          int    `json:"limit,omitempty" jsonschema:"maximum number of messages (default 50, max 500)"`
	Since          string `json:"since,omitempty" jsonschema:"only messages at or after this time: RFC 3339, YYYY-MM-DD, or relative like 30m, 24h, 7d, 2w"`
	Until          string `json:"until,omitempty" jsonschema:"only messages at or before this time, same formats as since"`
	Sender         string `json:"sender,omitempty" jsonschema:"only messages from this sender: @username or part of the name"`
	IncludeDeleted bool   `json:"include_deleted,omitempty" jsonschema:"also return messages known to be deleted"`
}

type searchInput struct {
	Query          string `json:"query" jsonschema:"words to search for in message text"`
	Chat           string `json:"chat,omitempty" jsonschema:"optional chat id, @username or (part of) the chat title"`
	Sender         string `json:"sender,omitempty" jsonschema:"optional sender: @username or part of the name"`
	Since          string `json:"since,omitempty" jsonschema:"only messages at or after this time: RFC 3339, YYYY-MM-DD, or relative like 30m, 24h, 7d, 2w"`
	Until          string `json:"until,omitempty" jsonschema:"only messages at or before this time, same formats as since"`
	Limit          int    `json:"limit,omitempty" jsonschema:"maximum number of messages (default 50, max 500)"`
	IncludeDeleted bool   `json:"include_deleted,omitempty" jsonschema:"also return messages known to be deleted"`
}

type contextInput struct {
	Chat      string `json:"chat" jsonschema:"chat id, @username or (part of) the chat title"`
	MessageID int64  `json:"message_id" jsonschema:"message_id of the message of interest"`
	Before    *int   `json:"before,omitempty" jsonschema:"how many earlier messages to include (default 5, max 50)"`
	After     *int   `json:"after,omitempty" jsonschema:"how many later messages to include (default 5, max 50)"`
}

func (t *tools) addGetMessages(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_messages",
		Description: "Read messages of one chat in chronological order. " +
			"Returns the latest messages unless a time range is given.",
		Annotations: readOnlyAnnotations(),
	}, t.getMessages)
}

func (t *tools) addSearchMessages(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_messages",
		Description: "Full-text search across archived messages (all words must match, " +
			"word prefixes allowed), optionally limited to a chat, sender or time range. " +
			"Newest first.",
		Annotations: readOnlyAnnotations(),
	}, t.searchMessages)
}

func (t *tools) addGetMessageContext(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_message_context",
		Description: "Show the messages surrounding a specific message in its chat, " +
			"e.g. to understand a search hit.",
		Annotations: readOnlyAnnotations(),
	}, t.getContext)
}

func (t *tools) getMessages(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in getMessagesInput,
) (*mcp.CallToolResult, MessagesOutput, error) {
	chat, err := t.store.ResolveChat(ctx, in.Chat)
	if err != nil {
		return nil, MessagesOutput{}, fmt.Errorf("resolve chat: %w", err)
	}

	q := store.Query{
		ChatID:         &chat.ID,
		Sender:         in.Sender,
		IncludeDeleted: in.IncludeDeleted,
	}
	out, err := t.query(ctx, q, in.Since, in.Until, in.Limit)
	if err != nil {
		return nil, MessagesOutput{}, err
	}
	// Newest-first selection, chronological presentation.
	slices.Reverse(out.Messages)
	return nil, out, nil
}

func (t *tools) searchMessages(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in searchInput,
) (*mcp.CallToolResult, MessagesOutput, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, MessagesOutput{}, errors.New(
			"query is required; use get_messages to read a chat without searching")
	}

	q := store.Query{
		Text:           in.Query,
		Sender:         in.Sender,
		IncludeDeleted: in.IncludeDeleted,
	}
	if in.Chat != "" {
		chat, err := t.store.ResolveChat(ctx, in.Chat)
		if err != nil {
			return nil, MessagesOutput{}, fmt.Errorf("resolve chat: %w", err)
		}
		q.ChatID = &chat.ID
	}
	out, err := t.query(ctx, q, in.Since, in.Until, in.Limit)
	return nil, out, err
}

// query runs q with one extra row to learn whether more results exist.
func (t *tools) query(
	ctx context.Context,
	q store.Query,
	since, until string,
	limit int,
) (MessagesOutput, error) {
	now := time.Now()
	var err error
	if q.Since, err = parseTime(since, now); err != nil {
		return MessagesOutput{}, fmt.Errorf("since: %w", err)
	}
	if q.Until, err = parseTime(until, now); err != nil {
		return MessagesOutput{}, fmt.Errorf("until: %w", err)
	}

	limit = store.ClampLimit(limit, _defaultMessageLimit)
	q.Limit = limit + 1
	msgs, err := t.store.Messages(ctx, q)
	if err != nil {
		return MessagesOutput{}, fmt.Errorf("query messages: %w", err)
	}
	return newMessagesOutput(msgs, limit), nil
}

func (t *tools) getContext(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in contextInput,
) (*mcp.CallToolResult, MessagesOutput, error) {
	chat, err := t.store.ResolveChat(ctx, in.Chat)
	if err != nil {
		return nil, MessagesOutput{}, fmt.Errorf("resolve chat: %w", err)
	}

	before, after := clampWindow(in.Before), clampWindow(in.After)
	msgs, err := t.store.Context(ctx, chat.ID, in.MessageID, before, after)
	if err != nil {
		return nil, MessagesOutput{}, fmt.Errorf("load message context: %w", err)
	}
	return nil, MessagesOutput{Count: len(msgs), Messages: convert(msgs)}, nil
}

// clampWindow bounds a requested context size; nil selects the default.
func clampWindow(n *int) int {
	if n == nil {
		return _defaultWindow
	}
	return max(0, min(*n, _maxWindow))
}
