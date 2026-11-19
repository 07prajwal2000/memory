package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"memory/internal/llm"
)

type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, m.dim)
		words := strings.Fields(strings.ToLower(text))
		for _, w := range words {
			var h uint32 = 2166136261
			for _, c := range []byte(w) {
				h ^= uint32(c)
				h *= 16777619
			}
			idx := int(h % uint32(m.dim))
			vec[idx] += 1.0
		}
		// Normalize L2
		var norm float32
		for _, v := range vec {
			norm += v * v
		}
		if norm > 0 {
			inv := float32(1.0 / math.Sqrt(float64(norm)))
			for d := range vec {
				vec[d] *= inv
			}
		}
		out[i] = vec
	}
	return out, nil
}

// 1. Test stopword and identifier query building
func TestSanitizeFTS5_StopwordsAndIdentifiers(t *testing.T) {
	// Query with common English stopwords
	qStop := "where is the audit log stored and how do we access it"
	sanStop := SanitizeFTS5(qStop, []string{"global"})
	// "where", "is", "the", "and", "how", "do", "we", "it" should be stripped
	if strings.Contains(sanStop, `"where"`) || strings.Contains(sanStop, `"the"`) || strings.Contains(sanStop, `"and"`) {
		t.Fatalf("SanitizeFTS5 should have removed common stopwords, got: %s", sanStop)
	}
	// "audit", "log", "stored", "access" should remain
	for _, term := range []string{`"audit"`, `"log"`, `"stored"`, `"access"`} {
		if !strings.Contains(sanStop, term) {
			t.Fatalf("SanitizeFTS5 missing expected content term %s, got: %s", term, sanStop)
		}
	}

	// Query with identifiers
	qIdent := "check ERR_AUTH_001 in /var/log/audit.log with FLAG_ENABLE_BETA and --expiry=3600"
	sanIdent := SanitizeFTS5(qIdent, []string{"tenantA"})
	for _, ident := range []string{`"ERR_AUTH_001"`, `"/var/log/audit.log"`, `"FLAG_ENABLE_BETA"`, `"--expiry=3600"`} {
		if !strings.Contains(sanIdent, ident) {
			t.Fatalf("SanitizeFTS5 failed to preserve identifier %s as phrase term, got: %s", ident, sanIdent)
		}
	}

	// Fallback when all terms are stopwords/short words
	qAllStop := "to be or not to be in on at"
	sanAllStop := SanitizeFTS5(qAllStop, []string{"global"})
	if !strings.Contains(sanAllStop, `"to"`) && !strings.Contains(sanAllStop, `"be"`) {
		t.Fatalf("SanitizeFTS5 fallback failed to preserve alphanumeric terms on all-stopword query, got: %s", sanAllStop)
	}
}

// 2. Test weighted RRF math with ties
func TestWeightedRRF_MathAndTies(t *testing.T) {
	vecRanks := map[int64]int{
		101: 1,
		102: 2,
		103: 3,
	}
	bm25Ranks := map[int64]int{
		102: 1,
		103: 3,
		104: 2,
	}

	wVec := 1.0
	wBM25 := 0.10
	k := 10.0

	merged := WeightedRRF(vecRanks, bm25Ranks, wVec, wBM25, k)

	// Item 101: vec rank 1 -> 1.0 / (10 + 1) = 1/11 ~ 0.090909
	expected101 := 1.0 / 11.0
	if math.Abs(merged[101].score-expected101) > 1e-6 {
		t.Fatalf("item 101 score mismatch: got %f, want %f", merged[101].score, expected101)
	}

	// Item 102: vec rank 2 (1/12) + bm25 rank 1 (0.1/11) = 0.083333 + 0.009091 = 0.092424
	expected102 := (1.0 / 12.0) + (0.10 / 11.0)
	if math.Abs(merged[102].score-expected102) > 1e-6 {
		t.Fatalf("item 102 score mismatch: got %f, want %f", merged[102].score, expected102)
	}

	// Item 102 should outrank item 101 due to high BM25 rank
	if merged[102].score <= merged[101].score {
		t.Fatalf("expected item 102 (score %f) > item 101 (score %f)", merged[102].score, merged[101].score)
	}

	// Test ties: equal weights and ranks
	tieVec := map[int64]int{1: 1, 2: 2}
	tieBM := map[int64]int{1: 2, 2: 1}
	tieMerged := WeightedRRF(tieVec, tieBM, 1.0, 1.0, 10.0)
	// Both item 1 and item 2 should have score = 1/11 + 1/12
	if math.Abs(tieMerged[1].score-tieMerged[2].score) > 1e-6 {
		t.Fatalf("expected tied scores, got %f and %f", tieMerged[1].score, tieMerged[2].score)
	}
}

