package mcpserver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var _relativeRe = regexp.MustCompile(`^(\d+)\s*([mhdw])$`)

// _localLayouts are the date layouts accepted besides RFC 3339. They are
// interpreted in the caller's location.
var _localLayouts = [...]string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// parseTime understands RFC 3339, a few date layouts in local time, and
// "ago" durations such as 30m, 24h, 7d, 2w. Empty input yields the zero time.
func parseTime(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return time.Time{}, nil
	}

	if m := _relativeRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("parse relative time %q: %w", s, err)
		}
		return now.Add(-time.Duration(n) * unitDuration(m[2])), nil
	}

	// Lower-casing broke the "T" and "Z" of RFC 3339; restore them.
	s = strings.ToUpper(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range _localLayouts {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf(
		"cannot parse time %q: use RFC 3339, YYYY-MM-DD or a relative value like 24h or 7d", s)
}

// unitDuration returns the length of a relative-time unit: m, h, d or w.
func unitDuration(unit string) time.Duration {
	switch unit {
	case "m":
		return time.Minute
	case "h":
		return time.Hour
	case "d":
		return 24 * time.Hour
	default: // "w"
		return 7 * 24 * time.Hour
	}
}
