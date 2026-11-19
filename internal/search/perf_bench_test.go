package search

import (
	"context"
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

	"memory/internal/llm"
)

type deterministicClusteredEmbedder struct {
	dim      int
	clusters [][]float32
}

func newClusteredEmbedder(dim, numClusters int) *deterministicClusteredEmbedder {
	rnd := rand.New(rand.NewSource(42))
	clusters := make([][]float32, numClusters)
	for c := 0; c < numClusters; c++ {
		vec := make([]float32, dim)
		var sum float64
		for d := 0; d < dim; d++ {
			v := float32(rnd.NormFloat64())
			vec[d] = v
			sum += float64(v * v)
		}
		inv := float32(1.0 / math.Sqrt(sum))
		for d := 0; d < dim; d++ {
			vec[d] *= inv
		}
		clusters[c] = vec
	}
	return &deterministicClusteredEmbedder{dim: dim, clusters: clusters}
}

func (e *deterministicClusteredEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	if kind == llm.KindQuery {
		// Simulate measured llama-server single-text query embedding latency (~5.3 ms)
		time.Sleep(5300 * time.Microsecond)
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		var h uint32 = 2166136261
		for _, b := range []byte(text) {
			h ^= uint32(b)
			h *= 16777619
		}
		cIdx := int(h % uint32(len(e.clusters)))
		center := e.clusters[cIdx]

		vec := make([]float32, e.dim)
		r := rand.New(rand.NewSource(int64(h)))
		var sum float64
		for d := 0; d < e.dim; d++ {
			noise := float32(r.NormFloat64() * 0.1)
			val := center[d] + noise
			vec[d] = val
			sum += float64(val * val)
		}
		inv := float32(1.0 / math.Sqrt(sum))
		for d := 0; d < e.dim; d++ {
			vec[d] *= inv
		}
		out[i] = vec
	}
	return out, nil
}

func generateZipfText(rnd *rand.Rand, zipf *rand.Zipf, wordCount int, vocab []string) string {
	var sb strings.Builder
	for w := 0; w < wordCount; w++ {
		idx := zipf.Uint64()
		if idx >= uint64(len(vocab)) {
			idx = uint64(len(vocab) - 1)
		}
		if w > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(vocab[idx])
	}
	return sb.String()
}

type stageStats struct {
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
}

type scaleBenchmarkReport struct {
	Scale       string     `json:"scale"`
	Mode        string     `json:"mode"`
	Embed       stageStats `json:"embed"`
	VecKNN      stageStats `json:"vec_knn"`
	FTS         stageStats `json:"fts"`
	Fusion      stageStats `json:"fusion"`
	Hydration   stageStats `json:"hydration"`
	Unaccounted stageStats `json:"unaccounted"`
	Total       stageStats `json:"total"`
}

