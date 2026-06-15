package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS quran (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chapter INTEGER NOT NULL,
	verse INTEGER NOT NULL,
	text TEXT NOT NULL,
	normalized TEXT NOT NULL,
	language TEXT NOT NULL,
	source TEXT NOT NULL
);

-- External content: text lives in quran; triggers maintain the FTS index.
-- SELECT count(*) FROM quran_fts reads quran, not index size — use MATCH or
-- RebuildFTS() after bulk loads to verify or refresh the index.
CREATE VIRTUAL TABLE IF NOT EXISTS quran_fts USING fts5(
	text,
	normalized,
	chapter,
	verse,
	language,
	source,
	content='quran',
	content_rowid='id',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS quran_ai AFTER INSERT ON quran BEGIN
	INSERT INTO quran_fts(rowid, text, normalized, chapter, verse, language, source)
	VALUES (new.id, new.text, new.normalized, new.chapter, new.verse, new.language, new.source);
END;

CREATE TRIGGER IF NOT EXISTS quran_ad AFTER DELETE ON quran BEGIN
	INSERT INTO quran_fts(quran_fts, rowid, text, normalized, chapter, verse, language, source)
	VALUES ('delete', old.id, old.text, old.normalized, old.chapter, old.verse, old.language, old.source);
END;

CREATE TRIGGER IF NOT EXISTS quran_au AFTER UPDATE ON quran BEGIN
	INSERT INTO quran_fts(quran_fts, rowid, text, normalized, chapter, verse, language, source)
	VALUES ('delete', old.id, old.text, old.normalized, old.chapter, old.verse, old.language, old.source);
	INSERT INTO quran_fts(rowid, text, normalized, chapter, verse, language, source)
	VALUES (new.id, new.text, new.normalized, new.chapter, new.verse, new.language, new.source);
END;

CREATE TABLE IF NOT EXISTS hadith (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	collection TEXT NOT NULL,
	hadith_number INTEGER NOT NULL,
	book INTEGER NOT NULL,
	hadith_in_book INTEGER NOT NULL,
	text TEXT NOT NULL,
	normalized TEXT NOT NULL,
	language TEXT NOT NULL,
	grades TEXT NOT NULL DEFAULT '[]'
);

CREATE VIRTUAL TABLE IF NOT EXISTS hadith_fts USING fts5(
	text,
	normalized,
	collection,
	hadith_number,
	language,
	content='hadith',
	content_rowid='id',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS hadith_ai AFTER INSERT ON hadith BEGIN
	INSERT INTO hadith_fts(rowid, text, normalized, collection, hadith_number, language)
	VALUES (new.id, new.text, new.normalized, new.collection, new.hadith_number, new.language);
END;

CREATE TRIGGER IF NOT EXISTS hadith_ad AFTER DELETE ON hadith BEGIN
	INSERT INTO hadith_fts(hadith_fts, rowid, text, normalized, collection, hadith_number, language)
	VALUES ('delete', old.id, old.text, old.normalized, old.collection, old.hadith_number, old.language);
END;

CREATE TRIGGER IF NOT EXISTS hadith_au AFTER UPDATE ON hadith BEGIN
	INSERT INTO hadith_fts(hadith_fts, rowid, text, normalized, collection, hadith_number, language)
	VALUES ('delete', old.id, old.text, old.normalized, old.collection, old.hadith_number, old.language);
	INSERT INTO hadith_fts(rowid, text, normalized, collection, hadith_number, language)
	VALUES (new.id, new.text, new.normalized, new.collection, new.hadith_number, new.language);
END;
`

const editionUniqueIndexes = `
DROP INDEX IF EXISTS idx_quran_verse;
CREATE UNIQUE INDEX IF NOT EXISTS idx_quran_edition ON quran(chapter, verse, language, source);
CREATE UNIQUE INDEX IF NOT EXISTS idx_hadith_edition ON hadith(collection, hadith_number, language);
`

func dedupeEditions(db *sql.DB) error {
	stmts := []string{
		`DELETE FROM quran WHERE id NOT IN (
			SELECT MIN(id) FROM quran GROUP BY chapter, verse, language, source
		)`,
		`DELETE FROM hadith WHERE id NOT IN (
			SELECT MIN(id) FROM hadith GROUP BY collection, hadith_number, language
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("dedupe editions: %w", err)
		}
	}
	return nil
}

// Open connects to SQLite at dbPath, creating parent directories when needed.
func Open(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := dedupeEditions(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(editionUniqueIndexes); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply edition indexes: %w", err)
	}

	return db, nil
}
