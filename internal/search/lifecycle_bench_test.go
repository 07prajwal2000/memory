package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"

	"memory/internal/llm"
)

// Helper to get current process RSS in MB on Windows/Unix
func getProcessRSSMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Sys) / (1024 * 1024)
}

// 1. Measure Population Cost and Cold Open at 10k, 39k, 200k
func TestBenchmark_PopulationCostAndColdOpen(t *testing.T) {
	if os.Getenv("MEM_RUN_LIFECYCLE_BENCH") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping lifecycle benchmark (set MEM_RUN_LIFECYCLE_BENCH=1 or MEM_BENCH=1 to run)")
	}

	embedder := newClusteredEmbedder(ModelDim, 64)
	scales := []struct {
		name  string
		count int
	}{
		{name: "10k", count: 10000},
		{name: "39k", count: 39000},
		{name: "200k", count: 200000},
	}

	t.Log("\n=================== BENCHMARK 1: POPULATION COST & COLD OPEN ===================")
	t.Log("| Scale | SQLite DB Size | RAM Population Time | CLI Cold Open+Search (SQLite Path) | Daemon Cold Open+Search (Sync RAM) |")
	t.Log("|---|---|---|---|---|")

	for _, sc := range scales {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, fmt.Sprintf("pop_%s.db", sc.name))

		// Populate database with sc.count chunks
		ixSeed, err := OpenIndex(dbPath, embedder, nil, WithRebuild())
		if err != nil {
			t.Fatal(err)
		}

		tx, _ := ixSeed.db.Begin()
		sChunk, _ := tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, 'global', 0, 'H', ?, 'th', 1, 10)")
		sFts, _ := tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, 'H', ?)")
		sVec, _ := tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, 'global', vec_int8(?))")
		sVecBit, _ := tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, 'global', vec_bit(?))")

		scopeTok := ScopeToken("global")
		dummyText := "Sample technical documentation content regarding distributed systems and search architecture."
		fvecs, _ := embedder.Embed(context.Background(), llm.KindDocument, []string{dummyText})
		i8Vec := Quantize(fvecs[0])
		bitVec := QuantizeBit(fvecs[0])

		for i := 1; i <= sc.count; i++ {
			sChunk.Exec(i, fmt.Sprintf("doc-%d", i), dummyText)
			sFts.Exec(i, scopeTok, dummyText)
			sVec.Exec(i, i8Vec)
			sVecBit.Exec(i, bitVec)
			if i%5000 == 0 {
				sChunk.Close()
				sFts.Close()
				sVec.Close()
				sVecBit.Close()
				tx.Commit()
				tx, _ = ixSeed.db.Begin()
				sChunk, _ = tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, 'global', 0, 'H', ?, 'th', 1, 10)")
				sFts, _ = tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, 'H', ?)")
				sVec, _ = tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, 'global', vec_int8(?))")
				sVecBit, _ = tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, 'global', vec_bit(?))")
			}
		}
		sChunk.Close()
		sFts.Close()
		sVec.Close()
		sVecBit.Close()
		tx.Commit()
		ixSeed.Close()

		fi, _ := os.Stat(dbPath)
		dbSizeMB := float64(fi.Size()) / (1024 * 1024)

		// Measure isolated RAM population time (reading BLOBs through WASM driver)
		rawDB, _ := sql.Open("sqlite3", fmt.Sprintf("file:%s?_txlock=immediate", dbPath))
		vsTest := newVecStore()
		tPopStart := time.Now()
		_ = vsTest.LoadFromDB(rawDB)
		popDuration := time.Since(tPopStart)
		rawDB.Close()

		// Measure CLI cold open + first search (WithResidentVectors(false))
		ctx := context.Background()
		tCliStart := time.Now()
		ixCli, err := OpenIndex(dbPath, embedder, nil, WithResidentVectors(false))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = ixCli.Search(ctx, Query{Text: "distributed systems search", Contexts: []string{"global"}, Limit: 10})
		cliColdDuration := time.Since(tCliStart)
		ixCli.Close()

		// Measure Daemon cold open + first search with synchronous population (WithResidentVectorsSync(true))
		tDaemonStart := time.Now()
		ixDaemon, err := OpenIndex(dbPath, embedder, nil, WithResidentVectors(true), WithResidentVectorsSync(true))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = ixDaemon.Search(ctx, Query{Text: "distributed systems search", Contexts: []string{"global"}, Limit: 10})
		daemonColdDuration := time.Since(tDaemonStart)
		ixDaemon.Close()

		t.Logf("| **%s** (%d chunks) | %.1f MB | %.2f ms | **%.2f ms** | **%.2f ms** |",
			sc.name, sc.count, dbSizeMB,
			float64(popDuration.Microseconds())/1000.0,
			float64(cliColdDuration.Microseconds())/1000.0,
			float64(daemonColdDuration.Microseconds())/1000.0,
		)
	}
}

