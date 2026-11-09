package store

import "testing"

func TestFTSTerms(t *testing.T) {
	tests := []struct {
		name string
		give string
		want string
	}{
		{"empty", "", ""},
		{"blank", "  \t", ""},
		{"single word", "hello", `"hello"*`},
		{"multiple words", "hello  world", `"hello"* "world"*`},
		{"quotes stripped", `he"llo "`, `"hello"*`},
		{"only quotes", `" ""`, ""},
		{"operators quoted", "a AND b", `"a"* "AND"* "b"*`},
		{"cyrillic", "Привет", `"Привет"*`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ftsTerms(tt.give); got != tt.want {
				t.Errorf("ftsTerms(%q) = %q, want %q", tt.give, got, tt.want)
			}
		})
	}
}

func TestPlaceholders(t *testing.T) {
	tests := []struct {
		give int
		want string
	}{
		{0, ""},
		{1, "?"},
		{3, "?,?,?"},
	}
	for _, tt := range tests {
		if got := placeholders(tt.give); got != tt.want {
			t.Errorf("placeholders(%d) = %q, want %q", tt.give, got, tt.want)
		}
	}
}
