package mcpserver

import (
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*60*60)
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, loc)

	tests := []struct {
		name    string
		give    string
		want    time.Time
		wantErr bool
	}{
		{name: "empty", give: "", want: time.Time{}},
		{name: "blank", give: "   ", want: time.Time{}},
		{name: "minutes", give: "30m", want: now.Add(-30 * time.Minute)},
		{name: "hours", give: "24h", want: now.Add(-24 * time.Hour)},
		{name: "days", give: "7d", want: now.AddDate(0, 0, -7)},
		{name: "weeks", give: "2w", want: now.Add(-14 * 24 * time.Hour)},
		{name: "unit with space and capital", give: " 5 H ", want: now.Add(-5 * time.Hour)},
		{
			name: "RFC 3339 with offset",
			give: "2026-01-02T03:04:05+02:00",
			want: time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("", 2*60*60)),
		},
		{
			name: "RFC 3339 lower case",
			give: "2026-01-02t03:04:05z",
			want: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		},
		{
			name: "local date",
			give: "2026-01-02",
			want: time.Date(2026, 1, 2, 0, 0, 0, 0, loc),
		},
		{
			name: "local date and minutes",
			give: "2026-01-02 03:04",
			want: time.Date(2026, 1, 2, 3, 4, 0, 0, loc),
		},
		{
			name: "local date, T and seconds",
			give: "2026-01-02T03:04:05",
			want: time.Date(2026, 1, 2, 3, 4, 5, 0, loc),
		},
		{name: "words", give: "yesterday", wantErr: true},
		{name: "unknown unit", give: "5y", wantErr: true},
		{name: "negative", give: "-5h", wantErr: true},
		{name: "no number", give: "h", wantErr: true},
		{name: "overflowing number", give: "99999999999999999999d", wantErr: true},
		{name: "impossible date", give: "2026-13-45", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTime(tt.give, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseTime(%q) error = %v, wantErr %v", tt.give, err, tt.wantErr)
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("parseTime(%q) = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}

func TestClampWindow(t *testing.T) {
	ptr := func(n int) *int { return &n }
	tests := []struct {
		name string
		give *int
		want int
	}{
		{name: "unset uses the default", give: nil, want: 5},
		{name: "zero is allowed", give: ptr(0), want: 0},
		{name: "in range", give: ptr(12), want: 12},
		{name: "too large", give: ptr(500), want: 50},
		{name: "negative", give: ptr(-3), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampWindow(tt.give); got != tt.want {
				t.Errorf("clampWindow = %d, want %d", got, tt.want)
			}
		})
	}
}
