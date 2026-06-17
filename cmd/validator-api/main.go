package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mh/islamic-text-validator/internal/database"
	"github.com/mh/islamic-text-validator/internal/models"
	"github.com/mh/islamic-text-validator/internal/validator"
)

func main() {
	dbPath := envOrDefault("DB_PATH", "data/generated/data.db")
	addr := envOrDefault("ADDR", ":8080")

	db, err := database.OpenReadOnly(dbPath)
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
	Chapter  int     `json:"chapter,omitempty"`
	Verse    int     `json:"verse,omitempty"`
}

type replaceTaggedResponse struct {
	Text         string                     `json:"text"`
	ReplacedText string                     `json:"replaced_text"`
	Replacements []replaceTaggedReplacement `json:"replacements"`
}

// Tag parser for the standardized formats:
// - <quran chapter="..." verse="...">...</quran>
// - <hadith collection="..." number="...">...</hadith>
var taggedBlockRegex = regexp.MustCompile(`(?is)<(quran|hadith)\b([^>]*)>(.*?)</(quran|hadith)>`)

var (
	quranChapterAttrRegex = regexp.MustCompile(`(?i)\bchapter\s*=\s*"(\d+)"`)
	quranVerseAttrRegex   = regexp.MustCompile(`(?i)\bverse\s*=\s*"(\d+)"`)

	hadithCollectionAttrRegex = regexp.MustCompile(`(?i)\bcollection\s*=\s*"([^"]*)"`)
	hadithNumberAttrRegex     = regexp.MustCompile(`(?i)\bnumber\s*=\s*"(\d+)"`)
)

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
		tag, attrs, content, ok := parseTaggedBlock(m)
		if !ok {
			return m
		}
		switch tag {
		case "quran":
			chapter, verse, _ := parseQuranChapterVerse(attrs) // optional
			newInner, rep := replaceQuranBlock(r, svc, chapter, verse, content)
			repls = append(repls, rep)
			if rep.Chapter > 0 && rep.Verse > 0 {
				return "<quran chapter=\"" + strconv.Itoa(rep.Chapter) + "\" verse=\"" + strconv.Itoa(rep.Verse) + "\">" + newInner + "</quran>"
			}
			return "<quran>" + newInner + "</quran>"
		case "hadith":
			collection, hadithNumber, _ := parseHadithCollectionNumber(attrs) // optional
			newInner, rep := replaceHadithBlock(r, svc, collection, hadithNumber, content)
			repls = append(repls, rep)
			if collection != "" && hadithNumber > 0 {
				return "<hadith collection=\"" + sanitizeAttrValue(collection) + "\" number=\"" + strconv.Itoa(hadithNumber) + "\">" + newInner + "</hadith>"
			}
			return "<hadith>" + newInner + "</hadith>"
		default:
			return m
		}
	})

	return out, repls
}

func parseTaggedBlock(m string) (tag string, attrs string, content string, ok bool) {
	idx := taggedBlockRegex.FindStringSubmatchIndex(m)
	// submatch indices: whole, openTag, content, closeTag
	// submatch indices: whole, openTagName, attrs, content, closeTagName
	if len(idx) < 10 {
		return "", "", "", false
	}
	openTag := strings.ToLower(m[idx[2]:idx[3]])
	attrs = m[idx[4]:idx[5]]
	content = strings.TrimSpace(m[idx[6]:idx[7]])
	closeTag := strings.ToLower(m[idx[8]:idx[9]])
	if openTag != closeTag {
		return "", "", "", false
	}
	tag = openTag
	return tag, strings.TrimSpace(attrs), content, true
}

