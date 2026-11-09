package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTokenIsNotLeakedInErrors(t *testing.T) {
	c := New("123:SECRET", "http://127.0.0.1:1") // nothing listens here
	_, err := c.GetMe(context.Background())
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("token leaked: %v", err)
	}
}

func TestAPIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`))
	}))
	defer ts.Close()
	_, err := New("t", ts.URL).GetUpdates(context.Background(), 0, time.Second)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 429 || apiErr.RetryAfter != 7*time.Second {
		t.Fatalf("unexpected error: %v", err)
	}
}
