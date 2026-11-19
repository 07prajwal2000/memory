package search

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
)

// Generate Zipf word generator
type zipfGen struct {
	vocab []string
	dist  *rand.Zipf
	r     *rand.Rand
}

func newZipfGen(vocab []string, s float64, v float64) *zipfGen {
	r := rand.New(rand.NewSource(42))
	z := rand.NewZipf(r, s, v, uint64(len(vocab)-1))
	return &zipfGen{vocab: vocab, dist: z, r: r}
}

func (zg *zipfGen) NextWord() string {
	idx := zg.dist.Uint64()
	return zg.vocab[idx]
}

func (zg *zipfGen) NextPassage(nWords int) string {
	words := make([]string, nWords)
	for i := range words {
		words[i] = zg.NextWord()
	}
	return strings.Join(words, " ")
}

func TestFTS5_ScopedVsPostFilter(t *testing.T) {
	if os.Getenv("MEM_RUN_FTS_BENCH") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping FTS5 200k benchmark (set MEM_RUN_FTS_BENCH=1 or MEM_BENCH=1 to run)")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fts_bench.db")

	dsn := fmt.Sprintf("file:%s?_txlock=immediate", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	_, _ = db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 5000;
		PRAGMA synchronous = NORMAL;
	`)

	_, err = db.Exec(`
		CREATE TABLE chunks (
			id INTEGER PRIMARY KEY,
			context TEXT NOT NULL
		);
		CREATE INDEX idx_chunks_ctx ON chunks(context);

		CREATE VIRTUAL TABLE chunks_fts USING fts5(
			scope,
			heading,
			text,
			tokenize='porter unicode61'
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	// 1. Build Vocabulary of 5,000 common engineering/systems words
	rawVocab := []string{
		"system", "database", "network", "cache", "token", "service", "cluster", "memory", "storage", "query",
		"index", "process", "connection", "client", "server", "request", "response", "latency", "throughput", "buffer",
		"thread", "worker", "lock", "transaction", "replication", "partition", "protocol", "endpoint", "handler", "router",
		"pipeline", "stream", "message", "queue", "topic", "consumer", "producer", "broker", "event", "state",
		"context", "timeout", "retry", "failure", "recovery", "checkpoint", "backup", "restore", "snapshot", "log",
		"commit", "rollback", "isolation", "concurrency", "consistency", "availability", "durability", "atomicity", "schema", "table",
		"column", "record", "document", "vector", "embedding", "distance", "metric", "ranking", "scoring", "filter",
		"search", "match", "scan", "lookup", "tree", "hash", "table", "graph", "node", "edge",
		"security", "auth", "permission", "access", "role", "policy", "certificate", "cipher", "secret", "key",
		"container", "image", "volume", "pod", "deployment", "ingress", "egress", "proxy", "gateway", "firewall",
	}
	vocabMap := make(map[string]bool)
	for _, w := range rawVocab {
		vocabMap[w] = true
	}
	for i := 0; len(vocabMap) < 5000; i++ {
		vocabMap[fmt.Sprintf("term%d", i)] = true
	}
	vocab := make([]string, 0, len(vocabMap))
	for w := range vocabMap {
		vocab = append(vocab, w)
	}

	zg := newZipfGen(vocab, 1.05, 1.0)

	// 2. Insert 200,000 chunks across 20 contexts
	// Context 0 is "global" with 30,000 chunks.
	// Contexts 1-19 have ~8,947 chunks each.
	contexts := make([]string, 20)
	contexts[0] = "global"
	for i := 1; i < 20; i++ {
		contexts[i] = fmt.Sprintf("tenant-%02d", i)
	}

	const totalChunks = 200000
	t.Logf("Inserting %d chunks into FTS5...", totalChunks)
	t0 := time.Now()

	stmtChunk, _ := db.Prepare("INSERT INTO chunks(id, context) VALUES (?, ?)")
	stmtFts, _ := db.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, ?, ?)")

	const batchSize = 5000
	for b := 0; b < totalChunks; b += batchSize {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		txChunk := tx.Stmt(stmtChunk)
		txFts := tx.Stmt(stmtFts)

		end := b + batchSize
		for i := b; i < end; i++ {
			chunkID := int64(i + 1)
			var ctxSlug string
			if i < 30000 {
				ctxSlug = "global"
			} else {
				ctxSlug = contexts[1+((i-30000)%19)]
			}
			scopeTok := ScopeToken(ctxSlug)
			text := zg.NextPassage(60)

			_, _ = txChunk.Exec(chunkID, ctxSlug)
			_, _ = txFts.Exec(chunkID, scopeTok, "Heading "+ctxSlug, text)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("Inserted %d chunks in %v", totalChunks, time.Since(t0))

	// 3. Generate 50 realistic multi-word queries targeting global + one context
	queries := make([]struct {
		ctxSlug  string
		text     string
		terms    []string
	}, 50)

	for i := 0; i < 50; i++ {
		targetCtx := contexts[1+(i%19)]
		// Pick 2-3 popular terms
		t1 := rawVocab[rand.Intn(len(rawVocab))]
		t2 := rawVocab[rand.Intn(len(rawVocab))]
		queries[i] = struct {
			ctxSlug string
			text    string
			terms   []string
		}{
			ctxSlug: targetCtx,
			text:    fmt.Sprintf("%s %s", t1, t2),
			terms:   []string{t1, t2},
		}
	}

	// 4. Benchmark Scoped FTS5
	t.Log("Benchmarking Scoped FTS5: scope:(cGlobal OR cCtx) AND (terms)...")
	scopedLatencies := make([]float64, len(queries))
	for i, q := range queries {
		scopeGlobal := ScopeToken("global")
		scopeTarget := ScopeToken(q.ctxSlug)
		sanitized := fmt.Sprintf("scope: (%s OR %s) AND (%q OR %q)", scopeGlobal, scopeTarget, q.terms[0], q.terms[1])

		tQuery := time.Now()
		rows, err := db.Query(`
			SELECT rowid, bm25(chunks_fts)
			FROM chunks_fts
			WHERE chunks_fts MATCH ?
			ORDER BY bm25(chunks_fts)
			LIMIT 50
		`, sanitized)
		if err != nil {
			t.Fatalf("scoped query failed: %v", err)
		}
		count := 0
		for rows.Next() {
			count++
		}
		rows.Close()
		scopedLatencies[i] = float64(time.Since(tQuery).Microseconds()) / 1000.0 // ms
	}

	// 5. Benchmark Post-filtered FTS5
	t.Log("Benchmarking Post-filtered FTS5: MATCH (terms) JOIN chunks WHERE c.context IN ('global', ctx)...")
	postLatencies := make([]float64, len(queries))
	for i, q := range queries {
		matchExpr := fmt.Sprintf("(%q OR %q)", q.terms[0], q.terms[1])

		tQuery := time.Now()
		rows, err := db.Query(`
			SELECT f.rowid, bm25(chunks_fts)
			FROM chunks_fts f
			JOIN chunks c ON f.rowid = c.id
			WHERE f.chunks_fts MATCH ? AND c.context IN ('global', ?)
			ORDER BY bm25(chunks_fts)
			LIMIT 50
		`, matchExpr, q.ctxSlug)
		if err != nil {
			t.Fatalf("post-filter query failed: %v", err)
		}
		count := 0
		for rows.Next() {
			count++
		}
		rows.Close()
		postLatencies[i] = float64(time.Since(tQuery).Microseconds()) / 1000.0 // ms
	}

	calcPercentiles := func(data []float64) (p50, p95 float64) {
		sorted := make([]float64, len(data))
		copy(sorted, data)
		sort.Float64s(sorted)
		p50 = sorted[int(float64(len(sorted))*0.50)]
		p95 = sorted[int(float64(len(sorted))*0.95)]
		return p50, p95
	}

	scopedP50, scopedP95 := calcPercentiles(scopedLatencies)
	postP50, postP95 := calcPercentiles(postLatencies)

	t.Logf("=== FTS5 200k Benchmark Results ===")
	t.Logf("Scoped FTS5 (scope token): p50 = %.2f ms | p95 = %.2f ms", scopedP50, scopedP95)
	t.Logf("Post-filtered (JOIN):      p50 = %.2f ms | p95 = %.2f ms", postP50, postP95)
	speedup := postP50 / scopedP50
	t.Logf("Scoped Speedup: %.2fx faster", speedup)

	// Save to JSON
	benchData := map[string]interface{}{
		"corpus_size":    totalChunks,
		"contexts_count": 20,
		"scoped_fts5": map[string]float64{
			"p50_ms": math.Round(scopedP50*100) / 100,
			"p95_ms": math.Round(scopedP95*100) / 100,
		},
		"post_filtered": map[string]float64{
			"p50_ms": math.Round(postP50*100) / 100,
			"p95_ms": math.Round(postP95*100) / 100,
		},
		"speedup_factor": math.Round(speedup*100) / 100,
	}
	bytes, _ := json.MarshalIndent(benchData, "", "  ")
	_ = os.WriteFile("testdata_fts_benchmark.json", bytes, 0644)
}
