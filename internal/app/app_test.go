package app

import (
	"strings"
	"testing"

	"github.com/mac3g0d/tg-secretary-mcp/internal/config"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		give    Options
		wantErr string
	}{
		{
			name: "serve on loopback without a token",
			give: Options{
				HTTP:   true,
				Config: &config.Config{Addr: "127.0.0.1:8080"},
			},
		},
		{
			name: "serve on all interfaces without a token",
			give: Options{
				HTTP:   true,
				Config: &config.Config{Addr: "0.0.0.0:8080"},
			},
			wantErr: "without MCP_AUTH_TOKEN",
		},
		{
			name: "serve on all interfaces with a token",
			give: Options{
				HTTP:   true,
				Config: &config.Config{Addr: "0.0.0.0:8080", AuthToken: "t"},
			},
		},
		{
			name: "stdio ignores the listen address",
			give: Options{
				Stdio:  true,
				Config: &config.Config{Addr: "0.0.0.0:8080"},
			},
		},
		{
			name:    "bot needs a token",
			give:    Options{Bot: true, Config: &config.Config{}},
			wantErr: "TELEGRAM_BOT_TOKEN is not set",
		},
		{
			name:    "bot with a token",
			give:    Options{Bot: true, Config: &config.Config{BotToken: "1:a"}},
			wantErr: "",
		},
		{
			name:    "sending needs a token",
			give:    Options{Stdio: true, Config: &config.Config{AllowSend: true}},
			wantErr: "TG_ALLOW_SEND requires TELEGRAM_BOT_TOKEN",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.give)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("validate() = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