// 3. Test two-stage search equals exact search on small deterministic set
func TestTwoStageSearch_DeterministicRecall(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "search_twostage.db")

	embedder := &mockEmbedder{dim: ModelDim}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithTwoStageThreshold(0))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	// Seed 30 documents with distinct topics
	topics := []string{
		"PostgreSQL WAL replication configuration and slot monitoring",
		"Redis sentinel failover quorum and timeout settings",
		"Kubernetes ingress controller certificate manager ACME",
		"Kafka consumer group rebalancing partition assignment strategy",
		"Elasticsearch index lifecycle management rollover conditions",
		"Prometheus scrape interval alerting rule recording rule",
		"Grafana dashboard provisioning datasource proxy authentication",
		"Docker daemon storage driver overlay2 configuration",
		"Linux systemd service unit file restart policy on-failure",
		"OpenSSL cipher suite TLS 1.3 configuration guideline",
		"Nginx reverse proxy upstream keepalive timeout buffer size",
		"HAProxy balance roundrobin health check interval fall rise",
		"Vault secret engine kv version 2 lease ttl renewal",
		"Terraform state backend s3 bucket dynamodb table locking",
		"Ansible playbook handler notify idempotency check",
		"MongoDB replica set election priority voting members",
		"Cassandra partition key clustering column compaction strategy",
		"RabbitMQ dead letter exchange queue message ttl policy",
		"Zookeeper ensemble leader election tickTime initLimit",
		"Memcached slab allocator memory chunk size distribution",
		"MySQL binlog row based format gtid mode enforcement",
		"ClickHouse merge tree engine partition by toYYYYMM order by",
		"Airflow DAG schedule interval cron catchup execution date",
		"Spark executor memory cores driver memory overhead ratio",
		"Flink checkpoint interval state backend rocksdb incremental",
		"Presto coordinator worker memory heap query max stage count",
		"Consul service mesh envoy sidecar proxy connect registration",
		"Envoy filter chain rate limiting jwt authentication listener",
		"Traefik dynamic configuration middleware strip prefix headers",
		"Etcd cluster member peer url snapshot save restore recovery",
	}

	for i, topic := range topics {
		doc := Doc{
			ID:      fmt.Sprintf("doc-%d", i+1),
			Context: "devops",
			Name:    fmt.Sprintf("Topic %d", i+1),
			Body:    fmt.Sprintf("# %s\n\nDetailed operational instructions for %s in production environments.", topic, topic),
			Version: 1,
		}
		if err := ix.Upsert(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}

	// Verify both exact and two-stage search find the top document
	for i, topic := range topics[:10] {
		qExact := Query{
			Text:       topic,
			Contexts:   []string{"devops"},
			Limit:      5,
			VectorMode: VectorSearchExact,
		}
		resExact, err := ix.Search(ctx, qExact)
		if err != nil {
			t.Fatal(err)
		}

		qTwoStage := Query{
			Text:       topic,
			Contexts:   []string{"devops"},
			Limit:      5,
			VectorMode: VectorSearchTwoStage,
		}
		resTwoStage, err := ix.Search(ctx, qTwoStage)
		if err != nil {
			t.Fatal(err)
		}

		expectedDocID := fmt.Sprintf("doc-%d", i+1)
		if len(resExact.Hits) == 0 || resExact.Hits[0].DocID != expectedDocID {
			t.Fatalf("exact search missed expected doc %s", expectedDocID)
		}
		if len(resTwoStage.Hits) == 0 || resTwoStage.Hits[0].DocID != expectedDocID {
			t.Fatalf("two-stage search missed expected doc %s", expectedDocID)
		}
		// Exact top hit must match
		if resExact.Hits[0].DocID != resTwoStage.Hits[0].DocID {
			t.Fatalf("top hit mismatch for %q: exact=%s, two-stage=%s", topic, resExact.Hits[0].DocID, resTwoStage.Hits[0].DocID)
		}
	}
}

