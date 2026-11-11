package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// ChatPhoto identifies the profile picture of a chat. The "small" variant is
// a 160x160 image.
type ChatPhoto struct {
	SmallFileID string `json:"small_file_id"`
	// SmallFileUniqueID stays the same for as long as the picture does, so
	// it tells whether a stored copy is still current.
	SmallFileUniqueID string `json:"small_file_unique_id"`
}

// ChatInfo is the subset of the Bot API ChatFullInfo object the secretary
// uses.
type ChatInfo struct {
	ID int64 `json:"id"`
	// Photo is nil when the chat has no picture or the bot may not see it.
	Photo *ChatPhoto `json:"photo"`
}

// File describes a file that is ready to be downloaded.
type File struct {
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}

// GetChat returns up-to-date information about a chat the bot has access to.
func (c *Client) GetChat(ctx context.Context, chatID int64) (*ChatInfo, error) {
	var info ChatInfo
	if err := c.call(ctx, "getChat", map[string]any{"chat_id": chatID}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetFile prepares a file for download and returns its path on the server.
func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	var f File
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// Download returns the contents of a file prepared with GetFile. It fails
// rather than read more than maxBytes.
func (c *Client) Download(ctx context.Context, filePath string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/file/bot"+c.token+"/"+filePath, nil)
	if err != nil {
		return nil, c.redact(err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.redact(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: download file: HTTP %d", resp.StatusCode)
	}
	// One extra byte distinguishes "exactly maxBytes" from "too large".
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("telegram: download file: %w", c.redact(err))
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("telegram: download file: larger than %d bytes", maxBytes)
	}
	return data, nil
}
