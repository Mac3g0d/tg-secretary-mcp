package store

import "strings"

// ftsTerms turns free-form user input into a safe FTS5 expression: every word
// becomes a quoted prefix term and all words must match.
func ftsTerms(input string) string {
	words := strings.Fields(input)
	terms := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ReplaceAll(w, `"`, "")
		if w == "" {
			continue
		}
		terms = append(terms, `"`+w+`"*`)
	}
	return strings.Join(terms, " ")
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
