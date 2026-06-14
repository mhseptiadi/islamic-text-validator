package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mh/islamic-text-validator/internal/models"
)

// Store persists normalized Quran and Hadith records.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// InsertQuran adds a single Quran verse.
func (s *Store) InsertQuran(ctx context.Context, entry models.Quran) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO quran (chapter, verse, text, normalized, language, source)
		VALUES (?, ?, ?, ?, ?, ?)
	`, entry.Chapter, entry.Verse, entry.Text, entry.Normalized, entry.Language, entry.Source)
	if err != nil {
		return fmt.Errorf("insert quran: %w", err)
	}
	return nil
}

// InsertHadith adds a single hadith translation row.
func (s *Store) InsertHadith(ctx context.Context, entry models.Hadith) error {
	if len(entry.Translations) == 0 {
		return fmt.Errorf("insert hadith: missing translation")
	}
	t := entry.Translations[0]
	grades, err := json.Marshal(t.Grades)
	if err != nil {
		return fmt.Errorf("marshal grades: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO hadith (collection, hadith_number, book, hadith_in_book, text, normalized, language, grades)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.Collection, entry.HadithNumber, entry.Reference.Book, entry.Reference.Hadith,
		t.Text, entry.Normalized, t.Language, string(grades))
	if err != nil {
		return fmt.Errorf("insert hadith: %w", err)
	}
	return nil
}

type rankedHit struct {
	source models.SourceKind
	id     int64
	rank   float64
}

// SearchFTS performs BM25-ranked full-text search over Quran and/or Hadith text.
func (s *Store) SearchFTS(ctx context.Context, query string, source models.SourceKind, limit int) ([]models.SearchHit, error) {
	if limit <= 0 {
		limit = 10
	}

	terms := strings.Fields(strings.TrimSpace(query))
	if len(terms) == 0 {
		return nil, nil
	}

	ftsQuery := strings.Join(terms, " ")
	var hits []rankedHit

	fmt.Println("ftsQuery", ftsQuery)

	if source == "" || source == models.SourceQuran {
		quranHits, err := s.searchQuranFTS(ctx, ftsQuery, limit)
		if err != nil {
			return nil, err
		}
		hits = append(hits, quranHits...)
	}
	if source == "" || source == models.SourceHadith {
		hadithHits, err := s.searchHadithFTS(ctx, ftsQuery, limit)
		if err != nil {
			return nil, err
		}
		hits = append(hits, hadithHits...)
	}

	fmt.Println("hits", hits)

	sort.Slice(hits, func(i, j int) bool { return hits[i].rank < hits[j].rank })
	if len(hits) > limit {
		hits = hits[:limit]
	}

	results := make([]models.SearchHit, 0, len(hits))
	for _, hit := range hits {
		switch hit.source {
		case models.SourceQuran:
			quran, err := s.getQuranByID(ctx, hit.id)
			if err != nil {
				return nil, err
			}
			results = append(results, models.SearchHit{Source: models.SourceQuran, Quran: quran})
		case models.SourceHadith:
			hadith, err := s.getHadithByID(ctx, hit.id)
			if err != nil {
				return nil, err
			}
			results = append(results, models.SearchHit{Source: models.SourceHadith, Hadith: hadith})
		}
	}

	return results, nil
}

func (s *Store) searchQuranFTS(ctx context.Context, ftsQuery string, limit int) ([]rankedHit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.id, bm25(quran_fts) AS rank
		FROM quran_fts
		JOIN quran q ON q.id = quran_fts.rowid
		WHERE quran_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("quran fts search: %w", err)
	}
	defer rows.Close()

	return scanRankedHits(rows, models.SourceQuran)
}

func (s *Store) searchHadithFTS(ctx context.Context, ftsQuery string, limit int) ([]rankedHit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT h.id, bm25(hadith_fts) AS rank
		FROM hadith_fts
		JOIN hadith h ON h.id = hadith_fts.rowid
		WHERE hadith_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("hadith fts search: %w", err)
	}
	defer rows.Close()

	return scanRankedHits(rows, models.SourceHadith)
}

func scanRankedHits(rows *sql.Rows, source models.SourceKind) ([]rankedHit, error) {
	var hits []rankedHit
	for rows.Next() {
		var hit rankedHit
		hit.source = source
		if err := rows.Scan(&hit.id, &hit.rank); err != nil {
			return nil, fmt.Errorf("scan fts row: %w", err)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fts rows: %w", err)
	}
	return hits, nil
}

func (s *Store) getQuranByID(ctx context.Context, id int64) (*models.Quran, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, chapter, verse, text, normalized, language, source
		FROM quran WHERE id = ?
	`, id)

	var q models.Quran
	if err := row.Scan(&q.ID, &q.Chapter, &q.Verse, &q.Text, &q.Normalized, &q.Language, &q.Source); err != nil {
		return nil, fmt.Errorf("get quran by id: %w", err)
	}
	return &q, nil
}

func (s *Store) getHadithByID(ctx context.Context, id int64) (*models.Hadith, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, collection, hadith_number, book, hadith_in_book, text, normalized, language, grades
		FROM hadith WHERE id = ?
	`, id)

	var h models.Hadith
	var book, hadithInBook int
	var text, language, gradesJSON string
	if err := row.Scan(&h.ID, &h.Collection, &h.HadithNumber, &book, &hadithInBook,
		&text, &h.Normalized, &language, &gradesJSON); err != nil {
		return nil, fmt.Errorf("get hadith by id: %w", err)
	}

	h.Reference = models.HadithReference{Book: book, Hadith: hadithInBook}
	var grades []models.HadithGrade
	if err := json.Unmarshal([]byte(gradesJSON), &grades); err != nil {
		return nil, fmt.Errorf("unmarshal grades: %w", err)
	}
	h.Translations = []models.HadithTranslation{{
		Text:     text,
		Language: language,
		Grades:   grades,
	}}
	return &h, nil
}
