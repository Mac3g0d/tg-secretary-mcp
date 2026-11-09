// Package web serves a minimal, JavaScript-free browser UI for viewing and
// searching the message archive. Pages are rendered on the server with
// html/template, which escapes the (untrusted) message content.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
)

var (
	//go:embed static
	_staticFiles embed.FS
	//go:embed templates
	_templateFiles embed.FS
)

// handler renders the UI pages.
type handler struct {
	store *store.Store
	log   *slog.Logger
	tmpl  *template.Template
}

// NewHandler returns the UI handler. It serves the page at / and its
// stylesheet under /static/. The page is driven by query parameters:
//
//	chat        numeric chat id to show
//	q           full-text search
//	everywhere  search all chats even though chat is set
//	deleted     include deleted messages
//	offset      paging
//	around      message id to show in context (requires chat)
func NewHandler(s *store.Store, log *slog.Logger) (http.Handler, error) {
	tmpl, err := template.ParseFS(_templateFiles, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	static, err := fs.Sub(_staticFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("open static files: %w", err)
	}

	h := &handler{
		store: s,
		log:   log,
		tmpl:  tmpl,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	return securityHeaders(mux), nil
}

// securityHeaders locks the page down: nothing but same-origin resources may
// load, and no script runs at all.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
