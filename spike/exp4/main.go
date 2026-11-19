package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
)

const (
	TotalChunks   = 200000
	GlobalChunks  = 30000
	NumContexts   = 20
	Dimensions    = 384
	QueryRuns     = 200
)

var vocab = []string{
	"memory", "system", "search", "vector", "database", "embedding", "context",
	"agent", "storage", "retrieval", "performance", "latency", "benchmark", "index",
	"sqlite", "rerank", "fusion", "relevance", "document", "passage", "scoring",
	"precision", "recall", "quantization", "cluster", "sharding", "concurrency",
	"transaction", "logging", "journal", "wal", "disk", "throughput", "table",
}

type Chunk struct {
	ID        int64
	Context   string
	Vector    []float32
	VectorI8  []byte
	VectorBit []byte
	Text      string
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x * x)
	}
	norm := float32(math.Sqrt(sum))
	if norm > 0 {
		for i := range v {
			v[i] /= norm
		}
	}
}

func quantizeInt8(v []float32) []byte {
	b := make([]byte, len(v))
	for i, f := range v {
		val := int(math.Round(float64(f * 127)))
		if val > 127 {
			val = 127
		} else if val < -128 {
			val = -128
		}
		b[i] = byte(int8(val))
	}
	return b
}

func quantizeBit(v []float32) []byte {
	numBytes := (len(v) + 7) / 8
	b := make([]byte, numBytes)
	for i, f := range v {
		if f > 0 {
			b[i/8] |= 1 << (i % 8)
		}
	}
	return b
}

func serializeFloat32(v []float32) []byte {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, v)
	return buf.Bytes()
}

func generateClusters(k, dim int) [][]float32 {
	r := rand.New(rand.NewSource(42))
	clusters := make([][]float32, k)
	for i := 0; i < k; i++ {
		c := make([]float32, dim)
		for d := 0; d < dim; d++ {
			c[d] = r.Float32()*2 - 1
		}
		normalize(c)
		clusters[i] = c
	}
	return clusters
}

func generateCorpus() []Chunk {
	fmt.Printf("Generating %d structured chunks across %d contexts...\n", TotalChunks, NumContexts)
	r := rand.New(rand.NewSource(1337))
	centroids := generateClusters(NumContexts, Dimensions)

	otherChunksPerCtx := (TotalChunks - GlobalChunks) / (NumContexts - 1)
	chunks := make([]Chunk, TotalChunks)

	var idx int64 = 0
	for cIdx := 0; cIdx < NumContexts; cIdx++ {
		var ctxName string
		var count int
		if cIdx == 0 {
			ctxName = "global"
			count = GlobalChunks
		} else {
			ctxName = fmt.Sprintf("ctx%d", cIdx)
			if cIdx == NumContexts-1 {
				count = TotalChunks - int(idx)
			} else {
				count = otherChunksPerCtx
			}
		}

		centroid := centroids[cIdx]
		for i := 0; i < count; i++ {
			vec := make([]float32, Dimensions)
			for d := 0; d < Dimensions; d++ {
				vec[d] = centroid[d] + (r.Float32()*2-1)*0.25
			}
			normalize(vec)

			w1 := vocab[r.Intn(len(vocab))]
			w2 := vocab[r.Intn(len(vocab))]
			w3 := vocab[r.Intn(len(vocab))]
			w4 := vocab[r.Intn(len(vocab))]
			text := fmt.Sprintf("Document %d in %s discusses %s %s with %s and %s techniques.", idx+1, ctxName, w1, w2, w3, w4)

			chunks[idx] = Chunk{
				ID:        idx + 1,
				Context:   ctxName,
				Vector:    vec,
				VectorI8:  quantizeInt8(vec),
				VectorBit: quantizeBit(vec),
				Text:      text,
			}
			idx++
		}
	}
	fmt.Printf("Corpus generated: %d chunks\n", len(chunks))
	return chunks
}