// 2. Measure Scoping: 200k chunks across 20 contexts
func TestBenchmark_Scoping_20Contexts(t *testing.T) {
	if os.Getenv("MEM_RUN_LIFECYCLE_BENCH") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping lifecycle benchmark (set MEM_RUN_LIFECYCLE_BENCH=1 or MEM_BENCH=1 to run)")
	}

	vs := newVecStore()
	rnd := rand.New(rand.NewSource(99999))
	makeVec := func() ([]byte, []byte) {
		f := make([]float32, ModelDim)
		for i := 0; i < ModelDim; i++ {
			f[i] = float32(rnd.NormFloat64())
		}
		return Quantize(f), QuantizeBit(f)
	}

	const numContexts = 20
	const chunksPerCtx = 10000
	totalChunks := numContexts * chunksPerCtx

	t.Log("\n=================== BENCHMARK 2: SCOPING (200k across 20 Contexts) ===================")
	t.Logf("Populating %d chunks across %d contexts (%d chunks/context)...", totalChunks, numContexts, chunksPerCtx)

	for c := 0; c < numContexts; c++ {
		ctxName := "global"
		if c > 0 {
			ctxName = fmt.Sprintf("tenant-%02d", c)
		}
		for i := 0; i < chunksPerCtx; i++ {
			rid := int64(c*chunksPerCtx + i + 1)
			i8, bit := makeVec()
			vs.Upsert(rid, ctxName, i8, bit)
		}
	}

	qI8, qBit := makeVec()
	const iters = 100

	// Case A: Scoped query (global + tenant-01) -> 20,000 chunks in scope
	t0 := time.Now()
	for it := 0; it < iters; it++ {
		_ = vs.SearchTwoStage(qI8, qBit, []string{"global", "tenant-01"}, 50, 200)
	}
	scopedDuration := time.Since(t0) / iters

	// Case B: Unscoped query (all 20 contexts) -> 200,000 chunks in scope
	allContexts := make([]string, numContexts)
	allContexts[0] = "global"
	for c := 1; c < numContexts; c++ {
		allContexts[c] = fmt.Sprintf("tenant-%02d", c)
	}

	t1 := time.Now()
	for it := 0; it < iters; it++ {
		_ = vs.SearchTwoStage(qI8, qBit, allContexts, 50, 200)
	}
	allDuration := time.Since(t1) / iters

	speedup := float64(allDuration) / float64(scopedDuration)

	t.Log("| Query Scope | Chunks Traversed | TwoStage KNN Scan Latency | Speedup |")
	t.Log("|---|---|---|---|")
	t.Logf("| **Scoped (global + 1 tenant)** | **20,000** | **%.3f ms** | **%.1fx faster** |",
		float64(scopedDuration.Microseconds())/1000.0, speedup)
	t.Logf("| Unscoped (all 20 contexts) | 200,000 | %.3f ms | 1.0x baseline |",
		float64(allDuration.Microseconds())/1000.0)
}

