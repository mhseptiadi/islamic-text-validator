package models

// SourceKind identifies the origin corpus of a text entry.
type SourceKind string

const (
	SourceQuran  SourceKind = "quran"
	SourceHadith SourceKind = "hadith"
)

// SearchHit is a typed full-text search result from the database.
type SearchHit struct {
	Source SourceKind `json:"source"`
	Quran  *Quran     `json:"quran,omitempty"`
	Hadith *Hadith    `json:"hadith,omitempty"`
}

// MatchResult is returned by the validator after search and scoring.
type MatchResult struct {
	Source     SourceKind `json:"source"`
	Quran      *Quran     `json:"quran,omitempty"`
	Hadith     *Hadith    `json:"hadith,omitempty"`
	Score      float64    `json:"score"`
	MatchType  string     `json:"match_type"`
	Similarity float64    `json:"similarity"`
}

// ValidateRequest is the API payload for text validation.
type ValidateRequest struct {
	Text   string     `json:"text"`
	Source SourceKind `json:"source,omitempty"`
	Limit  int        `json:"limit,omitempty"`
}

// ValidateResponse wraps ranked matches for a validation query.
type ValidateResponse struct {
	Query   string        `json:"query"`
	Matches []MatchResult `json:"matches"`
}
