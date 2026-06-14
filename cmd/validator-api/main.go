package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
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
