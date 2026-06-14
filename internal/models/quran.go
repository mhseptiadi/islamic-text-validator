package models

// Quran is a single verse from a specific edition and language.
type Quran struct {
	ID         int64  `json:"id,omitempty"`
	Chapter    int    `json:"chapter"`
	Verse      int    `json:"verse"`
	Text       string `json:"text"`
	Language   string `json:"language"`
	Source     string `json:"source"`
	Normalized string `json:"-"`
}
