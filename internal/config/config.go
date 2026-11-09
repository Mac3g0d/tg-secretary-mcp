// Package config reads settings from environment variables and an optional
// .env file.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime settings.
type Config struct {
	BotToken     string         // TELEGRAM_BOT_TOKEN
	APIURL       string         // TELEGRAM_API_URL (optional, for a self-hosted Bot API server)
	DBPath       string         // TG_DB_PATH
	AllowedChats map[int64]bool // TG_ALLOWED_CHATS (comma-separated chat IDs; empty = all)
	AllowSend    bool           // TG_ALLOW_SEND
	Addr         string         // MCP_ADDR
	AuthToken    string         // MCP_AUTH_TOKEN
}

// Load reads the configuration. Variables already present in the environment
// take precedence over the .env file in the working directory.
func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, err
	}
	cfg := &Config{
		BotToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
		APIURL:    os.Getenv("TELEGRAM_API_URL"),
		DBPath:    getenv("TG_DB_PATH", "data/secretary.db"),
		Addr:      getenv("MCP_ADDR", "127.0.0.1:8080"),
		AuthToken: os.Getenv("MCP_AUTH_TOKEN"),
	}
	var err error
	if v := os.Getenv("TG_ALLOW_SEND"); v != "" {
		if cfg.AllowSend, err = strconv.ParseBool(v); err != nil {
			return nil, fmt.Errorf("TG_ALLOW_SEND: %q is not a boolean", v)
		}
	}
	if cfg.AllowedChats, err = parseIDs(os.Getenv("TG_ALLOWED_CHATS")); err != nil {
		return nil, fmt.Errorf("TG_ALLOWED_CHATS: %w", err)
	}
	return cfg, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseIDs(s string) (map[int64]bool, error) {
	ids := map[int64]bool{}
	for _, part := range strings.Split(s, ",") {
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

// loadDotEnv sets variables from KEY=VALUE lines; a missing file is fine.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return sc.Err()
}