// 4. Test per-context chunk count cache invalidation on upsert/delete
func TestContextChunkCount_CacheInvalidation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "search_counts.db")

	embedder := &mockEmbedder{dim: ModelDim}
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	if count := ix.ContextChunkCount("tenant1"); count != 0 {
		t.Fatalf("expected 0 chunks initially, got %d", count)
	}

	// Doc with ~3 chunks
	body3 := "# Section 1\n" + strings.Repeat("word ", 150) + "\n# Section 2\n" + strings.Repeat("word ", 150) + "\n# Section 3\n" + strings.Repeat("word ", 150)
	doc1 := Doc{
		ID:      "doc-1",
		Context: "tenant1",
		Name:    "Doc 1",
		Body:    body3,
		Version: 1,
	}
	if err := ix.Upsert(ctx, doc1); err != nil {
		t.Fatal(err)
	}

	count1 := ix.ContextChunkCount("tenant1")
	if count1 == 0 {
		t.Fatal("expected non-zero chunk count after upsert")
	}

	// Upsert same doc with more content (version 2)
	body5 := body3 + "\n# Section 4\n" + strings.Repeat("word ", 150) + "\n# Section 5\n" + strings.Repeat("word ", 150)
	doc1v2 := Doc{
		ID:      "doc-1",
		Context: "tenant1",
		Name:    "Doc 1",
		Body:    body5,
		Version: 2,
	}
	if err := ix.Upsert(ctx, doc1v2); err != nil {
		t.Fatal(err)
	}

	count2 := ix.ContextChunkCount("tenant1")
	if count2 <= count1 {
		t.Fatalf("expected updated chunk count > %d, got %d", count1, count2)
	}

	// Add doc to tenant2
	doc2 := Doc{
		ID:      "doc-2",
		Context: "tenant2",
		Name:    "Doc 2",
		Body:    body3,
		Version: 1,
	}
	if err := ix.Upsert(ctx, doc2); err != nil {
		t.Fatal(err)
	}

	tot := ix.TotalChunksInContexts([]string{"tenant1", "tenant2"})
	if tot != count2+ix.ContextChunkCount("tenant2") {
		t.Fatalf("TotalChunksInContexts mismatch: got %d, want %d", tot, count2+ix.ContextChunkCount("tenant2"))
	}

	// Delete doc-1
	if err := ix.Delete(ctx, "doc-1"); err != nil {
		t.Fatal(err)
	}
	if countAfterDel := ix.ContextChunkCount("tenant1"); countAfterDel != 0 {
		t.Fatalf("expected 0 chunks for tenant1 after deleting doc-1, got %d", countAfterDel)
	}

	// DeleteContext tenant2
	if err := ix.DeleteContext(ctx, "tenant2"); err != nil {
		t.Fatal(err)
	}
	if countTenant2 := ix.ContextChunkCount("tenant2"); countTenant2 != 0 {
		t.Fatalf("expected 0 chunks for tenant2 after DeleteContext, got %d", countTenant2)
	}
}

// 5. Test rerank never runs without a reranker supplied
func TestRerank_NeverWithoutReranker(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "search_norerank.db")

	embedder := &mockEmbedder{dim: ModelDim}
	// Pass nil reranker to OpenIndex
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	doc := Doc{
		ID:      "doc-alpha",
		Context: "global",
		Name:    "Architecture Overview",
		Body:    "# Overview\n\nSystem architecture and memory subsystem indexing rules.",
		Version: 1,
	}
	if err := ix.Upsert(ctx, doc); err != nil {
		t.Fatal(err)
	}

	// Search with NoRerank explicitly false (i.e. requesting rerank if available)
	res, err := ix.Search(ctx, Query{
		Text:     "memory subsystem indexing rules",
		Contexts: []string{"global"},
		Limit:    5,
		NoRerank: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Hits) == 0 {
		t.Fatal("expected at least 1 hit")
	}

	// Check that rerank timing is 0 and no hit has "rerank" in sources
	if res.Timings.Rerank != 0 {
		t.Fatalf("expected 0 rerank timing without reranker, got %v", res.Timings.Rerank)
	}
	for _, h := range res.Hits {
		for _, s := range h.Sources {
			if s == "rerank" {
				t.Fatalf("unexpected rerank source in hit: %v", h.Sources)
			}
		}
	}
}

// 6. Test schema_version bump forces rebuild
func TestSchemaVersionBump_ForcesRebuild(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "search_schema_bump.db")

	// Pre-create database with schema_version = 1
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_txlock=immediate", dbPath))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO meta (key, value) VALUES
			('schema_version', '1'),
			('quant_version', '1'),
			('model_id', 'bge-small-en-v1.5-q8_0'),
			('dim', '384');
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	embedder := &mockEmbedder{dim: ModelDim}

	// Opening without WithRebuild() should fail with ErrNeedsReindex
	_, err = OpenIndex(dbPath, embedder, nil)
	if err == nil {
		t.Fatal("expected OpenIndex to fail with ErrNeedsReindex for old schema version 1")
	}
	if !errors.Is(err, ErrNeedsReindex) {
		t.Fatalf("expected ErrNeedsReindex, got: %v", err)
	}

	// Opening with WithRebuild() should succeed and upgrade schema_version to CurrentSchemaVersion (2)
	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
	if err != nil {
		t.Fatalf("OpenIndex with WithRebuild() failed: %v", err)
	}
	defer ix.Close()

	// Verify schema_version in meta is now CurrentSchemaVersion
	var newVersion int
	err = ix.db.QueryRow("SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'schema_version'").Scan(&newVersion)
	if err != nil {
		t.Fatal(err)
	}
	if newVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema_version = %d, got %d", CurrentSchemaVersion, newVersion)
	}
}
