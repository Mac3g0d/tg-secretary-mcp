// Command tg-secretary runs a Telegram bot that archives messages into SQLite
// and an MCP server that lets AI agents query the archive.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mac3g0d/tg-secretary-mcp/internal/app"
	"github.com/mac3g0d/tg-secretary-mcp/internal/config"
)

// version is set at build time:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/tg-secretary
var version = "dev"

const _usage = `tg-secretary — Telegram secretary bot with an MCP server

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
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		// Logs always go to stderr: in stdio mode stdout carries the MCP protocol.
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error(err.Error())
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, _usage)
		return errors.New("a command is required")
	}

	cmd := args[0]
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, _usage)
		return nil
	case "serve", "bot", "stdio":
	default:
		fmt.Fprint(stderr, _usage)
		return fmt.Errorf("unknown command %q", cmd)
	}

	opts, err := parseFlags(cmd, args[1:], stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	opts.Config = cfg
	opts.Log = slog.New(slog.NewTextHandler(stderr, nil))
	opts.Version = version

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, opts)
}

// parseFlags returns the components cmd runs, adjusted by its flags.
func parseFlags(cmd string, args []string, stderr io.Writer) (app.Options, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	noBot := fs.Bool("no-bot", false, "serve: do not run the bot in this process")
	withBot := fs.Bool("with-bot", false, "stdio: also run the bot in this process")
	if err := fs.Parse(args); err != nil {
		return app.Options{}, err
	}

	switch cmd {
	case "serve":
		return app.Options{Bot: !*noBot, HTTP: true}, nil
	case "bot":
		return app.Options{Bot: true}, nil
	default: // "stdio"
		return app.Options{Bot: *withBot, Stdio: true}, nil
	}
}
