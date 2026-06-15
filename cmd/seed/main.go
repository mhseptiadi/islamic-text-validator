package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mh/islamic-text-validator/internal/database"
	"github.com/mh/islamic-text-validator/internal/models"
	"github.com/mh/islamic-text-validator/internal/validator"
)

func main() {
	rawDir := flag.String("raw", "data/raw", "directory containing downloaded JSON files")
	dbPath := flag.String("out", "data/generated/data.db", "output SQLite database path")
	flag.Parse()

	db, err := database.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := database.NewStore(db)
	ctx := context.Background()

	quranRows, hadithRows, err := loadRawEntries(*rawDir)
	if err != nil {
		log.Fatalf("load raw entries: %v", err)
	}

	for _, entry := range quranRows {
		if err := store.InsertQuran(ctx, entry); err != nil {
			log.Fatalf("insert quran %d:%d: %v", entry.Chapter, entry.Verse, err)
		}
	}
	for _, entry := range hadithRows {
		if err := store.InsertHadith(ctx, entry); err != nil {
			log.Fatalf("insert hadith %s:%d: %v", entry.Collection, entry.HadithNumber, err)
		}
	}

	if err := store.RebuildFTS(ctx); err != nil {
		log.Fatalf("rebuild fts index: %v", err)
	}

	log.Printf("seeded %d quran and %d hadith entries into %s", len(quranRows), len(hadithRows), *dbPath)
}

type rawLoadResult struct {
	quran  []models.Quran
	hadith []models.Hadith
}

func loadRawEntries(rawDir string) ([]models.Quran, []models.Hadith, error) {
	info, err := os.Stat(rawDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("raw directory %q does not exist; download JSON files first", rawDir)
		}
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%q is not a directory", rawDir)
	}

	var result rawLoadResult
	err = filepath.WalkDir(rawDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		parsed, err := parseJSONFile(path, data)
		if err != nil {
			return err
		}
		result.quran = append(result.quran, parsed.quran...)
		result.hadith = append(result.hadith, parsed.hadith...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return result.quran, result.hadith, nil
}

func parseJSONFile(path string, data []byte) (rawLoadResult, error) {
	var probe struct {
		Quran   json.RawMessage `json:"quran"`
		Hadiths json.RawMessage `json:"hadiths"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return rawLoadResult{}, fmt.Errorf("unmarshal json %s: %w", path, err)
	}

	language, source := models.EditionFromFilename(path)

	switch {
	case probe.Quran != nil:
		var raw models.RawQuranFile
		if err := json.Unmarshal(data, &raw); err != nil {
			return rawLoadResult{}, fmt.Errorf("unmarshal quran json %s: %w", path, err)
		}
		return rawLoadResult{quran: quranEntries(raw, language, source)}, nil
	case probe.Hadiths != nil:
		var raw models.RawHadithFile
		if err := json.Unmarshal(data, &raw); err != nil {
			return rawLoadResult{}, fmt.Errorf("unmarshal hadith json %s: %w", path, err)
		}
		return rawLoadResult{hadith: hadithEntries(raw, language)}, nil
	default:
		return rawLoadResult{}, fmt.Errorf("unsupported json file %s (expected quran or hadiths root key)", path)
	}
}

func quranEntries(raw models.RawQuranFile, language, source string) []models.Quran {
	entries := make([]models.Quran, 0, len(raw.Quran))
	for _, verse := range raw.Quran {
		entries = append(entries, models.Quran{
			Chapter:    verse.Chapter,
			Verse:      verse.Verse,
			Text:       verse.Text,
			Language:   language,
			Source:     source,
			Normalized: validator.Normalize(verse.Text),
		})
	}
	return entries
}

func hadithEntries(raw models.RawHadithFile, language string) []models.Hadith {
	entries := make([]models.Hadith, 0, len(raw.Hadiths))
	for _, item := range raw.Hadiths {
		entries = append(entries, models.Hadith{
			Collection:   raw.Metadata.Name,
			HadithNumber: item.HadithNumber,
			Reference: models.HadithReference{
				Book:   item.Reference.Book,
				Hadith: item.Reference.Hadith,
			},
			Translations: []models.HadithTranslation{{
				Text:     item.Text,
				Language: language,
				Grades:   rawGradesToDomain(item.Grades),
			}},
			Normalized: validator.Normalize(item.Text),
		})
	}
	return entries
}

func rawGradesToDomain(raw []models.RawHadithGrade) []models.HadithGrade {
	if len(raw) == 0 {
		return nil
	}
	grades := make([]models.HadithGrade, len(raw))
	for i, g := range raw {
		grades[i] = models.HadithGrade{Grade: g.Grade, Grader: g.Grader}
	}
	return grades
}
