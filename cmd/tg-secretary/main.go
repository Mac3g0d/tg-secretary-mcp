// Command tg-secretary runs a Telegram bot that archives messages into SQLite
// and an MCP server that lets AI agents query the archive.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/collector"
	"github.com/mac3g0d/tg-secretary-mcp/internal/config"
	"github.com/mac3g0d/tg-secretary-mcp/internal/mcpserver"
	"github.com/mac3g0d/tg-secretary-mcp/internal/store"
	"github.com/mac3g0d/tg-secretary-mcp/internal/telegram"
	"github.com/mac3g0d/tg-secretary-mcp/internal/web"
)

const usage = `tg-secretary — Telegram secretary bot with an MCP server

Usage:
  tg-secretary serve   run the bot, the MCP server over HTTP and the web UI (recommended)
  tg-secretary bot     run only the bot (message collector)
  tg-secretary stdio   run only the MCP server over stdio, reading the same database
  tg-secretary version

Flags for serve and stdio:
  -no-bot / -with-bot  override whether the bot runs in this process

Configuration comes from environment variables or a .env file, see .env.example.
`

func main() {
	// Logs always go to stderr: in stdio mode stdout carries the MCP protocol.
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, os.Args[1:]); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("a command is required")
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	noBot := fs.Bool("no-bot", false, "serve: do not run the bot in this process")
	withBot := fs.Bool("with-bot", false, "stdio: also run the bot in this process")
	_ = fs.Parse(args[1:])

	var runBot, runHTTP, runStdio bool
	switch cmd {
	case "serve":
		runBot, runHTTP = !*noBot, true
	case "bot":
		runBot = true
	case "stdio":
		runBot, runStdio = *withBot, true
	case "version":
		fmt.Println(mcpserver.Version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if runBot && cfg.BotToken == "" {
		return errors.New("TELEGRAM_BOT_TOKEN is not set")
	}
	if cfg.AllowSend && cfg.BotToken == "" {
		return errors.New("TG_ALLOW_SEND requires TELEGRAM_BOT_TOKEN")
	}
	if runHTTP && cfg.AuthToken == "" && !isLoopback(cfg.Addr) {
		return fmt.Errorf("refusing to expose the message archive on %s without MCP_AUTH_TOKEN", cfg.Addr)
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	api := telegram.New(cfg.BotToken, cfg.APIURL)
	opts := mcpserver.Options{Store: db}
	if cfg.AllowSend {
		opts.Sender = api
	}

	// The first component to stop (or fail) brings the whole process down.
	errc := make(chan error, 3)
	if runBot {
		c := &collector.Collector{API: api, Store: db, Log: log, AllowedChats: cfg.AllowedChats}
		go func() { errc <- c.Run(ctx) }()
	}
	if runHTTP {
		go func() { errc <- serveHTTP(ctx, log, cfg, db, mcpserver.New(opts)) }()
	}
	if runStdio {
		go func() { errc <- mcpserver.New(opts).Run(ctx, &mcp.StdioTransport{}) }()
	}
	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return err
}

func serveHTTP(ctx context.Context, log *slog.Logger, cfg *config.Config, db *store.Store, srv *mcp.Server) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", tokenAuth(cfg.AuthToken, handler))
	if cfg.WebUI {
		ui, err := web.NewHandler(db, log)
		if err != nil {
			return fmt.Errorf("web UI: %w", err)
		}
		mux.Handle("/", tokenAuth(cfg.AuthToken, ui))
		log.Info("web UI enabled", "url", "http://"+cfg.Addr+"/")
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })

	httpSrv := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()
	log.Info("MCP server listening", "url", "http://"+cfg.Addr+"/mcp", "auth", cfg.AuthToken != "")
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// tokenAuth protects a handler when a token is configured. Agents send
// "Authorization: Bearer <token>"; browsers use HTTP Basic auth with the token
// as the password (any user name).
func tokenAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			_, got, _ = r.BasicAuth()
		}
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="tg-secretary", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
