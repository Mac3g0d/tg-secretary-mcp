// Package httpserver serves the MCP endpoint and the optional web UI over
// HTTP, with token authentication.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	_readHeaderTimeout = 10 * time.Second
	_shutdownTimeout   = 5 * time.Second
)

// Config configures a Server.
type Config struct {
	// Addr is the listen address, for example "127.0.0.1:8080".
	Addr string
	// AuthToken protects /mcp and the UI when non-empty. Clients present it as
	// a Bearer token or as the HTTP Basic password.
	AuthToken string
	// MCP serves /mcp. It is required.
	MCP http.Handler
	// UI serves / and /static/ when non-nil.
	UI http.Handler
	// Log receives lifecycle messages. It defaults to slog.Default().
	Log *slog.Logger
}

// Server is an HTTP server for the archive.
type Server struct {
	srv *http.Server
	log *slog.Logger

	addr    string
	hasAuth bool
	hasUI   bool
}

// New returns a Server for cfg.
func New(cfg Config) *Server {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", TokenAuth(cfg.AuthToken, cfg.MCP))
	if cfg.UI != nil {
		mux.Handle("/", TokenAuth(cfg.AuthToken, cfg.UI))
	}
	mux.HandleFunc("/healthz", healthz)

	return &Server{
		srv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: _readHeaderTimeout,
		},
		log:     log,
		addr:    cfg.Addr,
		hasAuth: cfg.AuthToken != "",
		hasUI:   cfg.UI != nil,
	}
}

// Handler returns the server's request handler.
func (s *Server) Handler() http.Handler {
	return s.srv.Handler
}

// Run listens and serves until ctx is cancelled, then shuts down gracefully.
// A clean shutdown returns nil.
func (s *Server) Run(ctx context.Context) error {
	if s.hasUI {
		s.log.Info("web UI enabled", "url", "http://"+s.addr+"/")
	}
	s.log.Info("MCP server listening", "url", "http://"+s.addr+"/mcp", "auth", s.hasAuth)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.srv.ListenAndServe() }()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}

	// ctx is already cancelled, so shutdown needs its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), _shutdownTimeout)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down http server: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}
	return nil
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintln(w, "ok")
}
