// Package app wires the bot, the MCP server and the web UI together and runs
// them until one of them stops or the context is cancelled.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/collector"
	"github.com/mac3g0d/tg-secretary-mcp/internal/config"
	"github.com/mac3g0d/tg-secretary-mcp/internal/httpserver"
	"github.com/mac3g0d/tg-secretary-mcp/internal/mcpserver"
	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
	"github.com/mac3g0d/tg-secretary-mcp/internal/web"
)

// Options selects which components run in the process.
type Options struct {
	// Config holds the runtime settings. It is required.
	Config *config.Config
	// Log receives progress and error messages. It is required.
	Log *slog.Logger
	// Version is reported to MCP clients.
	Version string

	// Bot runs the Telegram message collector.
	Bot bool
	// HTTP serves the MCP endpoint and the web UI.
	HTTP bool
	// Stdio serves MCP over standard input and output.
	Stdio bool
}

// Run starts the selected components and blocks until the first of them
// stops, then cancels the rest and waits for them. A stop caused by ctx
// being cancelled is not an error.
func Run(ctx context.Context, opts Options) error {
	if err := validate(opts); err != nil {
		return err
	}
	cfg := opts.Config

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	// Created up front so there is an obvious place to drop chat exports.
	// Failure is not fatal: the import tool reports a missing directory.
	if cfg.ImportDir != "" {
		if err := os.MkdirAll(cfg.ImportDir, 0o700); err != nil {
			opts.Log.Warn("create import directory", "dir", cfg.ImportDir, "error", err)
		}
	}

	api := telegram.New(cfg.BotToken, cfg.APIURL)
	mcpOpts := mcpserver.Options{
		Store:     db,
		Version:   opts.Version,
		ImportDir: cfg.ImportDir,
	}
	if cfg.AllowSend {
		mcpOpts.Sender = api
	}

	// The first component to stop (or fail) brings the whole process down.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g := newGroup()

	if opts.Bot {
		c := collector.New(collector.Config{
			API:          api,
			Store:        db,
			Log:          opts.Log,
			AllowedChats: cfg.AllowedChats,
		})
		g.Go(func() error { return c.Run(ctx) })
	}
	if opts.HTTP {
		srv, err := newHTTPServer(opts, db, mcpserver.New(mcpOpts))
		if err != nil {
			cancel()
			g.Wait()
			return err
		}
		g.Go(func() error { return srv.Run(ctx) })
	}
	if opts.Stdio {
		srv := mcpserver.New(mcpOpts)
		g.Go(func() error { return srv.Run(ctx, &mcp.StdioTransport{}) })
	}

	err = g.First()
	cancel()
	g.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// validate rejects option and configuration combinations that cannot work.
func validate(opts Options) error {
	cfg := opts.Config
	if opts.Bot && cfg.BotToken == "" {
		return errors.New("TELEGRAM_BOT_TOKEN is not set")
	}
	if cfg.AllowSend && cfg.BotToken == "" {
		return errors.New("TG_ALLOW_SEND requires TELEGRAM_BOT_TOKEN")
	}
	if opts.HTTP && cfg.AuthToken == "" && !httpserver.IsLoopback(cfg.Addr) {
		return fmt.Errorf(
			"refusing to expose the message archive on %s without MCP_AUTH_TOKEN", cfg.Addr)
	}
	return nil
}

// newHTTPServer builds the HTTP server for the MCP endpoint and, when enabled,
// the web UI.
func newHTTPServer(opts Options, db *store.Store, mcpSrv *mcp.Server) (*httpserver.Server, error) {
	cfg := opts.Config
	hc := httpserver.Config{
		Addr:      cfg.Addr,
		AuthToken: cfg.AuthToken,
		Log:       opts.Log,
		MCP: mcp.NewStreamableHTTPHandler(
			func(*http.Request) *mcp.Server { return mcpSrv },
			&mcp.StreamableHTTPOptions{
				// The SDK default (4 MiB) is too small for an inline chat export.
				MaxRequestBodyBytes: mcpserver.MaxRequestBodyBytes,
			}),
	}
	if cfg.WebUI {
		ui, err := web.NewHandler(db, opts.Log)
		if err != nil {
			return nil, fmt.Errorf("web UI: %w", err)
		}
		hc.UI = ui
	}
	return httpserver.New(hc), nil
}

// group runs functions in goroutines and reports their results.
type group struct {
	wg   sync.WaitGroup
	errc chan error
	n    int
}

func newGroup() *group {
	// Capacity for every component Run can start.
	return &group{errc: make(chan error, 3)}
}

// Go runs f in a new goroutine.
func (g *group) Go(f func() error) {
	g.n++
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.errc <- f()
	}()
}

// First blocks until some function returns and yields its result.
func (g *group) First() error {
	if g.n == 0 {
		return nil
	}
	return <-g.errc
}

// Wait blocks until all functions have returned.
func (g *group) Wait() {
	g.wg.Wait()
}
