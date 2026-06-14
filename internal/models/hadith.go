package models

// HadithReference locates a hadith within its collection.
type HadithReference struct {
	Book   int `json:"book"`
	Hadith int `json:"hadith"`
}

// HadithGrade is an authenticity grading from a named scholar.
type HadithGrade struct {
	Grade  string `json:"grade"`
	Grader string `json:"grader"`
}

// HadithTranslation is one language rendering of a hadith.
type HadithTranslation struct {
	Text     string        `json:"text"`
	Language string        `json:"language"`
	Grades   []HadithGrade `json:"grades"`
}

// Hadith is a canonical hadith record with optional multi-language translations.
type Hadith struct {
	ID           int64               `json:"id,omitempty"`
	Collection   string              `json:"collection"`
	HadithNumber int                 `json:"hadith_number"`
	Reference    HadithReference     `json:"reference"`
	Translations []HadithTranslation `json:"translations"`
	Normalized   string              `json:"-"`
}
