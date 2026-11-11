// Package config reads settings from environment variables and an optional
// .env file.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	_defaultDBPath = "data/secretary.db"
	_defaultAddr   = "127.0.0.1:8080"
	_defaultImport = "data/import"
	_importOff     = "off"
	_dotEnvPath    = ".env"
)

// Config holds all runtime settings.
type Config struct {
	// BotToken is TELEGRAM_BOT_TOKEN.
	BotToken string
	// APIURL is TELEGRAM_API_URL (optional, for a self-hosted Bot API server).
	APIURL string
	// DBPath is TG_DB_PATH.
	DBPath string
	// AllowedChats is TG_ALLOWED_CHATS (comma-separated chat IDs; empty = all).
	AllowedChats map[int64]bool
	// AllowSend is TG_ALLOW_SEND.
	AllowSend bool
	// Addr is MCP_ADDR.
	Addr string
	// AuthToken is MCP_AUTH_TOKEN.
	AuthToken string
	// WebUI is WEB_UI (default true): serve the browser UI next to /mcp.
	WebUI bool
	// ImportDir is TG_IMPORT_DIR: the directory agents may import chat
	// exports from. It is empty when the variable is "off".
	ImportDir string
}

// Load reads the configuration. Variables already present in the environment
// take precedence over the .env file in the working directory.
func Load() (*Config, error) {
	if err := loadDotEnv(_dotEnvPath); err != nil {
		return nil, err
	}

	webUI, err := envBool("WEB_UI", true)
	if err != nil {
		return nil, err
	}
	allowSend, err := envBool("TG_ALLOW_SEND", false)
	if err != nil {
		return nil, err
	}
	allowedChats, err := parseIDs(os.Getenv("TG_ALLOWED_CHATS"))
	if err != nil {
		return nil, fmt.Errorf("TG_ALLOWED_CHATS: %w", err)
	}

	importDir := envString("TG_IMPORT_DIR", _defaultImport)
	if importDir == _importOff {
		importDir = ""
	}

	return &Config{
		ImportDir:    importDir,
		BotToken:     os.Getenv("TELEGRAM_BOT_TOKEN"),
		APIURL:       os.Getenv("TELEGRAM_API_URL"),
		DBPath:       envString("TG_DB_PATH", _defaultDBPath),
		AllowedChats: allowedChats,
		AllowSend:    allowSend,
		Addr:         envString("MCP_ADDR", _defaultAddr),
		AuthToken:    os.Getenv("MCP_AUTH_TOKEN"),
		WebUI:        webUI,
	}, nil
}

// envString returns the variable's value, or def when it is unset or empty.
func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envBool parses the variable as a boolean, or returns def when it is unset
// or empty.
func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %q is not a boolean", key, v)
	}
	return b, nil
}

// parseIDs parses a comma-separated list of chat IDs into a set.
func parseIDs(s string) (map[int64]bool, error) {
	parts := strings.Split(s, ",")
	ids := make(map[int64]bool, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a chat id", part)
		}
		ids[id] = true
	}
	return ids, nil
}