func parseQuranChapterVerse(attrs string) (chapter, verse int, ok bool) {
	ch := quranChapterAttrRegex.FindStringSubmatch(attrs)
	vs := quranVerseAttrRegex.FindStringSubmatch(attrs)
	if len(ch) != 2 || len(vs) != 2 {
		return 0, 0, false
	}
	c, err1 := strconv.Atoi(ch[1])
	v, err2 := strconv.Atoi(vs[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return c, v, true
}

func parseHadithCollectionNumber(attrs string) (collection string, hadithNumber int, ok bool) {
	coll := hadithCollectionAttrRegex.FindStringSubmatch(attrs)
	num := hadithNumberAttrRegex.FindStringSubmatch(attrs)
	if len(coll) != 2 || len(num) != 2 {
		return "", 0, false
	}
	n, err := strconv.Atoi(num[1])
	if err != nil {
		return "", 0, false
	}
	return coll[1], n, true
}

func sanitizeAttrValue(v string) string {
	// Keep output tag syntactically valid (no unescaped quotes).
	return strings.ReplaceAll(v, `"`, `'`)
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

func replaceHadithBlock(r *http.Request, svc *validator.Service, collection string, hadithNumber int, content string) (string, replaceTaggedReplacement) {
	original := strings.TrimSpace(content)

	// Prefer a constrained match only when both fields are provided.
	// When ref exists in DB, always return the best edition (ignore score threshold).
	if collection != "" && hadithNumber > 0 {
		matchedText, score, ok := svc.MatchHadithByRef(r.Context(), original, collection, hadithNumber)
		if ok {
			return matchedText, replaceTaggedReplacement{
				Tag:      "hadith",
				Original: original,
				Matched:  matchedText,
				Score:    score,
			}
		}
	}

	// Fallback: best match by text only.
	fallbackText, fallbackScore, _, _, ok2 := bestMatchForContent(r, svc, models.SourceHadith, original)
	if !ok2 {
		return original, replaceTaggedReplacement{Tag: "hadith", Original: original}
	}
	if fallbackScore < 0.5 {
		return "", replaceTaggedReplacement{Tag: "hadith", Original: original, Matched: "", Score: fallbackScore}
	}
	return fallbackText, replaceTaggedReplacement{Tag: "hadith", Original: original, Matched: fallbackText, Score: fallbackScore}
}

type quranCitation struct {
	WrapperOpen  string // "(" or "["
	WrapperClose string // ")" or "]"
	Full         string // including wrappers
	Inner        string // without wrappers
	Chapter      int
	Verse        int
	HasNumbers   bool
	HasName      bool
	Name         string
}

// wrapper + anything + chapter:verse + anything, to locate the citation segment.
var quranCitationRegex = regexp.MustCompile(`(?is)(\(|\[)[^)\]]*\d{1,3}\s*:\s*\d{1,3}[^)\]]*(\)|\])`)
var quranNumbersRegex = regexp.MustCompile(`\b(\d{1,3})\s*:\s*(\d{1,3})\b`)

func quranReplacement(original, matched string, score float64, chapter, verse int) replaceTaggedReplacement {
	return replaceTaggedReplacement{
		Tag:      "quran",
		Original: original,
		Matched:  matched,
		Score:    score,
		Chapter:  chapter,
		Verse:    verse,
	}
}

var trailingParenBracketRegex = regexp.MustCompile(`(?is)\s*(\([^)]*\)|\[[^\]]*\])\s*$`)

func splitTrailingParenBracketSegments(s string) (main string, tail string) {
	s = strings.TrimSpace(s)
	for {
		loc := trailingParenBracketRegex.FindStringSubmatchIndex(s)
		if loc == nil {
			break
		}
		seg := strings.TrimSpace(s[loc[2]:loc[3]])
		if tail == "" {
			tail = seg
		} else {
			tail = seg + " " + tail
		}
		s = strings.TrimSpace(s[:loc[0]])
	}
	return strings.TrimSpace(s), strings.TrimSpace(tail)
}

func replaceQuranBlock(r *http.Request, svc *validator.Service, chapter, verse int, content string) (string, replaceTaggedReplacement) {
	original := strings.TrimSpace(content)
	mainText, _ := splitTrailingParenBracketSegments(original)

	// If chapter/verse are provided, prefer reference-constrained matching.
	// When ref exists in DB, always return the best edition (ignore score threshold).
	if chapter > 0 && verse > 0 {
		matched, score, ok := svc.MatchQuranByRef(r.Context(), strings.TrimSpace(mainText), chapter, verse, "")
		if ok {
			newInner := strings.TrimSpace(matched.Text)
			return newInner, quranReplacement(original, newInner, score, chapter, verse)
		}
	}

	// DB doesn't have that chapter:verse -> fallback by text only.
	fallbackText := strings.TrimSpace(mainText)
	if fallbackText == "" {
		fallbackText = original
	}
	matchedText, fallbackScore, bestChapter, bestVerse, ok2 := bestMatchForContent(r, svc, models.SourceQuran, fallbackText)
	if !ok2 {
		return original, quranReplacement(original, original, 0, chapter, verse)
	}
	if fallbackScore < 0.5 {
		return "", quranReplacement(original, "", fallbackScore, bestChapter, bestVerse)
	}
	newInner := strings.TrimSpace(matchedText)
	return newInner, quranReplacement(original, newInner, fallbackScore, bestChapter, bestVerse)
}

func extractChapterVerseFromMatch(matchedText string, svc *validator.Service, r *http.Request, query string) quranCitation {
	// We actually want the matched Quran ref; easiest is to re-validate and read m.Quran.
	vresp, err := svc.Validate(r.Context(), models.ValidateRequest{Text: query, Source: models.SourceQuran, Limit: 1})
	if err != nil || len(vresp.Matches) == 0 || vresp.Matches[0].Quran == nil {
		return quranCitation{}
	}
	q := vresp.Matches[0].Quran
	return quranCitation{Chapter: q.Chapter, Verse: q.Verse, HasNumbers: true}
}

func extractQuranCitation(s string) (quranCitation, bool) {
	matches := quranCitationRegex.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return quranCitation{}, false
	}
	// Take the last citation-like wrapper.
	idx := matches[len(matches)-1]
	full := s[idx[0]:idx[1]]
	open := s[idx[2]:idx[3]]
	close := s[idx[4]:idx[5]]
	inner := strings.TrimSpace(full[len(open) : len(full)-len(close)])

	num := quranNumbersRegex.FindStringSubmatch(inner)
	if len(num) != 3 {
		return quranCitation{WrapperOpen: open, WrapperClose: close, Full: full, Inner: inner}, true
	}
	ch, _ := strconv.Atoi(num[1])
	vs, _ := strconv.Atoi(num[2])

	name := extractSurahName(inner)
	cit := quranCitation{
		WrapperOpen:  open,
		WrapperClose: close,
		Full:         full,
		Inner:        inner,
		Chapter:      ch,
		Verse:        vs,
		HasNumbers:   true,
		HasName:      name != "",
		Name:         name,
	}
	return cit, true
}

func splitAroundCitation(original, citationFull string) (before string, after string) {
	i := strings.LastIndex(strings.ToLower(original), strings.ToLower(citationFull))
	if i < 0 {
		return original, ""
	}
	return original[:i], original[i+len(citationFull):]
}

var surahNameRegex = regexp.MustCompile(`(?i)\b(surah|surat|chapter)\s+([^,\]\)]+)`)

func extractSurahName(inner string) string {
	m := surahNameRegex.FindStringSubmatch(inner)
	if len(m) < 3 {
		return ""
	}
	return strings.TrimSpace(m[2])
}

func fixCitationChapterName(c quranCitation, chapter int) string {
	if !c.HasName || chapter <= 0 || chapter > len(surahNamesEN) {
		return c.Full
	}
	canonical := surahNamesEN[chapter-1]
	if canonical == "" {
		return c.Full
	}
	innerFixed := surahNameRegex.ReplaceAllStringFunc(c.Inner, func(seg string) string {
		sub := surahNameRegex.FindStringSubmatch(seg)
		if len(sub) < 3 {
			return seg
		}
		// keep original keyword (Surah/Surat/Chapter), replace only the name.
		kw := sub[1]
		return kw + " " + canonical
	})
	return c.WrapperOpen + innerFixed + c.WrapperClose
}

func replaceNumbersInCitation(original string, cit quranCitation, chapter, verse int) string {
	if !cit.HasNumbers {
		return original
	}
	newFull := quranNumbersRegex.ReplaceAllString(cit.Full, strconv.Itoa(chapter)+":"+strconv.Itoa(verse))
	return strings.Replace(original, cit.Full, newFull, 1)
}

// Basic canonical English surah names (used only to fix names when a chapter number is present).
var surahNamesEN = []string{
	"Al-Fatihah", "Al-Baqarah", "Aal-e-Imran", "An-Nisa", "Al-Ma'idah", "Al-An'am", "Al-A'raf", "Al-Anfal", "At-Tawbah", "Yunus",
	"Hud", "Yusuf", "Ar-Ra'd", "Ibrahim", "Al-Hijr", "An-Nahl", "Al-Isra", "Al-Kahf", "Maryam", "Ta-Ha",
	"Al-Anbiya", "Al-Hajj", "Al-Mu'minun", "An-Nur", "Al-Furqan", "Ash-Shu'ara", "An-Naml", "Al-Qasas", "Al-Ankabut", "Ar-Rum",
	"Luqman", "As-Sajdah", "Al-Ahzab", "Saba", "Fatir", "Ya-Sin", "As-Saffat", "Sad", "Az-Zumar", "Ghafir",
	"Fussilat", "Ash-Shuraa", "Az-Zukhruf", "Ad-Dukhan", "Al-Jathiyah", "Al-Ahqaf", "Muhammad", "Al-Fath", "Al-Hujurat", "Qaf",
	"Adh-Dhariyat", "At-Tur", "An-Najm", "Al-Qamar", "Ar-Rahman", "Al-Waqi'ah", "Al-Hadid", "Al-Mujadila", "Al-Hashr", "Al-Mumtahanah",
	"As-Saff", "Al-Jumu'ah", "Al-Munafiqun", "At-Taghabun", "At-Talaq", "At-Tahrim", "Al-Mulk", "Al-Qalam", "Al-Haqqah", "Al-Ma'arij",
	"Nuh", "Al-Jinn", "Al-Muzzammil", "Al-Muddaththir", "Al-Qiyamah", "Al-Insan", "Al-Mursalat", "An-Naba", "An-Nazi'at", "Abasa",
	"At-Takwir", "Al-Infitar", "Al-Mutaffifin", "Al-Inshiqaq", "Al-Buruj", "At-Tariq", "Al-A'la", "Al-Ghashiyah", "Al-Fajr", "Al-Balad",
	"Ash-Shams", "Al-Layl", "Ad-Duhaa", "Ash-Sharh", "At-Tin", "Al-Alaq", "Al-Qadr", "Al-Bayyinah", "Az-Zalzalah", "Al-Adiyat",
	"Al-Qari'ah", "At-Takathur", "Al-Asr", "Al-Humazah", "Al-Fil", "Quraysh", "Al-Ma'un", "Al-Kawthar", "Al-Kafirun", "An-Nasr",
	"Al-Masad", "Al-Ikhlas", "Al-Falaq", "An-Nas",
}

func bestMatchForContent(r *http.Request, svc *validator.Service, src models.SourceKind, content string) (matchedText string, score float64, chapter, verse int, ok bool) {
	vresp, err := svc.Validate(r.Context(), models.ValidateRequest{
		Text:   content,
		Source: src,
		Limit:  1,
	})
	if err != nil || len(vresp.Matches) == 0 {
		return "", 0, 0, 0, false
	}
	best := vresp.Matches[0]
	score = best.Score
	matchedText = strings.TrimSpace(bestMatchText(best))
	if matchedText == "" {
		return "", score, 0, 0, false
	}
	if best.Quran != nil {
		chapter = best.Quran.Chapter
		verse = best.Quran.Verse
	}
	return matchedText, score, chapter, verse, true
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
