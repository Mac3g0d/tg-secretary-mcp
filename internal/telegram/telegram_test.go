package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMediaType(t *testing.T) {
	raw := json.RawMessage(`{}`)
	tests := []struct {
		name string
		give Message
		want string
	}{
		{"plain text", Message{Text: "hi"}, ""},
		{"photo", Message{Photo: raw}, "photo"},
		{"video", Message{Video: raw}, "video"},
		{"video note", Message{VideoNote: raw}, "video_note"},
		{"voice", Message{Voice: raw}, "voice"},
		{"audio", Message{Audio: raw}, "audio"},
		{"animation wins over document", Message{Animation: raw, Document: raw}, "animation"},
		{"document", Message{Document: raw}, "document"},
		{"sticker", Message{Sticker: raw}, "sticker"},
		{"poll", Message{Poll: raw}, "poll"},
		{"location", Message{Location: raw}, "location"},
		{"contact", Message{Contact: raw}, "contact"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.give.MediaType(); got != tt.want {
				t.Errorf("MediaType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContent(t *testing.T) {
	tests := []struct {
		name string
		give Message
		want string
	}{
		{"text", Message{Text: "t", Caption: "c"}, "t"},
		{"caption fallback", Message{Caption: "c"}, "c"},
		{"empty", Message{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.give.Content(); got != tt.want {
				t.Errorf("Content() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUserDisplayName(t *testing.T) {
	tests := []struct {
		name string
		give User
		want string
	}{
		{"full name", User{FirstName: "Ada", LastName: "Lovelace", Username: "ada"}, "Ada Lovelace"},
		{"first only", User{FirstName: "Ada"}, "Ada"},
		{"last only", User{LastName: "Lovelace"}, "Lovelace"},
		{"username fallback", User{Username: "ada"}, "ada"},
		{"blank names fall back", User{FirstName: " ", Username: "ada"}, "ada"},
		{"empty", User{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.give.DisplayName(); got != tt.want {
				t.Errorf("DisplayName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChatDisplayName(t *testing.T) {
	tests := []struct {
		name string
		give Chat
		want string
	}{
		{"title wins", Chat{Title: "Team", FirstName: "Ada"}, "Team"},
		{"private name", Chat{FirstName: "Ada", LastName: "Lovelace"}, "Ada Lovelace"},
		{"username fallback", Chat{Username: "chan"}, "chan"},
		{"empty", Chat{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.give.DisplayName(); got != tt.want {
				t.Errorf("DisplayName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// newTestServer returns a server replying with body and capturing the request.
func newTestServer(
	t *testing.T, status int, body string, captured *capturedRequest,
) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			captured.path = r.URL.Path
			captured.method = r.Method
			captured.contentType = r.Header.Get("Content-Type")
			_ = json.NewDecoder(r.Body).Decode(&captured.body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

type capturedRequest struct {
	path, method, contentType string
	body                      map[string]any
}

func TestSendMessage(t *testing.T) {
	tests := []struct {
		name string
		give SendParams
		want map[string]any
	}{
		{
			name: "plain",
			give: SendParams{ChatID: 42, Text: "hello"},
			want: map[string]any{"chat_id": 42.0, "text": "hello"},
		},
		{
			name: "reply",
			give: SendParams{ChatID: 42, Text: "hi", ReplyToMessageID: 7},
			want: map[string]any{
				"chat_id": 42.0, "text": "hi",
				"reply_parameters": map[string]any{"message_id": 7.0},
			},
		},
		{
			name: "business",
			give: SendParams{ChatID: -1, Text: "x", BusinessConnectionID: "bc"},
			want: map[string]any{"chat_id": -1.0, "text": "x", "business_connection_id": "bc"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got capturedRequest
			ts := newTestServer(t, http.StatusOK,
				`{"ok":true,"result":{"message_id":99,"chat":{"id":42},"text":"echo"}}`, &got)

			msg, err := New("TOKEN", ts.URL).SendMessage(context.Background(), tt.give)
			if err != nil {
				t.Fatalf("SendMessage: %v", err)
			}
			if msg.MessageID != 99 || msg.Text != "echo" || msg.Chat.ID != 42 {
				t.Errorf("result = %+v", msg)
			}
			if got.path != "/botTOKEN/sendMessage" || got.method != http.MethodPost ||
				got.contentType != "application/json" {
				t.Errorf("request = %+v", got)
			}
			if !mapsEqual(got.body, tt.want) {
				t.Errorf("body = %v, want %v", got.body, tt.want)
			}
		})
	}
}

func mapsEqual(a, b map[string]any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestGetMe(t *testing.T) {
	ts := newTestServer(t, http.StatusOK,
		`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Bot","username":"b_bot"}}`, nil)
	u, err := New("t", ts.URL+"/").GetMe(context.Background()) // trailing slash trimmed
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 || !u.IsBot || u.Username != "b_bot" {
		t.Errorf("user = %+v", u)
	}
}

func TestGetUpdates(t *testing.T) {
	var got capturedRequest
	ts := newTestServer(t, http.StatusOK, `{"ok":true,"result":[
		{"update_id":5,"message":{"message_id":1,"chat":{"id":2},"text":"a"}},
		{"update_id":6,"deleted_business_messages":{"message_ids":[1,2]}}]}`, &got)

	updates, err := New("t", ts.URL).GetUpdates(context.Background(), 5, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || updates[0].Message == nil || updates[0].UpdateID != 5 ||
		updates[1].DeletedBusinessMessages == nil ||
		!slices.Equal(updates[1].DeletedBusinessMessages.MessageIDs, []int64{1, 2}) {
		t.Errorf("updates = %+v", updates)
	}
	if got.body["offset"] != 5.0 || got.body["timeout"] != 30.0 {
		t.Errorf("body = %v", got.body)
	}
	allowed, _ := got.body["allowed_updates"].([]any)
	if len(allowed) != len(allowedUpdates()) {
		t.Errorf("allowed_updates = %v", allowed)
	}
}

func TestAllowedUpdatesIsFresh(t *testing.T) {
	a := allowedUpdates()
	a[0] = "mutated"
	if allowedUpdates()[0] == "mutated" {
		t.Error("allowedUpdates must return a fresh slice")
	}
}

func TestAPIError(t *testing.T) {
	tests := []struct {
		name      string
		giveCode  int
		giveBody  string
		wantCode  int
		wantRetry time.Duration
		wantText  string
	}{
		{
			name:      "rate limited",
			giveCode:  http.StatusTooManyRequests,
			giveBody:  `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`,
			wantCode:  429,
			wantRetry: 7 * time.Second,
			wantText:  "telegram: Too Many Requests (429)",
		},
		{
			name:     "unauthorized",
			giveCode: http.StatusUnauthorized,
			giveBody: `{"ok":false,"error_code":401,"description":"Unauthorized"}`,
			wantCode: 401,
			wantText: "telegram: Unauthorized (401)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t, tt.giveCode, tt.giveBody, nil)
			_, err := New("t", ts.URL).GetUpdates(context.Background(), 0, time.Second)

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.Code != tt.wantCode || apiErr.RetryAfter != tt.wantRetry {
				t.Errorf("apiErr = %+v", apiErr)
			}
			if err.Error() != tt.wantText {
				t.Errorf("text = %q, want %q", err, tt.wantText)
			}
		})
	}
}

func TestBadResponse(t *testing.T) {
	ts := newTestServer(t, http.StatusBadGateway, `<html>oops</html>`, nil)
	_, err := New("t", ts.URL).GetMe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad response (HTTP 502)") {
		t.Errorf("err = %v", err)
	}
}

func TestTokenIsNotLeakedInErrors(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		giveCtx context.Context
		wantIs  error
	}{
		{"connection refused", context.Background(), nil},
		{"canceled context", canceled, context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New("123:SECRET", "http://127.0.0.1:1") // nothing listens here
			_, err := c.GetMe(tt.giveCtx)
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("token leaked: %v", err)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("err = %v, want errors.Is %v", err, tt.wantIs)
			}
		})
	}
}
