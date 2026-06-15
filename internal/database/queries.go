package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mh/islamic-text-validator/internal/models"
)

// Store persists normalized Quran and Hadith records.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// InsertQuran adds or updates a single Quran verse keyed by chapter, verse, language, and source.
func (s *Store) InsertQuran(ctx context.Context, entry models.Quran) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO quran (chapter, verse, text, normalized, language, source)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chapter, verse, language, source) DO UPDATE SET
			text = excluded.text,
			normalized = excluded.normalized
	`, entry.Chapter, entry.Verse, entry.Text, entry.Normalized, entry.Language, entry.Source)
	if err != nil {
		return fmt.Errorf("insert quran: %w", err)
	}
	return nil
}

// InsertHadith adds or updates a single hadith translation row keyed by collection, hadith number, and language.
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
		ON CONFLICT(collection, hadith_number, language) DO UPDATE SET
			book = excluded.book,
			hadith_in_book = excluded.hadith_in_book,
			text = excluded.text,
			normalized = excluded.normalized,
			grades = excluded.grades
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

func (s *Store) RebuildFTS(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO quran_fts(quran_fts) VALUES('rebuild')`); err != nil {
		return fmt.Errorf("rebuild quran fts: %w", err)
	}
	err := s.debugCountTable(ctx, "quran")
	if err != nil {
		return fmt.Errorf("debug quran: %w", err)
	}
	err = s.debugCountTable(ctx, "quran_fts")
	if err != nil {
		return fmt.Errorf("debug quran fts: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO hadith_fts(hadith_fts) VALUES('rebuild')`); err != nil {
		return fmt.Errorf("rebuild hadith fts: %w", err)
	}
	// err = s.debugQuranFTS(ctx, "hadith_fts")
	// if err != nil {
	// 	return fmt.Errorf("debug hadith fts: %w", err)
	// }
	return nil
}

// SearchFTS performs BM25-ranked full-text search over Quran and/or Hadith text.
func (s *Store) SearchFTS(ctx context.Context, query string, source models.SourceKind, limit int) ([]models.SearchHit, error) {
	if limit <= 0 {
		limit = 10
	}

	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	fmt.Println("ftsQuery", ftsQuery)

	var hits []rankedHit

	// TODO: join search and get by id into a single query

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

func StripPunctuation(text string) string {
	reg := regexp.MustCompile(`[^\p{L}\p{N}\s]+`)
	cleanText := reg.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(cleanText), " ")
}

// buildFTSQuery turns free-form user text into a safe FTS5 MATCH expression.
// Punctuation is stripped and each term is quoted so characters like "," are not
// parsed as FTS5 operators (e.g. NEAR syntax).
func buildFTSQuery(query string) string {
	var b strings.Builder
	b.Grow(len(query))
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}

	terms := strings.Fields(b.String())
	if len(terms) == 0 {
		return ""
	}

	quoted := make([]string, len(terms))
	for i, term := range terms {
		term = strings.ReplaceAll(term, `"`, `""`)
		quoted[i] = `"` + term + `"`
	}
	return strings.Join(quoted, " ")
}

func (s *Store) debugCountTable(ctx context.Context, table string) error {
	switch table {
	case "quran_fts", "hadith_fts", "quran", "hadith":
	default:
		return fmt.Errorf("debug count: unknown table %q", table)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT count(*) FROM %s", table))
	if err != nil {
		return fmt.Errorf("debug %s fts: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var count int
		err := rows.Scan(&count)
		if err != nil {
			return fmt.Errorf("scan %s fts count: %w", table, err)
		}
		fmt.Println("count", table, count)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s fts rows: %w", table, err)
	}
	return nil
}

func (s *Store) searchQuranFTS(ctx context.Context, ftsQuery string, limit int) ([]rankedHit, error) {

	err := s.debugCountTable(ctx, "quran_fts")
	if err != nil {
		return nil, fmt.Errorf("debug quran fts: %w", err)
	}

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
