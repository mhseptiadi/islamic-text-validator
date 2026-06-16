package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mh/islamic-text-validator/internal/database"
	"github.com/mh/islamic-text-validator/internal/models"
	"github.com/mh/islamic-text-validator/internal/validator"
)

func main() {
	dbPath := envOrDefault("DB_PATH", "data/generated/data.db")
	addr := envOrDefault("ADDR", ":8080")

	db, err := database.Open(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	svc := validator.NewService(database.NewStore(db))
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /validate", func(w http.ResponseWriter, r *http.Request) {
		var req models.ValidateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		resp, err := svc.Validate(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("POST /replace-tagged", replaceTaggedHandler(svc))

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("validator-api listening on %s (db=%s)", addr, dbPath)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type replaceTaggedRequest struct {
	Text  string `json:"text"`
	Limit int    `json:"limit,omitempty"`
}

type replaceTaggedReplacement struct {
	Tag      string  `json:"tag"`
	Original string  `json:"original"`
	Matched  string  `json:"matched,omitempty"`
	Score    float64 `json:"score,omitempty"`
}

type replaceTaggedResponse struct {
	Text         string                   `json:"text"`
	ReplacedText string                   `json:"replaced_text"`
	Replacements []replaceTaggedReplacement `json:"replacements"`
}

// Go's regexp does not support backreferences, so we capture both tags
// and verify they match in `parseTaggedBlock`.
var taggedBlockRegex = regexp.MustCompile(`(?is)<(quran|hadith)>(.*?)</(quran|hadith)>`)

func replaceTaggedHandler(svc *validator.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := decodeReplaceTaggedRequest(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		replaced, repls := replaceTaggedText(r, svc, req.Text)
		writeJSON(w, http.StatusOK, replaceTaggedResponse{
			Text:         req.Text,
			ReplacedText: replaced,
			Replacements: repls,
		})
	}
}

func decodeReplaceTaggedRequest(r *http.Request) (replaceTaggedRequest, error) {
	var req replaceTaggedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return replaceTaggedRequest{}, errInvalidJSON
	}
	if strings.TrimSpace(req.Text) == "" {
		return replaceTaggedRequest{}, errTextRequired
	}
	if req.Limit <= 0 {
		req.Limit = 5
	}
	return req, nil
}

var (
	errInvalidJSON  = &apiError{msg: "invalid json body"}
	errTextRequired = &apiError{msg: "text is required"}
)

type apiError struct{ msg string }

func (e *apiError) Error() string { return e.msg }

func replaceTaggedText(r *http.Request, svc *validator.Service, input string) (string, []replaceTaggedReplacement) {
	repls := make([]replaceTaggedReplacement, 0, 4)

	out := taggedBlockRegex.ReplaceAllStringFunc(input, func(m string) string {
		tag, content, ok := parseTaggedBlock(m)
		if !ok {
			return m
		}
		if content == "" {
			repls = append(repls, replaceTaggedReplacement{Tag: tag, Original: ""})
			return m
		}

		src, ok := sourceForTag(tag)
		if !ok {
			return m
		}

		matchedText, score, ok := bestMatchForContent(r, svc, src, content)
		if !ok {
			repls = append(repls, replaceTaggedReplacement{Tag: tag, Original: content})
			return m
		}

		if score < 0.5 {
			repls = append(repls, replaceTaggedReplacement{
				Tag:      tag,
				Original: content,
				Matched:  "",
				Score:    score,
			})
			return "<" + tag + "></" + tag + ">"
		}

		repls = append(repls, replaceTaggedReplacement{
			Tag:      tag,
			Original: content,
			Matched:  matchedText,
			Score:    score,
		})
		return "<" + tag + ">" + matchedText + "</" + tag + ">"
	})

	return out, repls
}

func parseTaggedBlock(m string) (tag string, content string, ok bool) {
	idx := taggedBlockRegex.FindStringSubmatchIndex(m)
	// submatch indices: whole, openTag, content, closeTag
	if len(idx) < 8 {
		return "", "", false
	}
	openTag := strings.ToLower(m[idx[2]:idx[3]])
	content = strings.TrimSpace(m[idx[4]:idx[5]])
	closeTag := strings.ToLower(m[idx[6]:idx[7]])
	if openTag != closeTag {
		return "", "", false
	}
	tag = openTag
	return tag, content, true
}

func sourceForTag(tag string) (models.SourceKind, bool) {
	switch tag {
	case "quran":
		return models.SourceQuran, true
	case "hadith":
		return models.SourceHadith, true
	default:
		return "", false
	}
}

func bestMatchForContent(r *http.Request, svc *validator.Service, src models.SourceKind, content string) (matchedText string, score float64, ok bool) {
	vresp, err := svc.Validate(r.Context(), models.ValidateRequest{
		Text:   content,
		Source: src,
		Limit:  1,
	})
	if err != nil || len(vresp.Matches) == 0 {
		return "", 0, false
	}
	best := vresp.Matches[0]
	score = best.Score
	matchedText = strings.TrimSpace(bestMatchText(best))
	if matchedText == "" {
		return "", score, false
	}
	return matchedText, score, true
}

func bestMatchText(m models.MatchResult) string {
	switch m.Source {
	case models.SourceQuran:
		if m.Quran != nil {
			return m.Quran.Text
		}
	case models.SourceHadith:
		if m.Hadith != nil && len(m.Hadith.Translations) > 0 {
			return m.Hadith.Translations[0].Text
		}
	}
	return ""
}
