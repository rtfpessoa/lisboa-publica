package app

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Search spelling is separate from provider identity and stored display names.
func nameSearch(text, query string) bool {
	return strings.Contains(foldName(text), foldName(query))
}

func foldName(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFD.String(text))
}
