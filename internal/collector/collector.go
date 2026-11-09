// Package collector runs the Telegram bot: it long-polls the Bot API and
// archives every message the bot can see.
package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

const (
	_defaultPollTimeout = 50 * time.Second
	_minBackoff         = time.Second
	_maxBackoff         = time.Minute
)

// Config configures a Collector.
type Config struct {
	// API is the Bot API client. It is required to Run.
	API *telegram.Client
	// Store receives the archived messages. It is required.
	Store *store.Store
	// Log receives progress and error messages. It defaults to a logger
	// that discards everything.
	Log *slog.Logger
	// AllowedChats, when non-empty, restricts archiving to these chat IDs.
	AllowedChats map[int64]bool
	// PollTimeout is the long-polling timeout. It defaults to 50 seconds.
	PollTimeout time.Duration
}

// Collector archives Bot API updates into the store.
type Collector struct {
	api          *telegram.Client
	store        *store.Store
	log          *slog.Logger
	allowedChats map[int64]bool
	pollTimeout  time.Duration

	// botID is the bot's own user ID, known once Run has started.
	botID int64
}

// New returns a Collector for cfg.
func New(cfg Config) *Collector {
	c := &Collector{
		api:          cfg.API,
		store:        cfg.Store,
		log:          cfg.Log,
		allowedChats: cfg.AllowedChats,
		pollTimeout:  cfg.PollTimeout,
	}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if c.pollTimeout == 0 {
		c.pollTimeout = _defaultPollTimeout
	}
	return c
}

// Run polls until ctx is cancelled. It returns an error only when the bot
// cannot start at all (for example, an invalid token).
func (c *Collector) Run(ctx context.Context) error {
	me, err := c.api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("get bot identity: %w", err)
	}
	c.botID = me.ID
	if err := c.store.SetMeta(ctx, MetaBotUsername, me.Username); err != nil {
		return fmt.Errorf("save bot username: %w", err)
	}
	c.log.Info("bot started", "username", me.Username)

	saved, err := c.store.Meta(ctx, MetaOffset)
	if err != nil {
		return fmt.Errorf("load update offset: %w", err)
	}
	offset, _ := strconv.ParseInt(saved, 10, 64)

	backoff := _minBackoff
	for ctx.Err() == nil {
		updates, pollErr := c.api.GetUpdates(ctx, offset, c.pollTimeout)
		if pollErr != nil {
			if ctx.Err() != nil {
				break
			}
			wait, fatal := c.retryDelay(pollErr, backoff)
			if fatal != nil {
				return fatal
			}
			c.log.Warn("getUpdates failed, retrying", "error", pollErr, "in", wait)
			sleep(ctx, wait)
			backoff = min(backoff*2, _maxBackoff)
			continue
		}
		backoff = _minBackoff

		if len(updates) > 0 {
			offset = c.handleBatch(ctx, updates)
		}
	}
	return nil
}

// retryDelay decides how long to wait after a failed poll. It returns the
// error itself when polling can never succeed, such as with a revoked token.
func (c *Collector) retryDelay(err error, backoff time.Duration) (time.Duration, error) {
	var apiErr *telegram.APIError
	if !errors.As(err, &apiErr) {
		return backoff, nil
	}
	if apiErr.Code == http.StatusUnauthorized {
		return 0, fmt.Errorf("poll updates: %w", err)
	}
	// 409 means another process polls with the same token.
	return max(backoff, apiErr.RetryAfter), nil
}

// handleBatch stores a batch of updates and returns the next offset.
func (c *Collector) handleBatch(ctx context.Context, updates []telegram.Update) int64 {
	var offset int64
	for _, u := range updates {
		if err := c.handle(ctx, u); err != nil {
			c.log.Error("store update", "update_id", u.UpdateID, "error", err)
		}
		offset = u.UpdateID + 1
	}
	// The offset is persisted so a restart neither replays nor skips updates.
	if err := c.store.SetMeta(ctx, MetaOffset, strconv.FormatInt(offset, 10)); err != nil {
		c.log.Error("persist offset", "error", err)
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	if err := c.store.SetMeta(ctx, MetaLastUpdateAt, now); err != nil {
		c.log.Warn("persist last update time", "error", err)
	}
	return offset
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