// 3. Measure Real Process RSS at 200k chunks
func TestBenchmark_ProcessRSS_200k(t *testing.T) {
	if os.Getenv("MEM_RUN_LIFECYCLE_BENCH") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping lifecycle benchmark (set MEM_RUN_LIFECYCLE_BENCH=1 or MEM_BENCH=1 to run)")
	}

	runtime.GC()
	initialSysMB := getProcessRSSMB()

	vs := newVecStore()
	rnd := rand.New(rand.NewSource(11111))
	makeVec := func() ([]byte, []byte) {
		f := make([]float32, ModelDim)
		for i := 0; i < ModelDim; i++ {
			f[i] = float32(rnd.NormFloat64())
		}
		return Quantize(f), QuantizeBit(f)
	}

	const totalChunks = 200000
	for i := 1; i <= totalChunks; i++ {
		i8, bit := makeVec()
		vs.Upsert(int64(i), "global", i8, bit)
	}

	runtime.GC()
	loadedSysMB := getProcessRSSMB()
	deltaMB := loadedSysMB - initialSysMB

	t.Log("\n=================== BENCHMARK 3: REAL PROCESS MEMORY FOOTPRINT (200k Chunks) ===================")
	t.Logf("| Chunks in RAM | Baseline MemSys | Loaded MemSys | Delta RSS Added | Target Cap (400k) |")
	t.Logf("|---|---|---|---|---|")
	t.Logf("| **200,000** | %.2f MB | %.2f MB | **%.2f MB** (~%.1f bytes/vector) | 400,000 chunks (staying on SQLite if exceeded) |",
		initialSysMB, loadedSysMB, deltaMB, (deltaMB*1024*1024)/float64(totalChunks))
}

