package validator

import (
	"strings"
)

const (
	hadithCollectionBukhari = "Sahih al Bukhari"
	hadithCollectionMuslim  = "Sahih Muslim"
)

type hadithCollectionMatcher struct {
	canonical string
	tokens    []string
}

var hadithCollectionMatchers = []hadithCollectionMatcher{
	{canonical: hadithCollectionBukhari, tokens: []string{"bukhari", "bukhori"}},
	{canonical: hadithCollectionMuslim, tokens: []string{"muslim"}},
}

// ResolveHadithCollection maps user-facing collection names to the canonical
// names stored in the database (e.g. "bukhari" -> "Sahih al Bukhari").
// Matching is case-insensitive and ignores common prefixes like "Sahih" and "al".
func ResolveHadithCollection(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}

	for _, m := range hadithCollectionMatchers {
		if strings.EqualFold(input, m.canonical) {
			return m.canonical, true
		}
	}

	norm := normalizeHadithCollectionKey(input)
	if norm == "" {
		return "", false
	}

	for _, m := range hadithCollectionMatchers {
		for _, token := range m.tokens {
			if norm == token || strings.Contains(norm, token) {
				return m.canonical, true
			}
		}
	}

	return "", false
}

func normalizeHadithCollectionKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("-", " ", "_", " ", ".", " ", "'", "", "\"", "").Replace(s)
	for _, word := range []string{"sahih", "sahihul", "al", "the"} {
		s = strings.ReplaceAll(s, word, " ")
	}
	return strings.Join(strings.Fields(s), "")
}
