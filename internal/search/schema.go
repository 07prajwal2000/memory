package search

import (
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

var (
	ErrNeedsReindex = errors.New("search index requires reindex: embedding model, dimension, or quantization version changed")
)

const (
	CurrentSchemaVersion = 5
)

var defaultStopwords = map[string]bool{
	"a": true, "about": true, "above": true, "after": true, "again": true, "against": true,
	"all": true, "am": true, "an": true, "and": true, "any": true, "are": true, "aren't": true,
	"as": true, "at": true, "be": true, "because": true, "been": true, "before": true,
	"being": true, "below": true, "between": true, "both": true, "but": true, "by": true,
	"can": true, "can't": true, "cannot": true, "could": true, "couldn't": true, "did": true,
	"didn't": true, "do": true, "does": true, "doesn't": true, "doing": true, "don't": true,
	"down": true, "during": true, "each": true, "few": true, "for": true, "from": true,
	"further": true, "had": true, "hadn't": true, "has": true, "hasn't": true, "have": true,
	"haven't": true, "having": true, "he": true, "her": true, "here": true, "hers": true,
	"herself": true, "him": true, "himself": true, "his": true, "how": true, "i": true,
	"if": true, "in": true, "into": true, "is": true, "isn't": true, "it": true, "it's": true,
	"its": true, "itself": true, "let's": true, "me": true, "more": true, "most": true,
	"mustn't": true, "my": true, "myself": true, "no": true, "nor": true, "not": true,
	"of": true, "off": true, "on": true, "once": true, "only": true, "or": true, "other": true,
	"ought": true, "our": true, "ours": true, "ourselves": true, "out": true, "over": true,
	"own": true, "same": true, "shan't": true, "she": true, "should": true, "shouldn't": true,
	"so": true, "some": true, "such": true, "than": true, "that": true, "the": true, "their": true,
	"theirs": true, "them": true, "themselves": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "through": true, "to": true, "too": true,
	"under": true, "until": true, "up": true, "very": true, "was": true, "wasn't": true,
	"we": true, "were": true, "weren't": true, "what": true, "when": true, "where": true,
	"which": true, "while": true, "who": true, "whom": true, "why": true, "with": true,
	"won't": true, "would": true, "wouldn't": true, "you": true, "your": true, "yours": true,
	"yourself": true, "yourselves": true,
}

func ScopeToken(slug string) string {
	h := sha1.Sum([]byte(slug))
	return "c" + hex.EncodeToString(h[:6]) // 12 hex chars prefixed with 'c'
}

func Sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func initSchema(db *sql.DB, rebuild bool) error {
	if rebuild {
		dropTables := []string{
			"DROP TABLE IF EXISTS doc_enrichments;",
			"DROP TABLE IF EXISTS chunks_fts_vocab;",
			"DROP TABLE IF EXISTS chunk_deletions;",
			"DROP TABLE IF EXISTS chunks_fts;",
			"DROP TABLE IF EXISTS chunks_vec_bit;",
			"DROP TABLE IF EXISTS chunks_vec;",
			"DROP TABLE IF EXISTS chunks;",
			"DROP TABLE IF EXISTS docs;",
			"DROP TABLE IF EXISTS meta;",
		}
		for _, q := range dropTables {
			if _, err := db.Exec(q); err != nil {
				return fmt.Errorf("drop table: %w", err)
			}
		}
	}

	// 1. meta table
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`)
	if err != nil {
		return fmt.Errorf("create meta table: %w", err)
	}

	// 2. docs table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS docs (
		id TEXT PRIMARY KEY,
		context TEXT NOT NULL,
		name TEXT NOT NULL,
		version INTEGER NOT NULL,
		hash TEXT NOT NULL,
		tags TEXT NOT NULL DEFAULT '[]'
	);
	CREATE INDEX IF NOT EXISTS idx_docs_context ON docs(context);
	`)
	if err != nil {
		return fmt.Errorf("create docs table: %w", err)
	}
	_, _ = db.Exec("ALTER TABLE docs ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';")

	// 2b. doc_enrichments table (Tier-0 extractive metadata keyed by doc_id + hash)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS doc_enrichments (
		doc_id TEXT NOT NULL,
		hash TEXT NOT NULL,
		title TEXT NOT NULL,
		keywords TEXT NOT NULL,
		lead TEXT NOT NULL,
		PRIMARY KEY (doc_id, hash)
	);
	CREATE INDEX IF NOT EXISTS idx_doc_enrichments_doc_id ON doc_enrichments(doc_id);
	`)
	if err != nil {
		return fmt.Errorf("create doc_enrichments table: %w", err)
	}

	// 3. chunks table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS chunks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		doc_id TEXT NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
		context TEXT NOT NULL,
		ord INTEGER NOT NULL,
		heading TEXT NOT NULL,
		text TEXT NOT NULL,
		text_hash TEXT NOT NULL,
		start_line INTEGER,
		end_line INTEGER,
		vec_pending INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_chunks_doc_id ON chunks(doc_id);
	CREATE INDEX IF NOT EXISTS idx_chunks_context ON chunks(context);
	CREATE INDEX IF NOT EXISTS idx_chunks_text_hash ON chunks(text_hash);
	`)
	if err != nil {
		return fmt.Errorf("create chunks table: %w", err)
	}

	// Schema migration for existing databases: ensure vec_pending column and index exist
	_, _ = db.Exec("ALTER TABLE chunks ADD COLUMN vec_pending INTEGER NOT NULL DEFAULT 0;")
	_, _ = db.Exec("CREATE INDEX IF NOT EXISTS idx_chunks_vec_pending ON chunks(vec_pending);")

	// 3b. chunk_deletions table and trigger for cross-process coherence
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS chunk_deletions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chunk_id INTEGER NOT NULL
	);
	CREATE TRIGGER IF NOT EXISTS trg_chunks_delete
	AFTER DELETE ON chunks
	BEGIN
		INSERT INTO chunk_deletions(chunk_id) VALUES (OLD.id);
	END;
	`)
	if err != nil {
		return fmt.Errorf("create chunk_deletions: %w", err)
	}

	// 4. chunks_vec virtual table (using vec0 with context text partition key and embedding int8[384])
	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_vec USING vec0(
		context text partition key,
		embedding int8[384]
	);`)
	if err != nil {
		return fmt.Errorf("create chunks_vec table: %w", err)
	}

	// 4b. chunks_vec_bit virtual table (using vec0 with context text partition key and embedding bit[384])
	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_vec_bit USING vec0(
		context text partition key,
		embedding bit[384]
	);`)
	if err != nil {
		return fmt.Errorf("create chunks_vec_bit table: %w", err)
	}

	// 5. chunks_fts virtual table (using fts5 with scope, heading, text, enrichment, tokenize='porter unicode61')
	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
		scope,
		heading,
		text,
		enrichment,
		tokenize='porter unicode61'
	);`)
	if err != nil {
		return fmt.Errorf("create chunks_fts table: %w", err)
	}

	// 6. chunks_fts_vocab virtual table for prefix range scans and typo expansion candidates
	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts_vocab USING fts5vocab(chunks_fts, 'row');`)
	if err != nil {
		return fmt.Errorf("create chunks_fts_vocab table: %w", err)
	}

	return nil
}

func checkMeta(db *sql.DB, rebuild bool) error {
	rows, err := db.Query("SELECT key, value FROM meta")
	if err != nil {
		return fmt.Errorf("query meta: %w", err)
	}
	defer rows.Close()

	metaMap := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		metaMap[k] = v
	}

	if len(metaMap) == 0 {
		// Fresh database -> initialize meta
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		insertMeta := `INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`
		if _, err := tx.Exec(insertMeta, "model_id", ModelID); err != nil {
			return err
		}
		if _, err := tx.Exec(insertMeta, "dim", strconv.Itoa(ModelDim)); err != nil {
			return err
		}
		if _, err := tx.Exec(insertMeta, "quant_version", strconv.Itoa(QuantVersion)); err != nil {
			return err
		}
		if _, err := tx.Exec(insertMeta, "schema_version", strconv.Itoa(CurrentSchemaVersion)); err != nil {
			return err
		}
		return tx.Commit()
	}

	// Check compatibility
	mModel := metaMap["model_id"]
	mDim, _ := strconv.Atoi(metaMap["dim"])
	mQuant, _ := strconv.Atoi(metaMap["quant_version"])
	mSchema, _ := strconv.Atoi(metaMap["schema_version"])

	if mModel != ModelID || mDim != ModelDim || mQuant != QuantVersion || mSchema != CurrentSchemaVersion {
		if !rebuild {
			return fmt.Errorf("%w: db has model=%s dim=%d quant=%d schema=%d, current is model=%s dim=%d quant=%d schema=%d",
				ErrNeedsReindex, mModel, mDim, mQuant, mSchema, ModelID, ModelDim, QuantVersion, CurrentSchemaVersion)
		}
		// Rebuild requested
		if err := initSchema(db, true); err != nil {
			return err
		}
		return checkMeta(db, false)
	}

	return nil
}

func isIdentifier(s string) bool {
	if strings.ContainsAny(s, "-_./") {
		return true
	}
	if strings.HasPrefix(s, "ERR_") || strings.HasPrefix(s, "FLAG_") || strings.HasPrefix(s, "UUID") || strings.HasPrefix(s, "PORT_") || strings.HasPrefix(s, "SEC_") || strings.HasPrefix(s, "ENV_") || strings.HasPrefix(s, "CRON_") {
		return true
	}
	hasLetter := false
	hasDigit := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

// ExtractFTS5Terms extracts sanitized identifiers and words from rawText.
func ExtractFTS5Terms(rawText string) (identifiers []string, normalWords []string) {
	fields := strings.Fields(rawText)
	for _, raw := range fields {
		trimmed := strings.TrimFunc(raw, func(r rune) bool {
			return r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' ||
				r == '"' || r == '\'' || r == ',' || r == ';' || r == ':' || r == '?' || r == '!'
		})
		if trimmed == "" {
			continue
		}

		if isIdentifier(trimmed) {
			clean := strings.ReplaceAll(trimmed, `"`, "")
			clean = strings.ReplaceAll(clean, `*`, "")
			if clean != "" {
				identifiers = append(identifiers, fmt.Sprintf("%q", clean))
			}
			continue
		}

		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, trimmed)

		if len(clean) == 0 {
			continue
		}

		lower := strings.ToLower(clean)
		if defaultStopwords[lower] {
			continue
		}
		if len(clean) < 3 {
			continue
		}

		normalWords = append(normalWords, fmt.Sprintf("%q", clean))
	}
	return identifiers, normalWords
}

