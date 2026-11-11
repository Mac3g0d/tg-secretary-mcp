package collector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
)

const (
	// _defaultAvatarInterval is how often new chats are looked for.
	_defaultAvatarInterval = time.Minute
	// _avatarMaxAge is how long a lookup stays valid before the picture is
	// checked for changes again.
	_avatarMaxAge = 24 * time.Hour
	// _avatarBatch bounds the Bot API calls made per round.
	_avatarBatch = 20
	// _avatarMaxBytes is far above the size of a 160x160 picture.
	_avatarMaxBytes = 1 << 20
)

// runAvatars keeps chat profile pictures up to date until ctx is cancelled.
func (c *Collector) runAvatars(ctx context.Context) {
	for ctx.Err() == nil {
		wait := c.avatarInterval
		if retryAfter := c.refreshAvatars(ctx); retryAfter > wait {
			wait = retryAfter
		}
		c.waitForAvatarWork(ctx, wait, retryAfterSet(wait, c.avatarInterval))
	}
}

// retryAfterSet reports whether wait was stretched by a rate limit.
func retryAfterSet(wait, interval time.Duration) bool {
	return wait > interval
}

// waitForAvatarWork blocks for d, or less if new updates arrive. While
// Telegram has asked to slow down, early wake-ups are ignored.
func (c *Collector) waitForAvatarWork(ctx context.Context, d time.Duration, rateLimited bool) {
	t := time.NewTimer(d)
	defer t.Stop()

	kick := c.avatarKick
	if rateLimited {
		kick = nil // a nil channel never becomes ready
	}
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-kick:
	}
}

// refreshAvatars looks up the pictures of chats that are due. When Telegram
// asks to slow down, it stops early and returns how long to wait.
func (c *Collector) refreshAvatars(ctx context.Context) (retryAfter time.Duration) {
	due, err := c.store.PhotosToCheck(ctx, time.Now().Add(-_avatarMaxAge), _avatarBatch)
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn("list chats for avatars", "error", err)
		}
		return 0
	}
	for _, chat := range due {
		err := c.refreshAvatar(ctx, chat)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return 0
		}
		var apiErr *telegram.APIError
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusTooManyRequests {
			return apiErr.RetryAfter
		}
		// Typically the bot may not see this chat (an imported one, or a
		// user who never wrote to it). Remember the attempt so the chat is
		// not asked about again until the next day.
		c.log.Debug("avatar unavailable", "chat_id", chat.ChatID, "error", err)
		if err := c.store.TouchPhoto(ctx, chat.ChatID); err != nil && ctx.Err() == nil {
			c.log.Warn("record avatar lookup", "chat_id", chat.ChatID, "error", err)
		}
	}
	return 0
}

// refreshAvatar brings the stored picture of one chat in line with Telegram.
func (c *Collector) refreshAvatar(ctx context.Context, chat store.PhotoCheck) error {
	info, err := c.api.GetChat(ctx, chat.ChatID)
	if err != nil {
		return fmt.Errorf("get chat: %w", err)
	}
	if info.Photo == nil || info.Photo.SmallFileID == "" {
		return c.store.ClearPhoto(ctx, chat.ChatID)
	}
	if chat.UniqueID != "" && info.Photo.SmallFileUniqueID == chat.UniqueID {
		return c.store.TouchPhoto(ctx, chat.ChatID)
	}

	file, err := c.api.GetFile(ctx, info.Photo.SmallFileID)
	if err != nil {
		return fmt.Errorf("get file: %w", err)
	}
	data, err := c.api.Download(ctx, file.FilePath, _avatarMaxBytes)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	// The bytes are served to browsers later, so only store what really is
	// an image, whatever the server claimed.
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return fmt.Errorf("unexpected avatar content type %q", contentType)
	}
	return c.store.SavePhoto(ctx, chat.ChatID, store.Photo{
		UniqueID:    info.Photo.SmallFileUniqueID,
		ContentType: contentType,
		Data:        data,
	})
}
