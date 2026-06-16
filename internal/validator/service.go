package validator

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mh/islamic-text-validator/internal/database"
	"github.com/mh/islamic-text-validator/internal/models"
)

const (
	matchTypeExact   = "exact"
	matchTypeSimilar = "similar"
	matchTypeFTS     = "fts"
)

// Service combines database search with string similarity scoring.
type Service struct {
	store *database.Store
}

func NewService(store *database.Store) *Service {
	return &Service{store: store}
}

// MatchQuranByRef finds the best Quran edition for text at a fixed chapter:verse.
// When verseText is empty, the edition is chosen by citationLanguageHint.
func (s *Service) MatchQuranByRef(ctx context.Context, verseText string, chapter, verse int, citationLanguageHint string) (*models.Quran, float64, bool) {
	editions, err := s.store.GetQuranEditionsByRef(ctx, chapter, verse)
	if err != nil || len(editions) == 0 {
		return nil, 0, false
	}

	verseText = strings.TrimSpace(verseText)
	if verseText == "" {
		if q := pickQuranByLanguage(editions, citationLanguageHint); q != nil {
			return q, 1, true
		}
		return &editions[0], 1, true
	}

	candidates, err := s.store.SearchQuranFTSByRef(ctx, verseText, chapter, verse, len(editions)*3)
	if err != nil || len(candidates) == 0 {
		return s.scoreQuranEditions(verseText, editions, citationLanguageHint)
	}

	matches := scoreSearchHits(verseText, candidates, 1)
	if len(matches) == 0 || matches[0].Quran == nil {
		return s.scoreQuranEditions(verseText, editions, citationLanguageHint)
	}
	best := matches[0]
	return best.Quran, best.Score, true
}

// MatchHadithByRef finds the best hadith translation text for a (collection, hadithNumber).
// If verseText is empty, it returns the first stored edition with score=1.
func (s *Service) MatchHadithByRef(ctx context.Context, verseText string, collection string, hadithNumber int) (string, float64, bool) {
	editions, err := s.store.GetHadithEditionsByRef(ctx, collection, hadithNumber)
	if err != nil || len(editions) == 0 {
		return "", 0, false
	}

	verseText = strings.TrimSpace(verseText)
	if verseText == "" {
		if len(editions[0].Translations) > 0 {
			return strings.TrimSpace(editions[0].Translations[0].Text), 1, true
		}
		return "", 1, false
	}

	var bestText string
	var bestScore float64
	for i := range editions {
		q := &editions[i]
		if len(q.Translations) == 0 {
			continue
		}
		cand := strings.TrimSpace(q.Translations[0].Text)
		if cand == "" {
			continue
		}
		score := Similarity(verseText, cand)
		if bestText == "" || score > bestScore {
			bestText = cand
			bestScore = score
		}
	}
	if bestText == "" {
		return "", 0, false
	}
	return bestText, bestScore, true
}

func (s *Service) scoreQuranEditions(verseText string, editions []models.Quran, languageHint string) (*models.Quran, float64, bool) {
	var best *models.Quran
	var bestScore float64
	for i := range editions {
		q := &editions[i]
		score := Similarity(verseText, q.Text)
		if languageHint != "" && strings.HasPrefix(q.Language, languageHint) {
			score += 0.001
		}
		if best == nil || score > bestScore {
			best = q
			bestScore = score
		}
	}
	if best == nil {
		return nil, 0, false
	}
	return best, bestScore, true
}

func pickQuranByLanguage(editions []models.Quran, languageHint string) *models.Quran {
	if languageHint == "" {
		return nil
	}
	for i := range editions {
		if strings.HasPrefix(editions[i].Language, languageHint) {
			return &editions[i]
		}
	}
	return nil
}

var (
	indonesianCitationRegex = regexp.MustCompile(`(?i)\b(qs|q\.s\.|surat)\b`)
	englishCitationRegex    = regexp.MustCompile(`(?i)\b(quran|chapter)\b`)
)

// CitationLanguageHint infers the expected edition language from citation wording.
func CitationLanguageHint(citationInner string) string {
	lower := strings.ToLower(citationInner)
	switch {
	case indonesianCitationRegex.MatchString(lower):
		return "ind"
	case englishCitationRegex.MatchString(lower):
		return "eng"
	default:
		return ""
	}
}

// Validate finds candidate matches for input text and ranks them by similarity.
func (s *Service) Validate(ctx context.Context, req models.ValidateRequest) (models.ValidateResponse, error) {
	query := req.Text
	if query == "" {
		return models.ValidateResponse{}, fmt.Errorf("text is required")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}

	var candidates []models.SearchHit
	var err error
	if req.Source == models.SourceQuran && req.Chapter > 0 && req.Verse > 0 {
		candidates, err = s.store.SearchQuranFTSByRef(ctx, query, req.Chapter, req.Verse, limit*3)
	} else {
		candidates, err = s.store.SearchFTS(ctx, query, req.Source, limit*3)
	}
	if err != nil {
		return models.ValidateResponse{}, err
	}

	matches := scoreSearchHits(query, candidates, limit)
	return models.ValidateResponse{
		Query:   query,
		Matches: matches,
	}, nil
}

func scoreSearchHits(query string, candidates []models.SearchHit, limit int) []models.MatchResult {
	matches := make([]models.MatchResult, 0, limit)
	for _, hit := range candidates {
		text := searchHitText(hit)
		sim := Similarity(query, text)
		matchType := matchTypeFTS
		if IsExactMatch(query, text) {
			matchType = matchTypeExact
		} else if sim >= 0.92 {
			matchType = matchTypeSimilar
		}

		matches = append(matches, models.MatchResult{
			Source:     hit.Source,
			Quran:      hit.Quran,
			Hadith:     hit.Hadith,
			Score:      sim,
			MatchType:  matchType,
			Similarity: sim,
		})
		if len(matches) >= limit {
			break
		}
	}
	return matches
}

func searchHitText(hit models.SearchHit) string {
	switch hit.Source {
	case models.SourceQuran:
		if hit.Quran != nil {
			return hit.Quran.Text
		}
	case models.SourceHadith:
		if hit.Hadith != nil && len(hit.Hadith.Translations) > 0 {
			return hit.Hadith.Translations[0].Text
		}
	}
	return ""
}
