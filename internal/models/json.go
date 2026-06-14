package models

import (
	"path/filepath"
	"strings"
)

// EditionFromFilename extracts language and source from names like "eng-ummmuhammad.json".
func EditionFromFilename(path string) (language, source string) {
	base := strings.TrimSuffix(filepath.Base(path), ".json")
	language, source, ok := strings.Cut(strings.ToLower(base), "-")
	if !ok {
		return "", base
	}
	return language, source
}

// RawQuranFile mirrors the shape of downloaded Quran JSON from fawazahmed0/quran-api.
type RawQuranFile struct {
	Quran []RawQuranVerse `json:"quran"`
}

// RawQuranVerse is a single verse in the raw Quran JSON export.
type RawQuranVerse struct {
	Chapter int    `json:"chapter"`
	Verse   int    `json:"verse"`
	Text    string `json:"text"`
}

// RawHadithFile mirrors the shape of downloaded Hadith JSON from fawazahmed0/hadith-api.
type RawHadithFile struct {
	Metadata RawHadithMetadata `json:"metadata"`
	Hadiths  []RawHadithItem   `json:"hadiths"`
}

// RawHadithMetadata holds collection-level metadata from the raw Hadith export.
type RawHadithMetadata struct {
	Name     string            `json:"name"`
	Sections map[string]string `json:"sections"`
}

// RawHadithItem is a single hadith in the raw JSON export.
type RawHadithItem struct {
	HadithNumber int                `json:"hadithnumber"`
	ArabicNumber int                `json:"arabicnumber"`
	Text         string             `json:"text"`
	Grades       []RawHadithGrade   `json:"grades"`
	Reference    RawHadithReference `json:"reference"`
}

// RawHadithReference is the book/hadith locator in the raw JSON export.
type RawHadithReference struct {
	Book   int `json:"book"`
	Hadith int `json:"hadith"`
}

// RawHadithGrade is a grading entry in the raw JSON export.
type RawHadithGrade struct {
	Grade  string `json:"grade"`
	Grader string `json:"name"`
}
