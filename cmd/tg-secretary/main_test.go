package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mac3g0d/tg-secretary-mcp/internal/app"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		give       []string
		wantErr    string
		wantStdout string
		wantStderr string
	}{
		{name: "no command", wantErr: "a command is required", wantStderr: "Usage:"},
		{name: "version", give: []string{"version"}, wantStdout: version + "\n"},
		{name: "help", give: []string{"help"}, wantStdout: "Usage:"},
		{name: "--help", give: []string{"--help"}, wantStdout: "Usage:"},
		{
			name:       "unknown command",
			give:       []string{"frobnicate"},
			wantErr:    `unknown command "frobnicate"`,
			wantStderr: "Usage:",
		},
		{
			name:       "unknown flag",
			give:       []string{"serve", "-bogus"},
			wantErr:    "flag provided but not defined: -bogus",
			wantStderr: "-no-bot",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(tt.give, &stdout, &stderr)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr) {
				t.Fatalf("run() error = %v, want %q", err, tt.wantErr)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		give []string
		want app.Options
	}{
		{name: "serve", cmd: "serve", want: app.Options{Bot: true, HTTP: true}},
		{name: "serve -no-bot", cmd: "serve", give: []string{"-no-bot"}, want: app.Options{HTTP: true}},
		{name: "bot", cmd: "bot", want: app.Options{Bot: true}},
		{name: "stdio", cmd: "stdio", want: app.Options{Stdio: true}},
		{
			name: "stdio -with-bot",
			cmd:  "stdio",
			give: []string{"-with-bot"},
			want: app.Options{Bot: true, Stdio: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFlags(tt.cmd, tt.give, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("parseFlags = %+v, want %+v", got, tt.want)
			}
		})
	}
}
