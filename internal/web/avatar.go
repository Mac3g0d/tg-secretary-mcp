package web

import (
	"fmt"
	"net/http"
	"strconv"
)

// avatar serves the stored profile picture of a chat.
func (h *handler) avatar(w http.ResponseWriter, r *http.Request) {
	chatID, err := strconv.ParseInt(r.PathValue("chat"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	photo, ok, err := h.store.Photo(r.Context(), chatID)
	if err != nil {
		h.fail(w, fmt.Errorf("load avatar: %w", err))
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	header := w.Header()
	header.Set("Content-Type", photo.ContentType)
	header.Set("Content-Length", strconv.Itoa(len(photo.Data)))
	// Links carry the picture's version, so a response never goes stale.
	// "private" keeps other people's faces out of shared caches.
	header.Set("Cache-Control", "private, max-age=31536000, immutable")
	if _, err := w.Write(photo.Data); err != nil {
		h.log.Warn("web: write avatar", "error", err)
	}
}