// 4. Realistic Corpus Latency Benchmark: Go stdlib source chunks + realistic queries
func TestBenchmark_RealisticCorpus_Latencies(t *testing.T) {
	if os.Getenv("MEM_RUN_LIFECYCLE_BENCH") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping lifecycle benchmark (set MEM_RUN_LIFECYCLE_BENCH=1 or MEM_BENCH=1 to run)")
	}

	t.Log("\n=================== BENCHMARK 4: REALISTIC CORPUS LATENCY (Go stdlib docs) ===================")

	// Collect real text chunks from GOROOT/src
	goRoot := os.Getenv("GOROOT")
	if goRoot == "" {
		goRoot = `D:\Softwares\Golang`
	}
	srcDir := filepath.Join(goRoot, "src")

	var realTexts []string
	chunkOpts := DefaultChunkOpts()

	filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if len(realTexts) >= 100000 {
			return nil
		}
		content, rErr := os.ReadFile(path)
		if rErr != nil || len(content) == 0 {
			return nil
		}
		chunks := Chunker(filepath.Base(path), string(content), chunkOpts)
		for _, c := range chunks {
			if len(c.Text) >= 50 {
				realTexts = append(realTexts, c.Text)
				if len(realTexts) >= 100000 {
					return nil
				}
			}
		}
		return nil
	})

	t.Logf("Collected %d unique real chunks from %s", len(realTexts), srcDir)
	if len(realTexts) < 10000 {
		t.Fatalf("Insufficient real chunks collected: %d", len(realTexts))
	}

	// Prepare 100 realistic queries
	realisticQueries := []string{
		// 2-3 word keyword queries
		"http server timeout", "mutex lock contention", "tls certificate handshake",
		"goroutine stack allocation", "atomic compare and swap", "context cancellation propagation",
		"garbage collection pause", "channel buffer capacity", "json unmarshal reflection",
		"hash map bucket collision", "crypto sha256 digest", "os file descriptor permissions",
		"net tcp keepalive", "syscall epoll wait", "unicode utf8 decoding",
		"io reader buffer copy", "time timer tick duration", "runtime memory allocator",
		"path filepath walkdir", "sync waitgroup counter",

		// 8-15 word natural language questions
		"how does the http server handle graceful shutdown when active connections exist",
		"why does mutex locking prevent data races in concurrent goroutine execution",
		"configuring tcp keepalive probes on linux socket connections for idle clients",
		"how does garbage collector mark and sweep allocate memory in go runtime",
		"implementing context cancellation with timeout in long running database queries",
		"handling json decoding errors with unknown fields and custom unmarshalers",
		"atomic pointer operations compared to mutex synchronization performance",
		"difference between buffered and unbuffered channels in synchronization patterns",
		"resolving dns domain names using coredns caching plugin and resolver",
		"managing file permissions and directory traversal with filepath walk",

		// 40-word pasted paragraph queries
		"The net/http package provides HTTP client and server implementations. A Server defines parameters for running an HTTP server. The zero value for Server is a valid configuration. The ListenAndServe method listens on the TCP network address Addr and then calls Serve.",
		"Package sync provides basic synchronization primitives such as mutual exclusion locks. Other than the Once and WaitGroup types, most are intended for use by low-level library routines. Higher-level synchronization is better done via channels.",
		"Package runtime contains operations that interact with Go's runtime system, such as functions to control goroutines. It also includes the low-level type information used by the reflect package and garbage collector execution statistics.",
	}

	// Populate database to 200k chunks across partitions
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "realistic_corpus_bench.db")
	embedder := newClusteredEmbedder(ModelDim, 64)

	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	ctx := context.Background()

	partitions := []struct {
		context string
		count   int
	}{
		{context: "ctx-10k", count: 10000},
		{context: "global-9k", count: 9000},
		{context: "ctx-30k", count: 30000},
		{context: "ctx-51k", count: 51000},
		{context: "ctx-extra-100k", count: 100000},
	}

	tx, _ := ix.db.Begin()
	sChunk, _ := tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, ?, 0, 'Doc', ?, 'th', 1, 10)")
	sFts, _ := tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, 'Doc', ?)")
	sVec, _ := tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
	sVecBit, _ := tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, ?, vec_bit(?))")

	chunkID := int64(1)
	for _, spec := range partitions {
		scopeTok := ScopeToken(spec.context)
		for i := 0; i < spec.count; i++ {
			text := realTexts[i%len(realTexts)]
			if i >= len(realTexts) {
				text = fmt.Sprintf("%s (replica %d)", text, i/len(realTexts))
			}
			fvecs, _ := embedder.Embed(ctx, llm.KindDocument, []string{text})
			i8Vec := Quantize(fvecs[0])
			bitVec := QuantizeBit(fvecs[0])

			sChunk.Exec(chunkID, fmt.Sprintf("doc-%s-%d", spec.context, i/5), spec.context, text)
			sFts.Exec(chunkID, scopeTok, text)
			sVec.Exec(chunkID, spec.context, i8Vec)
			sVecBit.Exec(chunkID, spec.context, bitVec)
			ix.vecStore.Upsert(chunkID, spec.context, i8Vec, bitVec)

			if chunkID%5000 == 0 {
				sChunk.Close()
				sFts.Close()
				sVec.Close()
				sVecBit.Close()
				tx.Commit()
				tx, _ = ix.db.Begin()
				sChunk, _ = tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, ?, 0, 'Doc', ?, 'th', 1, 10)")
				sFts, _ = tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, 'Doc', ?)")
				sVec, _ = tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
				sVecBit, _ = tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, ?, vec_bit(?))")
			}
			chunkID++
		}
	}
	sChunk.Close()
	sFts.Close()
	sVec.Close()
	sVecBit.Close()
	tx.Commit()

	ix.countsMu.Lock()
	ix.contextCounts = map[string]int{
		"ctx-10k":        10000,
		"global-9k":      9000,
		"ctx-30k":        30000,
		"ctx-51k":        51000,
		"ctx-extra-100k": 100000,
	}
	ix.countsMu.Unlock()

	scales := []struct {
		name     string
		contexts []string
	}{
		{name: "10k", contexts: []string{"ctx-10k"}},
		{name: "39k", contexts: []string{"global-9k", "ctx-30k"}},
		{name: "100k", contexts: []string{"global-9k", "ctx-30k", "ctx-10k", "ctx-51k"}},
		{name: "200k", contexts: []string{"global-9k", "ctx-30k", "ctx-10k", "ctx-51k", "ctx-extra-100k"}},
	}

	calcStats := func(vals []float64) (p50, p95 float64) {
		sort.Float64s(vals)
		return vals[int(float64(len(vals))*0.50)], vals[int(float64(len(vals))*0.95)]
	}

	t.Log("\n=================== REALISTIC CORPUS LATENCY TABLE (Cold vs Warm across Scales) ===================")
	t.Log("| Chunks in Scope | First Query (Cold) | EmbedQuery (p50 / p95) | Vec KNN (p50 / p95) | FTS5 (p50 / p95) | Hydration (p50 / p95) | Fusion (p50 / p95) | Unacc (p50 / p95) | **Total Pipeline (p50 / p95)** | Target (p95) |")
	t.Log("|---|---|---|---|---|---|---|---|---|---|")

	for _, sc := range scales {
		// Cold query (first search)
		tCold0 := time.Now()
		resCold, _ := ix.Search(ctx, Query{
			Text:       realisticQueries[0],
			Contexts:   sc.contexts,
			Limit:      10,
			VectorMode: VectorSearchTwoStage,
		})
		coldDuration := time.Since(tCold0)
		_ = resCold

		// Warm queries
		embedVals := make([]float64, len(realisticQueries))
		knnVals := make([]float64, len(realisticQueries))
		ftsVals := make([]float64, len(realisticQueries))
		hydrationVals := make([]float64, len(realisticQueries))
		fusionVals := make([]float64, len(realisticQueries))
		unaccVals := make([]float64, len(realisticQueries))
		totVals := make([]float64, len(realisticQueries))

		for qIdx, qText := range realisticQueries {
			res, err := ix.Search(ctx, Query{
				Text:       qText,
				Contexts:   sc.contexts,
				Limit:      10,
				VectorMode: VectorSearchTwoStage,
			})
			if err != nil {
				t.Fatalf("Search failed at %s: %v", sc.name, err)
			}
			embedVals[qIdx] = float64(res.Timings.EmbedQuery.Microseconds()) / 1000.0
			knnVals[qIdx] = float64(res.Timings.VecKNN.Microseconds()) / 1000.0
			ftsVals[qIdx] = float64(res.Timings.FTS.Microseconds()) / 1000.0
			hydrationVals[qIdx] = float64(res.Timings.Hydration.Microseconds()) / 1000.0
			fusionVals[qIdx] = float64(res.Timings.Fusion.Microseconds()) / 1000.0
			unaccVals[qIdx] = float64(res.Timings.Unaccounted.Microseconds()) / 1000.0
			totVals[qIdx] = float64(res.Timings.Total.Microseconds()) / 1000.0
		}

		emb50, emb95 := calcStats(embedVals)
		knn50, knn95 := calcStats(knnVals)
		fts50, fts95 := calcStats(ftsVals)
		hyd50, hyd95 := calcStats(hydrationVals)
		fus50, fus95 := calcStats(fusionVals)
		un50, un95 := calcStats(unaccVals)
		tot50, tot95 := calcStats(totVals)

		targetStr := "<= 100 ms"
		if sc.name == "200k" {
			targetStr = "<= 250 ms"
		} else if sc.name == "100k" {
			targetStr = "<= 150 ms"
		} else if sc.name == "10k" {
			targetStr = "<= 50 ms"
		}

		t.Logf("| **%s** | **%.2f ms** | %.2f / %.2f ms | %.2f / %.2f ms | %.2f / %.2f ms | %.2f / %.2f ms | %.2f / %.2f ms | %.2f / %.2f ms | **%.2f / %.2f ms** | %s |",
			sc.name, float64(coldDuration.Microseconds())/1000.0,
			emb50, emb95,
			knn50, knn95,
			fts50, fts95,
			hyd50, hyd95,
			fus50, fus95,
			un50, un95,
			tot50, tot95,
			targetStr,
		)
	}
}

