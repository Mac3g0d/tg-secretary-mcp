package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var _envKeys = []string{
	"TELEGRAM_BOT_TOKEN",
	"TELEGRAM_API_URL",
	"TG_DB_PATH",
	"TG_IMPORT_DIR",
	"TG_AVATARS",
	"TG_ALLOWED_CHATS",
	"TG_ALLOW_SEND",
	"MCP_ADDR",
	"MCP_AUTH_TOKEN",
	"WEB_UI",
}

// isolate leaves every configuration variable unset for the test and moves
// into an empty working directory so no real .env file is picked up.
func isolate(t *testing.T) string {
	t.Helper()
	for _, key := range _envKeys {
		// t.Setenv registers the cleanup that restores the original value.
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		dotEnv  string
		want    Config
		wantErr string
	}{
		{
			name: "defaults",
			want: Config{
				DBPath:       "data/secretary.db",
				Addr:         "127.0.0.1:8080",
				WebUI:        true,
				ImportDir:    "data/import",
				Avatars:      true,
				AllowedChats: map[int64]bool{},
			},
		},
		{
			name: "all variables",
			env: map[string]string{
				"TELEGRAM_BOT_TOKEN": "1:abc",
				"TELEGRAM_API_URL":   "http://localhost:8081",
				"TG_DB_PATH":         "/tmp/x.db",
				"TG_IMPORT_DIR":      "off",
				"TG_AVATARS":         "false",
				"TG_ALLOWED_CHATS":   "1, -100 ,2",
				"TG_ALLOW_SEND":      "true",
				"MCP_ADDR":           "0.0.0.0:9000",
				"MCP_AUTH_TOKEN":     "secret",
				"WEB_UI":             "false",
			},
			want: Config{
				BotToken:     "1:abc",
				APIURL:       "http://localhost:8081",
				DBPath:       "/tmp/x.db",
				AllowedChats: map[int64]bool{1: true, -100: true, 2: true},
				AllowSend:    true,
				Addr:         "0.0.0.0:9000",
				AuthToken:    "secret",
				WebUI:        false,
				ImportDir:    "",
				Avatars:      false,
			},
		},
		{
			name: "empty values fall back to defaults",
			env: map[string]string{
				"TG_DB_PATH": "",
				"MCP_ADDR":   "",
				"WEB_UI":     "",
			},
			want: Config{
				DBPath:       "data/secretary.db",
				Addr:         "127.0.0.1:8080",
				WebUI:        true,
				ImportDir:    "data/import",
				Avatars:      true,
				AllowedChats: map[int64]bool{},
			},
		},
		{
			name: "dotenv supplies values",
			dotEnv: `# comment
TELEGRAM_BOT_TOKEN=1:abc
export MCP_ADDR="127.0.0.1:9999"
MCP_AUTH_TOKEN='tok en'
not a pair
TG_ALLOW_SEND = 1

WEB_UI=0
`,
			want: Config{
				BotToken:     "1:abc",
				DBPath:       "data/secretary.db",
				AllowedChats: map[int64]bool{},
				AllowSend:    true,
				Addr:         "127.0.0.1:9999",
				AuthToken:    "tok en",
				WebUI:        false,
				ImportDir:    "data/import",
				Avatars:      true,
			},
		},
		{
			name:   "environment beats dotenv",
			env:    map[string]string{"MCP_ADDR": "127.0.0.1:1111"},
			dotEnv: "MCP_ADDR=127.0.0.1:2222\nTG_DB_PATH=from-file.db\n",
			want: Config{
				DBPath:       "from-file.db",
				AllowedChats: map[int64]bool{},
				Addr:         "127.0.0.1:1111",
				WebUI:        true,
				ImportDir:    "data/import",
				Avatars:      true,
			},
		},
		{
			name:   "empty environment variable still beats dotenv",
			env:    map[string]string{"TG_DB_PATH": ""},
			dotEnv: "TG_DB_PATH=from-file.db\n",
			want: Config{
				DBPath:       "data/secretary.db",
				AllowedChats: map[int64]bool{},
				Addr:         "127.0.0.1:8080",
				WebUI:        true,
				ImportDir:    "data/import",
				Avatars:      true,
			},
		},
		{
			name:    "invalid WEB_UI",
			env:     map[string]string{"WEB_UI": "maybe"},
			wantErr: `WEB_UI: "maybe" is not a boolean`,
		},
		{
			name:    "invalid TG_ALLOW_SEND",
			env:     map[string]string{"TG_ALLOW_SEND": "yes"},
			wantErr: `TG_ALLOW_SEND: "yes" is not a boolean`,
		},
		{
			name:    "invalid boolean in dotenv",
			dotEnv:  "TG_ALLOW_SEND=nope\n",
			wantErr: `TG_ALLOW_SEND: "nope" is not a boolean`,
		},
		{
			name:    "invalid TG_ALLOWED_CHATS",
			env:     map[string]string{"TG_ALLOWED_CHATS": "1,abc"},
			wantErr: `TG_ALLOWED_CHATS: "abc" is not a chat id`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := isolate(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if tt.dotEnv != "" {
				path := filepath.Join(dir, ".env")
				if err := os.WriteFile(path, []byte(tt.dotEnv), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := Load()
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Load() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("Load() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestParseIDs(t *testing.T) {
	tests := []struct {
		name    string
		give    string
		want    map[int64]bool
		wantErr bool
	}{
		{name: "empty", give: "", want: map[int64]bool{}},
		{name: "single", give: "42", want: map[int64]bool{42: true}},
		{
			name: "negative and spaces",
			give: " -1001234567890 , 7 ",
			want: map[int64]bool{-1001234567890: true, 7: true},
		},
		{name: "skips empty parts", give: "1,,2,", want: map[int64]bool{1: true, 2: true}},
		{name: "duplicates", give: "5,5", want: map[int64]bool{5: true}},
		{name: "not a number", give: "1,x", wantErr: true},
		{name: "username is not an id", give: "@chat", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIDs(tt.give)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseIDs(%q) error = %v, wantErr %v", tt.give, err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseIDs(%q) = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}

func TestParseDotEnvLine(t *testing.T) {
	tests := []struct {
		name      string
		give      string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{name: "plain", give: "A=b", wantKey: "A", wantValue: "b", wantOK: true},
		{name: "export prefix", give: "export A=b", wantKey: "A", wantValue: "b", wantOK: true},
		{name: "spaces", give: "  A = b  ", wantKey: "A", wantValue: "b", wantOK: true},
		{name: "double quotes", give: `A="x y"`, wantKey: "A", wantValue: "x y", wantOK: true},
		{name: "single quotes", give: `A='x y'`, wantKey: "A", wantValue: "x y", wantOK: true},
		{name: "mismatched quotes", give: `A="x'`, wantKey: "A", wantValue: `"x'`, wantOK: true},
		{name: "lone quote", give: `A="`, wantKey: "A", wantValue: `"`, wantOK: true},
		{name: "value with equals", give: "A=b=c", wantKey: "A", wantValue: "b=c", wantOK: true},
		{name: "empty value", give: "A=", wantKey: "A", wantValue: "", wantOK: true},
		{name: "comment", give: "# A=b"},
		{name: "blank", give: "   "},
		{name: "no equals", give: "A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, value, ok := parseDotEnvLine(tt.give)
			if key != tt.wantKey || value != tt.wantValue || ok != tt.wantOK {
				t.Errorf("parseDotEnvLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.give, key, value, ok, tt.wantKey, tt.wantValue, tt.wantOK)
			}
		})
	}
}