func TestPerformance_200kAndBitBench(t *testing.T) {
	if os.Getenv("MEM_RUN_PERF_BENCH") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping performance benchmark (set MEM_RUN_PERF_BENCH=1 or MEM_BENCH=1 to run)")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "perf_bench_comprehensive.db")

	embedder := newClusteredEmbedder(ModelDim, 64)

	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithTwoStageThreshold(10000), WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	defer ix.Close()

	ctx := context.Background()

	// Vocabulary of 5,000 words
	vocab := make([]string, 5000)
	for i := 0; i < 5000; i++ {
		vocab[i] = fmt.Sprintf("term%04d", i)
	}
	// Add common technical words
	techWords := []string{
		"architecture", "database", "indexing", "memory", "storage", "performance",
		"cluster", "query", "latency", "throughput", "processor", "cache", "network",
		"protocol", "distributed", "partition", "replication", "consistency", "backup",
	}
	for i, w := range techWords {
		vocab[i] = w
	}

	rnd := rand.New(rand.NewSource(123456))
	zipf := rand.NewZipf(rnd, 1.2, 1.0, 4999)

	t.Log("=== Populating 200,000 Chunks across Partition Contexts ===")
	tInsertStart := time.Now()

	// Context allocation:
	// - ctx-10k: 10,000 chunks
	// - global-9k: 9,000 chunks
	// - ctx-30k: 30,000 chunks
	// - ctx-100k: 51,000 chunks (so global-9k + ctx-30k + ctx-10k + ctx-100k = 100k)
	// - ctx-extra-100k: 100,000 chunks (total = 200k)

	type partitionSpec struct {
		context string
		count   int
	}
	partitions := []partitionSpec{
		{context: "ctx-10k", count: 10000},
		{context: "global-9k", count: 9000},
		{context: "ctx-30k", count: 30000},
		{context: "ctx-51k", count: 51000},
		{context: "ctx-extra-100k", count: 100000},
	}

	totalChunksCount := 200000
	const batchInsertSize = 5000

	tx, err := ix.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmtChunk, _ := tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
	stmtFts, _ := tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, ?, ?)")
	stmtVecI8, _ := tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
	stmtVecBit, _ := tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, ?, vec_bit(?))")

	chunkID := int64(1)
	for _, spec := range partitions {
		scopeTok := ScopeToken(spec.context)
		for i := 0; i < spec.count; i++ {
			text := generateZipfText(rnd, zipf, 40, vocab)
			th := Sha256Hex(text)

			// Generate clustered vector
			fvecs, _ := embedder.Embed(ctx, llm.KindDocument, []string{text})
			i8Vec := Quantize(fvecs[0])
			bitVec := QuantizeBit(fvecs[0])

			docID := fmt.Sprintf("doc-%s-%d", spec.context, i/5)
			heading := fmt.Sprintf("Section %d", i%5)

			stmtChunk.Exec(chunkID, docID, spec.context, i%5, heading, text, th, 1, 10)
			stmtFts.Exec(chunkID, scopeTok, heading, text)
			stmtVecI8.Exec(chunkID, spec.context, i8Vec)
			stmtVecBit.Exec(chunkID, spec.context, bitVec)
			ix.vecStore.Upsert(chunkID, spec.context, i8Vec, bitVec)

			if chunkID%batchInsertSize == 0 {
				stmtChunk.Close()
				stmtFts.Close()
				stmtVecI8.Close()
				stmtVecBit.Close()
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				t.Logf("Inserted %d / %d chunks...", chunkID, totalChunksCount)
				tx, err = ix.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				stmtChunk, _ = tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
				stmtFts, _ = tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, ?, ?)")
				stmtVecI8, _ = tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
				stmtVecBit, _ = tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, ?, vec_bit(?))")
			}
			chunkID++
		}
	}
	stmtChunk.Close()
	stmtFts.Close()
	stmtVecI8.Close()
	stmtVecBit.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	insertDuration := time.Since(tInsertStart)
	throughput := float64(totalChunksCount) / insertDuration.Seconds()
	fi, _ := os.Stat(dbPath)
	dbSizeMB := float64(fi.Size()) / (1024 * 1024)

	t.Logf("Populated %d chunks in %v (%.2f chunks/s). DB size: %.2f MB", totalChunksCount, insertDuration, throughput, dbSizeMB)

	// Refresh index context counts
	ix.countsMu.Lock()
	ix.contextCounts = map[string]int{
		"ctx-10k":        10000,
		"global-9k":      9000,
		"ctx-30k":        30000,
		"ctx-51k":        51000,
		"ctx-extra-100k": 100000,
	}
	ix.countsMu.Unlock()

	// Define scale scopes
	type scaleDef struct {
		name     string
		contexts []string
	}
	scales := []scaleDef{
		{name: "10k", contexts: []string{"ctx-10k"}},
		{name: "39k", contexts: []string{"global-9k", "ctx-30k"}},
		{name: "100k", contexts: []string{"global-9k", "ctx-30k", "ctx-10k", "ctx-51k"}},
		{name: "200k", contexts: []string{"global-9k", "ctx-30k", "ctx-10k", "ctx-51k", "ctx-extra-100k"}},
	}

	// Prepare 200 realistic search queries
	searchQueries := make([]string, 200)
	for q := 0; q < 200; q++ {
		w1 := vocab[zipf.Uint64()%uint64(len(vocab))]
		w2 := vocab[zipf.Uint64()%uint64(len(vocab))]
		w3 := techWords[q%len(techWords)]
		searchQueries[q] = fmt.Sprintf("%s %s %s", w1, w2, w3)
	}

	calcStats := func(vals []float64) stageStats {
		sort.Float64s(vals)
		return stageStats{
			P50: vals[int(float64(len(vals))*0.50)],
			P95: vals[int(float64(len(vals))*0.95)],
		}
	}

	var allReports []scaleBenchmarkReport

	t.Log("\n=================== BENCHMARK: STAGE TIMING ACROSS SCALES (200 queries/scale) ===================")

	for _, sc := range scales {
		modes := []struct {
			name string
			mode VectorSearchMode
		}{
			{name: "Exact-int8", mode: VectorSearchExact},
			{name: "Two-Stage-bit", mode: VectorSearchTwoStage},
		}

		for _, m := range modes {
			embedVals := make([]float64, 200)
			knnVals := make([]float64, 200)
			ftsVals := make([]float64, 200)
			fusionVals := make([]float64, 200)
			hydrationVals := make([]float64, 200)
			unaccountedVals := make([]float64, 200)
			totalVals := make([]float64, 200)

			for q := 0; q < 200; q++ {
				qText := searchQueries[q]
				res, err := ix.Search(ctx, Query{
					Text:       qText,
					Contexts:   sc.contexts,
					Limit:      10,
					VectorMode: m.mode,
				})
				if err != nil {
					t.Fatalf("Search failed at %s (%s): %v", sc.name, m.name, err)
				}

				embedVals[q] = float64(res.Timings.EmbedQuery.Microseconds()) / 1000.0
				knnVals[q] = float64(res.Timings.VecKNN.Microseconds()) / 1000.0
				ftsVals[q] = float64(res.Timings.FTS.Microseconds()) / 1000.0
				fusionVals[q] = float64(res.Timings.Fusion.Microseconds()) / 1000.0
				hydrationVals[q] = float64(res.Timings.Hydration.Microseconds()) / 1000.0
				unaccountedVals[q] = float64(res.Timings.Unaccounted.Microseconds()) / 1000.0
				totalVals[q] = float64(res.Timings.Total.Microseconds()) / 1000.0
			}

			rep := scaleBenchmarkReport{
				Scale:       sc.name,
				Mode:        m.name,
				Embed:       calcStats(embedVals),
				VecKNN:      calcStats(knnVals),
				FTS:         calcStats(ftsVals),
				Fusion:      calcStats(fusionVals),
				Hydration:   calcStats(hydrationVals),
				Unaccounted: calcStats(unaccountedVals),
				Total:       calcStats(totalVals),
			}
			allReports = append(allReports, rep)

			t.Logf("[%s | %s] Total: p50=%.2fms, p95=%.2fms | Embed: p50=%.2fms, p95=%.2fms | VecKNN: p50=%.2fms, p95=%.2fms | FTS: p50=%.2fms, p95=%.2fms | Hydration: p50=%.2fms, p95=%.2fms | Fusion: p50=%.2fms, p95=%.2fms | Unacc: p50=%.2fms, p95=%.2fms",
				sc.name, m.name, rep.Total.P50, rep.Total.P95, rep.Embed.P50, rep.Embed.P95, rep.VecKNN.P50, rep.VecKNN.P95, rep.FTS.P50, rep.FTS.P95, rep.Hydration.P50, rep.Hydration.P95, rep.Fusion.P50, rep.Fusion.P95, rep.Unaccounted.P50, rep.Unaccounted.P95)
		}
	}

	// Output Markdown Table
	t.Log("\n======================= STAGE TIMING LATENCY SUMMARY TABLE =======================")
	t.Log("| Chunks in Scope | Search Mode | Embed (p50 / p95) | Vec KNN (p50 / p95) | FTS5 (p50 / p95) | Hydration (p50 / p95) | Fusion (p50 / p95) | Unaccounted (p50 / p95) | **Total Pipeline (p50 / p95)** |")
	t.Log("|---|---|---|---|---|---|---|---|---|")
	for _, rep := range allReports {
		t.Logf("| **%s** | %s | %.2f ms / %.2f ms | %.2f ms / %.2f ms | %.2f ms / %.2f ms | %.2f ms / %.2f ms | %.2f ms / %.2f ms | %.2f ms / %.2f ms | **%.2f ms / %.2f ms** |",
			rep.Scale, rep.Mode,
			rep.Embed.P50, rep.Embed.P95,
			rep.VecKNN.P50, rep.VecKNN.P95,
			rep.FTS.P50, rep.FTS.P95,
			rep.Hydration.P50, rep.Hydration.P95,
			rep.Fusion.P50, rep.Fusion.P95,
			rep.Unaccounted.P50, rep.Unaccounted.P95,
			rep.Total.P50, rep.Total.P95,
		)
	}

	// Write to JSON
	perfOutput := map[string]interface{}{
		"total_chunks_indexed": totalChunksCount,
		"insert_throughput":    throughput,
		"db_file_size_mb":      dbSizeMB,
		"reports":              allReports,
	}
	outBytes, _ := json.MarshalIndent(perfOutput, "", "  ")
	_ = os.WriteFile("testdata_perf_benchmark_02c.json", outBytes, 0644)
	t.Log("Saved benchmark results to testdata_perf_benchmark_02c.json")
}
