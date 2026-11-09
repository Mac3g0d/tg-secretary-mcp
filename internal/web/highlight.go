package web

import (
	"regexp"
	"slices"
	"strings"
)

var _wordRe = regexp.MustCompile(`[\p{L}\p{N}_]+`)

// segment is a piece of message text; Mark is set for search hits.
type segment struct {
	Text string
	Mark bool
}

// highlight splits text into segments, marking words that start with one of
// the lower-cased search terms (the search itself matches word prefixes).
func highlight(text string, terms []string) []segment {
	if text == "" {
		return nil
	}
	if len(terms) == 0 {
		return []segment{{Text: text}}
	}

	var out []segment
	last := 0
	for _, loc := range _wordRe.FindAllStringIndex(text, -1) {
		word := text[loc[0]:loc[1]]
		if !hasPrefixAny(strings.ToLower(word), terms) {
			continue
		}
		if loc[0] > last {
			out = append(out, segment{Text: text[last:loc[0]]})
		}
		out = append(out, segment{Text: word, Mark: true})
		last = loc[1]
	}
	if last < len(text) {
		out = append(out, segment{Text: text[last:]})
	}
	return out
}

// hasPrefixAny reports whether word starts with any of prefixes.
func hasPrefixAny(word string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(p string) bool {
		return strings.HasPrefix(word, p)
	})
}