// 5. Measure End-to-End UpsertMany on 2,000 Real Chunks
func TestBenchmark_UpsertMany_2000Chunks(t *testing.T) {
	if os.Getenv("MEM_RUN_LIFECYCLE_BENCH") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping lifecycle benchmark (set MEM_RUN_LIFECYCLE_BENCH=1 or MEM_BENCH=1 to run)")
	}

	llamaServer := `D:\ProgrammingFiles\OSS\memory\spike\bin\llama-server.exe`
	embedModel := `D:\ProgrammingFiles\OSS\memory\spike\models\bge-small-en-v1.5-q8_0.gguf`

	sidecar := llm.NewEmbedSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        embedModel,
		Parallel:     4,
		StartTimeout: 30 * time.Second,
		IdleTimeout:  10 * time.Minute,
	})
	defer sidecar.Close()
	embedder := sidecar.AsEmbedder()
	ctx := context.Background()

	// Warm up sidecar
	_, _ = embedder.Embed(ctx, llm.KindDocument, []string{"warmup"})

	// Prepare 1,000 documents (yielding 2,000 chunks)
	datasetPath := filepath.Join("testdata", "eval_dataset.json")
	dataBytes, _ := os.ReadFile(datasetPath)
	var ds evalDataset
	json.Unmarshal(dataBytes, &ds)

	docs := make([]Doc, 1000)
	for i := 0; i < 1000; i++ {
		c := ds.Corpus[i%len(ds.Corpus)]
		body := fmt.Sprintf("# %s\n\n%s\n\n## Section 2\n\n%s (expanded notes %d)", c.Heading, c.Text, c.Text, i)
		docs[i] = Doc{
			ID:      fmt.Sprintf("bench-doc-%d", i),
			Context: "global",
			Name:    fmt.Sprintf("Doc %d", i),
			Body:    body,
			Version: 1,
		}
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "upsert_many_bench.db")
	ix, err := OpenIndex(dbPath, embedder, nil,
		WithRebuild(),
		WithResidentVectors(true),
		WithResidentVectorsSync(true),
		WithUpsertBatchSize(32),
		WithUpsertConcurrency(4),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	t.Log("\n=================== BENCHMARK 5: END-TO-END UPSERT_MANY (2,000 chunks) ===================")
	tStart := time.Now()
	if err := ix.UpsertMany(ctx, docs); err != nil {
		t.Fatalf("UpsertMany failed: %v", err)
	}
	duration := time.Since(tStart)

	totalChunks := ix.ContextChunkCount("global")
	tps := float64(totalChunks) / duration.Seconds()

	t.Logf("UpsertMany indexed %d chunks across %d docs in %v", totalChunks, len(docs), duration)
	t.Logf("End-to-End Throughput: %.2f chunks/s (target >= 120 texts/s)", tps)
	t.Log("| Batch Size | Concurrency | Total Chunks | Wall Time | **End-to-End Throughput** | Baseline (02c) | Speedup |")
	t.Log("|---|---|---|---|---|---|---|")
	t.Logf("| 32 texts/req | 4 parallel workers | %d | %v | **%.2f chunks/s** | ~25 chunks/s | **%.1fx** |",
		totalChunks, duration, tps, tps/25.0)
}
