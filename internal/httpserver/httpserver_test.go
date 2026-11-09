package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ok(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, "ok")
}

func TestTokenAuth(t *testing.T) {
	const token = "s3cret"
	tests := []struct {
		name       string
		token      string
		setup      func(*http.Request)
		wantStatus int
	}{
		{
			name:       "no token configured is open",
			token:      "",
			setup:      func(*http.Request) {},
			wantStatus: http.StatusOK,
		},
		{
			name:       "bearer",
			token:      token,
			setup:      func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) },
			wantStatus: http.StatusOK,
		},
		{
			name:       "basic password",
			token:      token,
			setup:      func(r *http.Request) { r.SetBasicAuth("anyone", token) },
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing credentials",
			token:      token,
			setup:      func(*http.Request) {},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong bearer",
			token:      token,
			setup:      func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") },
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong basic password",
			token:      token,
			setup:      func(r *http.Request) { r.SetBasicAuth("anyone", "nope") },
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "token as basic user name is not enough",
			token:      token,
			setup:      func(r *http.Request) { r.SetBasicAuth(token, "") },
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "bearer prefix is case sensitive",
			token:      token,
			setup:      func(r *http.Request) { r.Header.Set("Authorization", "bearer "+token) },
			wantStatus: http.StatusUnauthorized,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := TokenAuth(tt.token, http.HandlerFunc(ok))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			tt.setup(req)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			challenge := rec.Header().Get("WWW-Authenticate")
			if tt.wantStatus == http.StatusUnauthorized && !strings.HasPrefix(challenge, "Basic ") {
				t.Errorf("WWW-Authenticate = %q, want a Basic challenge", challenge)
			}
			if tt.wantStatus == http.StatusOK && rec.Body.String() != "ok" {
				t.Errorf("body = %q, want the wrapped handler's response", rec.Body.String())
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"127.5.5.5:1", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{":8080", false},
		{"192.168.1.10:8080", false},
		{"example.com:80", false},
		{"127.0.0.1", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := IsLoopback(tt.addr); got != tt.want {
				t.Errorf("IsLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestRoutes(t *testing.T) {
	mcpH := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "mcp") })
	uiH := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ui") })

	tests := []struct {
		name       string
		ui         http.Handler
		path       string
		auth       bool
		wantStatus int
		wantBody   string
	}{
		{name: "healthz is always open", ui: uiH, path: "/healthz", wantStatus: 200, wantBody: "ok\n"},
		{name: "mcp needs auth", ui: uiH, path: "/mcp", wantStatus: 401},
		{name: "mcp with auth", ui: uiH, path: "/mcp", auth: true, wantStatus: 200, wantBody: "mcp"},
		{name: "ui needs auth", ui: uiH, path: "/", wantStatus: 401},
		{name: "ui with auth", ui: uiH, path: "/", auth: true, wantStatus: 200, wantBody: "ui"},
		{name: "ui disabled", ui: nil, path: "/", auth: true, wantStatus: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(Config{AuthToken: "tok", MCP: mcpH, UI: tt.ui})
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.auth {
				req.Header.Set("Authorization", "Bearer tok")
			}
			rec := httptest.NewRecorder()

			s.Handler().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestRunShutsDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	s := New(Config{
		Addr: addr,
		MCP:  http.HandlerFunc(ok),
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Wait for the listener to come up.
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunListenError(t *testing.T) {
	s := New(Config{
		Addr: "256.0.0.1:1",
		MCP:  http.HandlerFunc(ok),
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded on an invalid address")
	}
}