func calcP50P95(durs []time.Duration) (float64, float64) {
	if len(durs) == 0 {
		return 0, 0
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	p50 := float64(durs[len(durs)*50/100].Microseconds()) / 1000.0
	p95 := float64(durs[len(durs)*95/100].Microseconds()) / 1000.0
	return p50, p95
}

type QueryResultItem struct {
	ID       int64
	Distance float64
}

func main() {
	corpus := generateCorpus()

	// Generate 200 test queries
	r := rand.New(rand.NewSource(9999))
	centroids := generateClusters(NumContexts, Dimensions)
	queriesFloat := make([][]float32, QueryRuns)
	queriesI8 := make([][]byte, QueryRuns)
	queryKeywords := make([]string, QueryRuns)

	for q := 0; q < QueryRuns; q++ {
		cIdx := r.Intn(NumContexts)
		qVec := make([]float32, Dimensions)
		for d := 0; d < Dimensions; d++ {
			qVec[d] = centroids[cIdx][d] + (r.Float32()*2-1)*0.2
		}
		normalize(qVec)
		queriesFloat[q] = qVec
		queriesI8[q] = quantizeInt8(qVec)
		queryKeywords[q] = vocab[r.Intn(len(vocab))]
	}

	report := make(map[string]interface{})

	// ----------------------------------------------------
	// 1. Build and test INT8 Database
	// ----------------------------------------------------
	fmt.Println("\n========================================")
	fmt.Println("Building int8 Database (200k chunks)...")
	fmt.Println("========================================")
	dbFileI8 := "scale_int8.db"
	_ = os.Remove(dbFileI8)
	_ = os.Remove(dbFileI8 + "-wal")
	_ = os.Remove(dbFileI8 + "-shm")

	dbI8, err := sql.Open("sqlite3", dbFileI8)
	if err != nil {
		log.Fatalf("open dbI8: %v", err)
	}

	_, _ = dbI8.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000; PRAGMA synchronous = NORMAL;")
	_, err = dbI8.Exec("CREATE VIRTUAL TABLE vec_i8 USING vec0(context text partition key, embedding int8[384]);")
	if err != nil {
		log.Fatalf("create vec_i8: %v", err)
	}

	t0 := time.Now()
	batchSize := 10000
	for bStart := 0; bStart < TotalChunks; bStart += batchSize {
		bEnd := bStart + batchSize
		if bEnd > TotalChunks {
			bEnd = TotalChunks
		}
		tx, err := dbI8.Begin()
		if err != nil {
			log.Fatalf("begin tx: %v", err)
		}
		stmtI8, err := tx.Prepare("INSERT INTO vec_i8(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
		if err != nil {
			log.Fatalf("prepare insert i8: %v", err)
		}
		for i := bStart; i < bEnd; i++ {
			ch := corpus[i]
			if _, err := stmtI8.Exec(ch.ID, ch.Context, ch.VectorI8); err != nil {
				log.Fatalf("exec insert i8 at index %d: %v", i, err)
			}
		}
		_ = stmtI8.Close()
		if err := tx.Commit(); err != nil {
			log.Fatalf("commit tx i8: %v", err)
		}
	}
	durInsertI8 := time.Since(t0)
	tpsI8 := float64(TotalChunks) / durInsertI8.Seconds()
	fmt.Printf("Int8 200k inserted in %v (%.1f chunks/sec)\n", durInsertI8, tpsI8)

	_, _ = dbI8.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	fiI8, _ := os.Stat(dbFileI8)
	sizeI8MB := float64(fiI8.Size()) / (1024 * 1024)
	fmt.Printf("Int8 DB size: %.2f MB\n", sizeI8MB)

	// ----------------------------------------------------
	// 2. Build and test FLOAT32 Database
	// ----------------------------------------------------
	fmt.Println("\n========================================")
	fmt.Println("Building float32 Database (200k chunks)...")
	fmt.Println("========================================")
	dbFileF := "scale_float.db"
	_ = os.Remove(dbFileF)
	_ = os.Remove(dbFileF + "-wal")
	_ = os.Remove(dbFileF + "-shm")

	dbF, err := sql.Open("sqlite3", dbFileF)
	if err != nil {
		log.Fatalf("open dbF: %v", err)
	}

	_, _ = dbF.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000; PRAGMA synchronous = NORMAL;")
	_, err = dbF.Exec("CREATE VIRTUAL TABLE vec_f32 USING vec0(context text partition key, embedding float[384]);")
	if err != nil {
		log.Fatalf("create vec_f32: %v", err)
	}

	t0 = time.Now()
	batchSizeF := 5000
	for bStart := 0; bStart < TotalChunks; bStart += batchSizeF {
		bEnd := bStart + batchSizeF
		if bEnd > TotalChunks {
			bEnd = TotalChunks
		}
		txF, err := dbF.Begin()
		if err != nil {
			log.Fatalf("begin txF at %d: %v", bStart, err)
		}
		stmtF, err := txF.Prepare("INSERT INTO vec_f32(rowid, context, embedding) VALUES (?, ?, vec_f32(?))")
		if err != nil {
			log.Fatalf("prepare insert f32 at %d: %v", bStart, err)
		}
		for i := bStart; i < bEnd; i++ {
			ch := corpus[i]
			rawFloat := serializeFloat32(ch.Vector)
			if _, err := stmtF.Exec(ch.ID, ch.Context, rawFloat); err != nil {
				log.Fatalf("exec insert f32 at index %d: %v", i, err)
			}
		}
		_ = stmtF.Close()
		if err := txF.Commit(); err != nil {
			log.Fatalf("commit tx f32 at batch %d-%d: %v", bStart, bEnd, err)
		}
		if (bEnd % 20000) == 0 {
			_, _ = dbF.Exec("PRAGMA wal_checkpoint(PASSIVE);")
		}
	}
	durInsertF := time.Since(t0)
	tpsF := float64(TotalChunks) / durInsertF.Seconds()
	fmt.Printf("Float32 200k inserted in %v (%.1f chunks/sec)\n", durInsertF, tpsF)

	_, _ = dbF.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	fiF, _ := os.Stat(dbFileF)
	sizeFMB := float64(fiF.Size()) / (1024 * 1024)
	fmt.Printf("Float32 DB size: %.2f MB\n", sizeFMB)

	// ----------------------------------------------------
	// 3. Build BIT Database for size comparison
	// ----------------------------------------------------
	fmt.Println("\n========================================")
	fmt.Println("Building bit Database (200k chunks)...")
	fmt.Println("========================================")
	dbFileB := "scale_bit.db"
	_ = os.Remove(dbFileB)
	_ = os.Remove(dbFileB + "-wal")
	_ = os.Remove(dbFileB + "-shm")

	dbB, err := sql.Open("sqlite3", dbFileB)
	if err != nil {
		log.Fatalf("open dbB: %v", err)
	}

	_, _ = dbB.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000; PRAGMA synchronous = NORMAL;")
	_, err = dbB.Exec("CREATE VIRTUAL TABLE vec_b USING vec0(context text partition key, embedding bit[384]);")
	if err != nil {
		log.Fatalf("create vec_b: %v", err)
	}

	t0 = time.Now()
	for bStart := 0; bStart < TotalChunks; bStart += batchSize {
		bEnd := bStart + batchSize
		if bEnd > TotalChunks {
			bEnd = TotalChunks
		}
		txB, err := dbB.Begin()
		if err != nil {
			log.Fatalf("begin txB: %v", err)
		}
		stmtB, err := txB.Prepare("INSERT INTO vec_b(rowid, context, embedding) VALUES (?, ?, vec_bit(?))")
		if err != nil {
			log.Fatalf("prepare insert b: %v", err)
		}
		for i := bStart; i < bEnd; i++ {
			ch := corpus[i]
			if _, err := stmtB.Exec(ch.ID, ch.Context, ch.VectorBit); err != nil {
				log.Fatalf("exec insert bit: %v", err)
			}
		}
		_ = stmtB.Close()
		if err := txB.Commit(); err != nil {
			log.Fatalf("commit tx bit: %v", err)
		}
	}
	durInsertB := time.Since(t0)
	tpsB := float64(TotalChunks) / durInsertB.Seconds()
	fmt.Printf("Bit 200k inserted in %v (%.1f chunks/sec)\n", durInsertB, tpsB)

	_, _ = dbB.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	fiB, _ := os.Stat(dbFileB)
	sizeBMB := float64(fiB.Size()) / (1024 * 1024)
	fmt.Printf("Bit DB size: %.2f MB\n", sizeBMB)
	_ = dbB.Close()

	// ----------------------------------------------------
	// 4. Build FTS5 Table for BM25 latency
	// ----------------------------------------------------
	fmt.Println("\n========================================")
	fmt.Println("Building FTS5 Table (200k documents)...")
	fmt.Println("========================================")
	_, err = dbI8.Exec("CREATE VIRTUAL TABLE docs_fts USING fts5(context UNINDEXED, content);")
	if err != nil {
		log.Fatalf("create fts5: %v", err)
	}
	t0 = time.Now()
	for bStart := 0; bStart < TotalChunks; bStart += batchSize {
		bEnd := bStart + batchSize
		if bEnd > TotalChunks {
			bEnd = TotalChunks
		}
		txFts, _ := dbI8.Begin()
		stmtFts, _ := txFts.Prepare("INSERT INTO docs_fts(rowid, context, content) VALUES (?, ?, ?)")
		for i := bStart; i < bEnd; i++ {
			ch := corpus[i]
			_, _ = stmtFts.Exec(ch.ID, ch.Context, ch.Text)
		}
		_ = stmtFts.Close()
		_ = txFts.Commit()
	}
	durFtsBuild := time.Since(t0)
	fmt.Printf("FTS5 table populated in %v (%.1f docs/sec)\n", durFtsBuild, float64(TotalChunks)/durFtsBuild.Seconds())

	// Benchmark FTS5 BM25 top 50
	var ftsDurs []time.Duration
	stmtFtsQuery, _ := dbI8.Prepare("SELECT rowid, rank FROM docs_fts WHERE docs_fts MATCH ? ORDER BY rank LIMIT 50;")
	for q := 0; q < QueryRuns; q++ {
		kw := queryKeywords[q]
		start := time.Now()
		rows, err := stmtFtsQuery.Query(kw)
		if err != nil {
			log.Fatalf("fts query: %v", err)
		}
		for rows.Next() {
			var rID int64
			var rRank float64
			_ = rows.Scan(&rID, &rRank)
		}
		_ = rows.Close()
		ftsDurs = append(ftsDurs, time.Since(start))
	}
	_ = stmtFtsQuery.Close()
	ftsP50, ftsP95 := calcP50P95(ftsDurs)
	fmt.Printf("FTS5 BM25 top 50: p50=%.2f ms, p95=%.2f ms\n", ftsP50, ftsP95)

	// ----------------------------------------------------
	// 5. Benchmark KNN Queries
	// ----------------------------------------------------
	fmt.Println("\n========================================")
	fmt.Println("Benchmarking KNN Latencies (200 queries)...")
	fmt.Println("========================================")

	// Test 3a: partition = one context (ctx1)
	fmt.Println("--- Test 3a: partition = one context (ctx1, ~8.9k vectors) ---")
	// Int8
	stmtI8Single, _ := dbI8.Prepare("SELECT rowid, distance FROM vec_i8 WHERE embedding MATCH vec_int8(?) AND k = 50 AND context = 'ctx1';")
	var dursI8Single []time.Duration
	for q := 0; q < QueryRuns; q++ {
		start := time.Now()
		rows, err := stmtI8Single.Query(queriesI8[q])
		if err != nil {
			log.Fatalf("i8 single query: %v", err)
		}
		for rows.Next() {
			var rID int64
			var dist float64
			_ = rows.Scan(&rID, &dist)
		}
		_ = rows.Close()
		dursI8Single = append(dursI8Single, time.Since(start))
	}
	_ = stmtI8Single.Close()
	i8SingleP50, i8SingleP95 := calcP50P95(dursI8Single)
	fmt.Printf("Int8 (partition=one context): p50=%.2f ms, p95=%.2f ms\n", i8SingleP50, i8SingleP95)

	// Float32
	stmtFSingle, _ := dbF.Prepare("SELECT rowid, distance FROM vec_f32 WHERE embedding MATCH vec_f32(?) AND k = 50 AND context = 'ctx1';")
	var dursFSingle []time.Duration
	for q := 0; q < QueryRuns; q++ {
		qRaw := serializeFloat32(queriesFloat[q])
		start := time.Now()
		rows, err := stmtFSingle.Query(qRaw)
		if err != nil {
			log.Fatalf("f single query: %v", err)
		}
		for rows.Next() {
			var rID int64
			var dist float64
			_ = rows.Scan(&rID, &dist)
		}
		_ = rows.Close()
		dursFSingle = append(dursFSingle, time.Since(start))
	}
	_ = stmtFSingle.Close()
	fSingleP50, fSingleP95 := calcP50P95(dursFSingle)
	fmt.Printf("Float32 (partition=one context): p50=%.2f ms, p95=%.2f ms\n", fSingleP50, fSingleP95)

	// Test 3b: global (30k) + one context (8.9k) as two partitioned queries merged in Go (~39k vectors)
	fmt.Println("\n--- Test 3b: global + one context as two partitioned queries merged in Go (~38.9k vectors) ---")
	// Int8
	stmtI8Global, _ := dbI8.Prepare("SELECT rowid, distance FROM vec_i8 WHERE embedding MATCH vec_int8(?) AND k = 50 AND context = 'global';")
	stmtI8Ctx, _ := dbI8.Prepare("SELECT rowid, distance FROM vec_i8 WHERE embedding MATCH vec_int8(?) AND k = 50 AND context = 'ctx1';")
	var dursI8Merged []time.Duration
	for q := 0; q < QueryRuns; q++ {
		start := time.Now()
		items := make([]QueryResultItem, 0, 100)

		rows1, _ := stmtI8Global.Query(queriesI8[q])
		for rows1.Next() {
			var it QueryResultItem
			_ = rows1.Scan(&it.ID, &it.Distance)
			items = append(items, it)
		}
		_ = rows1.Close()

		rows2, _ := stmtI8Ctx.Query(queriesI8[q])
		for rows2.Next() {
			var it QueryResultItem
			_ = rows2.Scan(&it.ID, &it.Distance)
			items = append(items, it)
		}
		_ = rows2.Close()

		sort.Slice(items, func(i, j int) bool { return items[i].Distance < items[j].Distance })
		if len(items) > 50 {
			items = items[:50]
		}
		dursI8Merged = append(dursI8Merged, time.Since(start))
	}
	_ = stmtI8Global.Close()
	_ = stmtI8Ctx.Close()
	i8MergedP50, i8MergedP95 := calcP50P95(dursI8Merged)
	fmt.Printf("Int8 (global + ctx merged in Go): p50=%.2f ms, p95=%.2f ms\n", i8MergedP50, i8MergedP95)

	// Float32
	stmtFGlobal, _ := dbF.Prepare("SELECT rowid, distance FROM vec_f32 WHERE embedding MATCH vec_f32(?) AND k = 50 AND context = 'global';")
	stmtFCtx, _ := dbF.Prepare("SELECT rowid, distance FROM vec_f32 WHERE embedding MATCH vec_f32(?) AND k = 50 AND context = 'ctx1';")
	var dursFMerged []time.Duration
	for q := 0; q < QueryRuns; q++ {
		qRaw := serializeFloat32(queriesFloat[q])
		start := time.Now()
		items := make([]QueryResultItem, 0, 100)

		rows1, _ := stmtFGlobal.Query(qRaw)
		for rows1.Next() {
			var it QueryResultItem
			_ = rows1.Scan(&it.ID, &it.Distance)
			items = append(items, it)
		}
		_ = rows1.Close()

		rows2, _ := stmtFCtx.Query(qRaw)
		for rows2.Next() {
			var it QueryResultItem
			_ = rows2.Scan(&it.ID, &it.Distance)
			items = append(items, it)
		}
		_ = rows2.Close()

		sort.Slice(items, func(i, j int) bool { return items[i].Distance < items[j].Distance })
		if len(items) > 50 {
			items = items[:50]
		}
		dursFMerged = append(dursFMerged, time.Since(start))
	}
	_ = stmtFGlobal.Close()
	_ = stmtFCtx.Close()
	fMergedP50, fMergedP95 := calcP50P95(dursFMerged)
	fmt.Printf("Float32 (global + ctx merged in Go): p50=%.2f ms, p95=%.2f ms\n", fMergedP50, fMergedP95)

	// Test 3c: Single query with context IN ('global', 'ctx1')
	fmt.Println("\n--- Test 3c: Single query with context IN ('global', 'ctx1') ---")
	var inSupported bool
	var inErrorMsg string
	testRows, testErr := dbI8.Query("SELECT rowid, distance FROM vec_i8 WHERE embedding MATCH vec_int8(?) AND k = 50 AND context IN ('global', 'ctx1');", queriesI8[0])
	if testErr != nil {
		inSupported = false
		inErrorMsg = testErr.Error()
		fmt.Printf("vec0 context IN query error: %v\n", testErr)
	} else {
		testCount := 0
		for testRows.Next() {
			testCount++
		}
		_ = testRows.Close()
		inSupported = true
		fmt.Printf("vec0 context IN query SUCCEEDED! Rows returned: %d\n", testCount)
	}

	var i8InP50, i8InP95 float64
	var fInP50, fInP95 float64
	if inSupported {
		stmtI8In, _ := dbI8.Prepare("SELECT rowid, distance FROM vec_i8 WHERE embedding MATCH vec_int8(?) AND k = 50 AND context IN ('global', 'ctx1');")
		var dursI8In []time.Duration
		for q := 0; q < QueryRuns; q++ {
			start := time.Now()
			rows, _ := stmtI8In.Query(queriesI8[q])
			for rows.Next() {
			}
			_ = rows.Close()
			dursI8In = append(dursI8In, time.Since(start))
		}
		_ = stmtI8In.Close()
		i8InP50, i8InP95 = calcP50P95(dursI8In)
		fmt.Printf("Int8 (context IN query): p50=%.2f ms, p95=%.2f ms\n", i8InP50, i8InP95)

		stmtFIn, _ := dbF.Prepare("SELECT rowid, distance FROM vec_f32 WHERE embedding MATCH vec_f32(?) AND k = 50 AND context IN ('global', 'ctx1');")
		var dursFIn []time.Duration
		for q := 0; q < QueryRuns; q++ {
			qRaw := serializeFloat32(queriesFloat[q])
			start := time.Now()
			rows, _ := stmtFIn.Query(qRaw)
			for rows.Next() {
			}
			_ = rows.Close()
			dursFIn = append(dursFIn, time.Since(start))
		}
		_ = stmtFIn.Close()
		fInP50, fInP95 = calcP50P95(dursFIn)
		fmt.Printf("Float32 (context IN query): p50=%.2f ms, p95=%.2f ms\n", fInP50, fInP95)
	}

	_ = dbI8.Close()
	_ = dbF.Close()

	report["build_throughput"] = map[string]interface{}{
		"int8_tps":    tpsI8,
		"float32_tps": tpsF,
		"bit_tps":     tpsB,
	}
	report["db_size_mb"] = map[string]interface{}{
		"int8":    sizeI8MB,
		"float32": sizeFMB,
		"bit":     sizeBMB,
	}
	report["knn_latencies"] = map[string]interface{}{
		"int8_single_context_p50": i8SingleP50,
		"int8_single_context_p95": i8SingleP95,
		"float_single_context_p50": fSingleP50,
		"float_single_context_p95": fSingleP95,
		"int8_merged_p50": i8MergedP50,
		"int8_merged_p95": i8MergedP95,
		"float_merged_p50": fMergedP50,
		"float_merged_p95": fMergedP95,
		"in_supported": inSupported,
		"in_error": inErrorMsg,
		"int8_in_p50": i8InP50,
		"int8_in_p95": i8InP95,
		"float_in_p50": fInP50,
		"float_in_p95": fInP95,
	}
	report["fts5_latency"] = map[string]interface{}{
		"p50": ftsP50,
		"p95": ftsP95,
	}

	outBytes, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile("exp4_results.json", outBytes, 0644)
	fmt.Println("\nExperiment 4 completed! Results saved to spike/exp4_results.json")
}
