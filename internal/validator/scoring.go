package validator

import (
	"strings"
	"unicode"

	"github.com/agnivade/levenshtein"
)

// Normalize prepares text for comparison by lowercasing and stripping diacritics/punctuation.
func Normalize(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Similarity returns a score in [0, 1] using Levenshtein distance on normalized strings.
func Similarity(a, b string) float64 {
	a = Normalize(a)
	b = Normalize(b)
	if a == "" && b == "" {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}

	distance := levenshtein.ComputeDistance(a, b)
	maxLen := max(len(a), len(b))
	return 1 - float64(distance)/float64(maxLen)
}

// IsExactMatch reports whether two strings match after normalization.
func IsExactMatch(a, b string) bool {
	return Normalize(a) == Normalize(b)
}