// BuildFTS5ANDQuery builds an AND conjunction of the query terms.
func BuildFTS5ANDQuery(rawText string, contexts []string) string {
	var scopeClause string
	if len(contexts) > 0 {
		scopeTokens := make([]string, len(contexts))
		for i, ctx := range contexts {
			scopeTokens[i] = ScopeToken(ctx)
		}
		if len(scopeTokens) == 1 {
			scopeClause = fmt.Sprintf("scope: %s", scopeTokens[0])
		} else {
			scopeClause = fmt.Sprintf("scope: (%s)", strings.Join(scopeTokens, " OR "))
		}
	}

	idents, words := ExtractFTS5Terms(rawText)
	allTerms := append(idents, words...)
	if len(allTerms) == 0 {
		return ""
	}

	textClause := "(" + strings.Join(allTerms, " AND ") + ")"
	if scopeClause == "" {
		return textClause
	}
	return scopeClause + " AND " + textClause
}

// BuildFTS5CappedORQuery builds a disjunctive OR query capping normal words to maxTerms.
func BuildFTS5CappedORQuery(rawText string, contexts []string, maxTerms int) string {
	var scopeClause string
	if len(contexts) > 0 {
		scopeTokens := make([]string, len(contexts))
		for i, ctx := range contexts {
			scopeTokens[i] = ScopeToken(ctx)
		}
		if len(scopeTokens) == 1 {
			scopeClause = fmt.Sprintf("scope: %s", scopeTokens[0])
		} else {
			scopeClause = fmt.Sprintf("scope: (%s)", strings.Join(scopeTokens, " OR "))
		}
	}

	if maxTerms <= 0 {
		maxTerms = 8
	}

	idents, words := ExtractFTS5Terms(rawText)
	// Sort normal words by length descending (longer words are rarer and carry more BM25 signal)
	for i := 0; i < len(words); i++ {
		for j := i + 1; j < len(words); j++ {
			if len(words[j]) > len(words[i]) {
				words[i], words[j] = words[j], words[i]
			}
		}
	}

	selected := append([]string{}, idents...)
	budget := maxTerms - len(selected)
	if budget > 0 {
		if len(words) > budget {
			selected = append(selected, words[:budget]...)
		} else {
			selected = append(selected, words...)
		}
	}

	if len(selected) == 0 {
		// Fallback for short punctuation/stopword only query
		fields := strings.Fields(rawText)
		for _, raw := range fields {
			clean := strings.Map(func(r rune) rune {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					return r
				}
				return -1
			}, raw)
			if clean != "" {
				selected = append(selected, fmt.Sprintf("%q", clean))
				if len(selected) >= maxTerms {
					break
				}
			}
		}
	}

	if len(selected) == 0 {
		return scopeClause
	}

	textClause := "(" + strings.Join(selected, " OR ") + ")"
	if scopeClause == "" {
		return textClause
	}
	return scopeClause + " AND " + textClause
}

// SanitizeFTS5 builds a safe, injection-proof FTS5 MATCH query with scoped filtering.
// Context slugs are hashed to valid scope tokens: scope:(cA OR cB).
// English stopwords are removed for disjunctive queries, short words (<3 chars) dropped unless identifiers,
// and identifiers (containing hyphens, underscores, dots, paths, UUIDs, flags) are preserved as quoted phrase terms.
// Special FTS5 characters (quotes, colons, asterisks, hyphens, NEAR, NOT) are safely handled.
func SanitizeFTS5(rawText string, contexts []string) string {
	return BuildFTS5CappedORQuery(rawText, contexts, 8)
}
