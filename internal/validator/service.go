package validator

import (
	"context"
	"fmt"

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

	fmt.Println("query", query)

	candidates, err := s.store.SearchFTS(ctx, query, req.Source, limit*3)
	if err != nil {
		return models.ValidateResponse{}, err
	}

	fmt.Println("candidates", candidates)

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

	return models.ValidateResponse{
		Query:   query,
		Matches: matches,
	}, nil
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
