package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
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

type evalCorpusChunk struct {
	ID      string `json:"id"`
	DocID   string `json:"doc_id"`
	Context string `json:"context"`
	Name    string `json:"name"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

type evalQueryItem struct {
	ID          string   `json:"id"`
	Class       string   `json:"class"`
	Query       string   `json:"query"`
	RelevantIDs []string `json:"relevant_ids"`
}

type evalDataset struct {
	Corpus  []evalCorpusChunk `json:"corpus"`
	Queries []evalQueryItem   `json:"queries"`
}

type queryResult struct {
	mrr10    float64
	recall10 float64
	latency  time.Duration
}

type accStats struct {
	mrrSum     float64
	recallSum  float64
	latencySum time.Duration
	count      int
}

func TestEvaluation_Harness(t *testing.T) {
	if os.Getenv("MEM_E2E") != "1" {
		t.Skip("Skipping E2E evaluation (set MEM_E2E=1 to run)")
	}

	llamaServer := os.Getenv("MEM_LLAMA_SERVER")
	if llamaServer == "" {
		llamaServer = `D:\ProgrammingFiles\OSS\memory\spike\bin\llama-server.exe`
	}
	embedModel := os.Getenv("MEM_EMBED_MODEL")
	if embedModel == "" {
		embedModel = `D:\ProgrammingFiles\OSS\memory\spike\models\bge-small-en-v1.5-q8_0.gguf`
	}
	jinaRerankModel := os.Getenv("MEM_RERANK_MODEL")
	if jinaRerankModel == "" {
		jinaRerankModel = `D:\ProgrammingFiles\OSS\memory\spike\models\jina-reranker-v1-tiny-en-Q8_0.gguf`
	}
	bgeRerankModel := `D:\ProgrammingFiles\OSS\memory\spike\models\bge-reranker-v2-m3-Q4_K_M.gguf`

	// Verify model files exist
	for _, f := range []string{llamaServer, embedModel, jinaRerankModel, bgeRerankModel} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("Required file not found: %s", f)
		}
	}

	// Load Tuning Split (80 queries)
	tuningPath := filepath.Join("testdata", "eval_dataset.json")
	tuningBytes, err := os.ReadFile(tuningPath)
	if err != nil {
		t.Fatalf("read tuning dataset: %v", err)
	}
	var dsTuning evalDataset
	if err := json.Unmarshal(tuningBytes, &dsTuning); err != nil {
		t.Fatalf("unmarshal tuning dataset: %v", err)
	}

	// Load Held-out Split (40 queries)
	heldOutPath := filepath.Join("testdata", "eval_held_out.json")
	heldOutBytes, err := os.ReadFile(heldOutPath)
	if err != nil {
		t.Fatalf("read held-out dataset: %v", err)
	}
	var dsHeldOut evalDataset
	if err := json.Unmarshal(heldOutBytes, &dsHeldOut); err != nil {
		t.Fatalf("unmarshal held-out dataset: %v", err)
	}

	t.Logf("Loaded datasets: Corpus=%d chunks, TuningSplit=%d queries, HeldOutSplit=%d queries",
		len(dsTuning.Corpus), len(dsTuning.Queries), len(dsHeldOut.Queries))

	// Start Embedder sidecar
	embedSidecar := llm.NewEmbedSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        embedModel,
		StartTimeout: 30 * time.Second,
		IdleTimeout:  15 * time.Minute,
	})
	defer embedSidecar.Close()
	embedder := embedSidecar.AsEmbedder()

	// Start Jina Reranker sidecar
	jinaSidecar := llm.NewRerankSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        jinaRerankModel,
		StartTimeout: 30 * time.Second,
		IdleTimeout:  15 * time.Minute,
	})
	defer jinaSidecar.Close()
	rerankerJina := jinaSidecar.AsReranker()

	// Start BGE-M3 Reranker sidecar
	bgeSidecar := llm.NewRerankSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        bgeRerankModel,
		StartTimeout: 45 * time.Second,
		IdleTimeout:  15 * time.Minute,
	})
	defer bgeSidecar.Close()
	rerankerBge := bgeSidecar.AsReranker()

	ctx := context.Background()

	// Setup Index database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "eval_comprehensive.db")
	dsn := fmt.Sprintf("file:%s?_txlock=immediate", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, _ = db.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000; PRAGMA synchronous = NORMAL;")
	if err := initSchema(db, true); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	// Also float32 vector table for exact f32 comparison
	_, err = db.Exec("CREATE VIRTUAL TABLE chunks_vec_f32 USING vec0(context text partition key, embedding float[384]);")
	if err != nil {
		t.Fatalf("create f32 vec table: %v", err)
	}

	// Embed all corpus chunks
	t.Log("Embedding corpus chunks with BGE-small...")
	corpusTexts := make([]string, len(dsTuning.Corpus))
	for i, c := range dsTuning.Corpus {
		ch := Chunk{
			DocName: c.Name,
			Heading: c.Heading,
			Text:    c.Text,
		}
		corpusTexts[i] = ch.Embed()
	}

	t0 := time.Now()
	corpusFVecs, err := embedder.Embed(ctx, llm.KindDocument, corpusTexts)
	if err != nil {
		t.Fatalf("embed corpus: %v", err)
	}
	t.Logf("Embedded %d chunks in %v (%.2f chunks/s)", len(corpusFVecs), time.Since(t0), float64(len(corpusFVecs))/time.Since(t0).Seconds())

	// Insert into DB
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmtChunk, _ := tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
	stmtFts, _ := tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, ?, ?)")
	stmtVecI8, _ := tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
	stmtVecBit, _ := tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, ?, vec_bit(?))")
	stmtVecF32, _ := tx.Prepare("INSERT INTO chunks_vec_f32(rowid, context, embedding) VALUES (?, ?, vec_f32(?))")

	rowIDToChunkID := make(map[int64]string)
	for i, c := range dsTuning.Corpus {
		rowid := int64(i + 1)
		rowIDToChunkID[rowid] = c.ID
		scopeTok := ScopeToken(c.Context)
		i8Bytes := Quantize(corpusFVecs[i])
		bitBytes := QuantizeBit(corpusFVecs[i])

		f32Bytes := make([]byte, len(corpusFVecs[i])*4)
		for d, val := range corpusFVecs[i] {
			bits := math.Float32bits(val)
			f32Bytes[d*4+0] = byte(bits)
			f32Bytes[d*4+1] = byte(bits >> 8)
			f32Bytes[d*4+2] = byte(bits >> 16)
			f32Bytes[d*4+3] = byte(bits >> 24)
		}

		_, _ = stmtChunk.Exec(rowid, c.DocID, c.Context, i, c.Heading, c.Text, Sha256Hex(c.Text), 1, 10)
		_, _ = stmtFts.Exec(rowid, scopeTok, c.Heading, c.Text)
		_, _ = stmtVecI8.Exec(rowid, c.Context, i8Bytes)
		_, _ = stmtVecBit.Exec(rowid, c.Context, bitBytes)
		_, _ = stmtVecF32.Exec(rowid, c.Context, f32Bytes)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit insert: %v", err)
	}
	stmtChunk.Close()
	stmtFts.Close()
	stmtVecI8.Close()
	stmtVecBit.Close()
	stmtVecF32.Close()

	// Embed queries for Tuning and Held-out splits
	embedQueries := func(items []evalQueryItem) [][]float32 {
		texts := make([]string, len(items))
		for i, q := range items {
			texts[i] = q.Query
		}
		vecs, err := embedder.Embed(ctx, llm.KindQuery, texts)
		if err != nil {
			t.Fatalf("embed queries: %v", err)
		}
		return vecs
	}

	t.Log("Embedding queries for tuning and held-out splits...")
	tuningQVecs := embedQueries(dsTuning.Queries)
	heldOutQVecs := embedQueries(dsHeldOut.Queries)

	// Systems to evaluate:
	systems := []string{
		"bm25",
		"vec_i8",
		"vec_f32",
		"hybrid_rrf_base", // Baseline unweighted RRF (k=60)
		"hybrid_weighted", // Tuned Weighted RRF (w_vec=1.0, w_bm25=0.10, k=10)
		"hybrid_boost",    // Vector order + BM25 boost
		"hybrid_twostage", // Two-Stage Vector (bit top-200 -> int8 top-50) + Weighted RRF
		"rerank_jina",     // Hybrid weighted + Jina reranker (top 30)
		"rerank_bgem3",    // Hybrid weighted + BGE-M3 reranker (top 30)
		"rerank_margin",   // Hybrid weighted + Jina reranker (top 10 only when top-1 margin < 0.05)
	}

	classes := []string{"paraphrase", "exact_id", "confusable", "multi_fact"}

	runSystem := func(qItem evalQueryItem, qVec []float32, system string) queryResult {
		relSet := make(map[string]bool)
		for _, rid := range qItem.RelevantIDs {
			relSet[rid] = true
		}

		tQueryStart := time.Now()
		var rankedRowIDs []int64

		switch system {
		case "bm25":
			sanitized := SanitizeFTS5(qItem.Query, []string{"global"})
			if sanitized != "" {
				rows, err := db.Query(`
					SELECT rowid
					FROM chunks_fts
					WHERE chunks_fts MATCH ?
					ORDER BY bm25(chunks_fts)
					LIMIT 10
				`, sanitized)
				if err == nil {
					for rows.Next() {
						var r int64
						if err := rows.Scan(&r); err == nil {
							rankedRowIDs = append(rankedRowIDs, r)
						}
					}
					rows.Close()
				}
			}

		case "vec_i8":
			qVecI8 := Quantize(qVec)
			rows, err := db.Query(`
				SELECT rowid
				FROM chunks_vec
				WHERE embedding MATCH vec_int8(?) AND k = 10 AND context = 'global'
			`, qVecI8)
			if err == nil {
				for rows.Next() {
					var r int64
					if err := rows.Scan(&r); err == nil {
						rankedRowIDs = append(rankedRowIDs, r)
					}
				}
				rows.Close()
			}

		case "vec_f32":
			f32Bits := make([]byte, len(qVec)*4)
			for d, val := range qVec {
				bits := math.Float32bits(val)
				f32Bits[d*4+0] = byte(bits)
				f32Bits[d*4+1] = byte(bits >> 8)
				f32Bits[d*4+2] = byte(bits >> 16)
				f32Bits[d*4+3] = byte(bits >> 24)
			}
			rows, err := db.Query(`
				SELECT rowid
				FROM chunks_vec_f32
				WHERE embedding MATCH vec_f32(?) AND k = 10 AND context = 'global'
			`, f32Bits)
			if err == nil {
				for rows.Next() {
					var r int64
					if err := rows.Scan(&r); err == nil {
						rankedRowIDs = append(rankedRowIDs, r)
					}
				}
				rows.Close()
			}

		case "hybrid_rrf_base", "hybrid_weighted", "hybrid_boost", "hybrid_twostage", "rerank_jina", "rerank_bgem3", "rerank_margin":
			// Leg 1: BM25 top 50
			bm25Ranks := make(map[int64]int)
			sanitized := SanitizeFTS5(qItem.Query, []string{"global"})
			if sanitized != "" {
				rows, err := db.Query(`
					SELECT rowid, bm25(chunks_fts)
					FROM chunks_fts
					WHERE chunks_fts MATCH ?
					ORDER BY bm25(chunks_fts)
					LIMIT 50
				`, sanitized)
				if err == nil {
					rank := 1
					for rows.Next() {
						var r int64
						var score float64
						if err := rows.Scan(&r, &score); err == nil {
							bm25Ranks[r] = rank
							rank++
						}
					}
					rows.Close()
				}
			}

			// Leg 2: Vector top 50
			vecRanks := make(map[int64]int)
			qVecI8 := Quantize(qVec)

			if system == "hybrid_twostage" {
				// Two-stage: bit top-200 -> int8 top-50
				qBit := QuantizeBit(qVec)
				rowsBit, err := db.Query(`
					SELECT rowid
					FROM chunks_vec_bit
					WHERE embedding MATCH vec_bit(?) AND k = 200 AND context = 'global'
				`, qBit)
				var candidateIDs []int64
				if err == nil {
					for rowsBit.Next() {
						var r int64
						if err := rowsBit.Scan(&r); err == nil {
							candidateIDs = append(candidateIDs, r)
						}
					}
					rowsBit.Close()
				}
				if len(candidateIDs) > 0 {
					placeholders := make([]string, len(candidateIDs))
					args := make([]interface{}, 1+len(candidateIDs))
					args[0] = qVecI8
					for i, cid := range candidateIDs {
						placeholders[i] = "?"
						args[1+i] = cid
					}
					qRescore := fmt.Sprintf(`
						SELECT rowid, distance
						FROM chunks_vec
						WHERE embedding MATCH vec_int8(?) AND k = 50 AND rowid IN (%s)
					`, strings.Join(placeholders, ","))
					rowsRescore, err := db.Query(qRescore, args...)
					if err == nil {
						rank := 1
						for rowsRescore.Next() {
							var r int64
							var d float64
							if err := rowsRescore.Scan(&r, &d); err == nil {
								vecRanks[r] = rank
								rank++
							}
						}
						rowsRescore.Close()
					}
				}
			} else {
				// Exact int8 top-50
				rows, err := db.Query(`
					SELECT rowid, distance
					FROM chunks_vec
					WHERE embedding MATCH vec_int8(?) AND k = 50 AND context = 'global'
				`, qVecI8)
				if err == nil {
					rank := 1
					for rows.Next() {
						var r int64
						var d float64
						if err := rows.Scan(&r, &d); err == nil {
							vecRanks[r] = rank
							rank++
						}
					}
					rows.Close()
				}
			}

			// Fusion
			var candidates []*candidateHit
			if system == "hybrid_rrf_base" {
				merged := WeightedRRF(vecRanks, bm25Ranks, 1.0, 1.0, 60.0)
				for _, h := range merged {
					candidates = append(candidates, h)
				}
			} else if system == "hybrid_boost" {
				merged := BoostFusion(vecRanks, bm25Ranks, 3, 0.25)
				for _, h := range merged {
					candidates = append(candidates, h)
				}
			} else {
				// Tuned Weighted RRF (w_vec=1.0, w_bm25=0.10, k=10)
				merged := WeightedRRF(vecRanks, bm25Ranks, 1.0, 0.10, 10.0)
				for _, h := range merged {
					candidates = append(candidates, h)
				}
			}

			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].score > candidates[j].score
			})

			// Reranking if applicable
			if len(candidates) > 0 && (system == "rerank_jina" || system == "rerank_bgem3" || system == "rerank_margin") {
				rerankerToUse := rerankerJina
				if system == "rerank_bgem3" {
					rerankerToUse = rerankerBge
				}

				limitK := 30
				if system == "rerank_margin" {
					limitK = 10
				}
				if len(candidates) < limitK {
					limitK = len(candidates)
				}

				shouldRerank := true
				if system == "rerank_margin" && len(candidates) >= 2 {
					margin := candidates[0].score - candidates[1].score
					if margin > 0.05 {
						shouldRerank = false // Top 1 hit is clear winner; skip rerank
					}
				}

				if shouldRerank && limitK > 0 {
					docsToRerank := make([]string, limitK)
					for i := 0; i < limitK; i++ {
						cid := rowIDToChunkID[candidates[i].chunkID]
						for _, ch := range dsTuning.Corpus {
							if ch.ID == cid {
								docsToRerank[i] = ch.Text
								break
							}
						}
					}

					scores, err := rerankerToUse.Rerank(ctx, qItem.Query, docsToRerank)
					if err == nil && len(scores) == limitK {
						for i := 0; i < limitK; i++ {
							candidates[i].score = scores[i]
						}
						sort.Slice(candidates[:limitK], func(i, j int) bool {
							return candidates[i].score > candidates[j].score
						})
					}
				}
			}

			for _, c := range candidates {
				rankedRowIDs = append(rankedRowIDs, c.chunkID)
			}
		}

		mrr := 0.0
		recall := 0.0
		limit := 10
		if len(rankedRowIDs) < limit {
			limit = len(rankedRowIDs)
		}

		for r := 0; r < limit; r++ {
			cID := rowIDToChunkID[rankedRowIDs[r]]
			if relSet[cID] {
				if mrr == 0.0 {
					mrr = 1.0 / float64(r+1)
				}
				recall = 1.0
			}
		}

		return queryResult{
			mrr10:    mrr,
			recall10: recall,
			latency:  time.Since(tQueryStart),
		}
	}

	evaluateSplit := func(splitName string, queries []evalQueryItem, qVecs [][]float32) map[string]map[string]*accStats {
		results := make(map[string]map[string]*accStats)
		for _, cl := range classes {
			results[cl] = make(map[string]*accStats)
			for _, s := range systems {
				results[cl][s] = &accStats{}
			}
		}
		results["OVERALL"] = make(map[string]*accStats)
		for _, s := range systems {
			results["OVERALL"][s] = &accStats{}
		}

		for i, q := range queries {
			for _, s := range systems {
				res := runSystem(q, qVecs[i], s)
				results[q.Class][s].mrrSum += res.mrr10
				results[q.Class][s].recallSum += res.recall10
				results[q.Class][s].latencySum += res.latency
				results[q.Class][s].count++

				results["OVERALL"][s].mrrSum += res.mrr10
				results["OVERALL"][s].recallSum += res.recall10
				results["OVERALL"][s].latencySum += res.latency
				results["OVERALL"][s].count++
			}
		}
		return results
	}

	t.Log("=== Running Evaluation on Tuning Split (80 queries) ===")
	tuningResults := evaluateSplit("tuning", dsTuning.Queries, tuningQVecs)

	t.Log("=== Running Evaluation on Held-out Split (40 queries) ===")
	heldOutResults := evaluateSplit("held_out", dsHeldOut.Queries, heldOutQVecs)

	printSplitTable := func(name string, res map[string]map[string]*accStats) {
		t.Logf("\n=================== %s EVALUATION REPORT ===================", strings.ToUpper(name))
		t.Log("| Query Class | Metric | BM25 | Vec int8 | Vec f32 | Hybrid RRF Base | Hybrid Weighted | Hybrid Boost | Two-Stage Hybrid | Rerank Jina | Rerank BGE-M3 | Rerank Margin |")
		t.Log("|---|---|---|---|---|---|---|---|---|---|---|---|")

		classList := append(classes, "OVERALL")
		for _, cl := range classList {
			mrrRow := fmt.Sprintf("| **%s** | MRR@10", cl)
			recRow := fmt.Sprintf("| | Recall@10")
			latRow := fmt.Sprintf("| | Latency")

			for _, s := range systems {
				st := res[cl][s]
				cnt := float64(st.count)
				if cnt == 0 {
					cnt = 1
				}
				mrr := st.mrrSum / cnt
				rec := st.recallSum / cnt
				avgLatMs := float64(st.latencySum.Milliseconds()) / cnt

				mrrRow += fmt.Sprintf(" | %.4f", mrr)
				recRow += fmt.Sprintf(" | %.4f", rec)
				latRow += fmt.Sprintf(" | %.1fms", avgLatMs)
			}
			mrrRow += " |"
			recRow += " |"
			latRow += " |"

			t.Log(mrrRow)
			t.Log(recRow)
			t.Log(latRow)
		}
	}

	printSplitTable("Tuning Split (80 queries)", tuningResults)
	printSplitTable("Held-Out Split (40 queries)", heldOutResults)

	// Save all metrics to JSON
	dumpMap := map[string]interface{}{
		"tuning":   tuningResults,
		"held_out": heldOutResults,
	}
	outBytes, _ := json.MarshalIndent(dumpMap, "", "  ")
	_ = os.WriteFile("testdata_eval_results_02c.json", outBytes, 0644)
	t.Log("Saved results to testdata_eval_results_02c.json")
}

func TestEvaluation_02c2_FinalSplit(t *testing.T) {
	if os.Getenv("MEM_E2E") != "1" && os.Getenv("MEM_RUN_FINAL_EVAL") != "1" {
		t.Skip("Skipping final split evaluation (set MEM_RUN_FINAL_EVAL=1 or MEM_E2E=1 to run)")
	}

	llamaServer := os.Getenv("MEM_LLAMA_SERVER")
	if llamaServer == "" {
		llamaServer = `D:\ProgrammingFiles\OSS\memory\spike\bin\llama-server.exe`
	}
	embedModel := os.Getenv("MEM_EMBED_MODEL")
	if embedModel == "" {
		embedModel = `D:\ProgrammingFiles\OSS\memory\spike\models\bge-small-en-v1.5-q8_0.gguf`
	}

	datasetPath := filepath.Join("testdata", "eval_dataset.json")
	dataBytes, _ := os.ReadFile(datasetPath)
	var dsTuning evalDataset
	json.Unmarshal(dataBytes, &dsTuning)

	heldOutPath := filepath.Join("testdata", "eval_held_out.json")
	heldOutBytes, _ := os.ReadFile(heldOutPath)
	var dsHeldOut evalDataset
	json.Unmarshal(heldOutBytes, &dsHeldOut)

	finalPath := filepath.Join("testdata", "eval_final.json")
	finalBytes, _ := os.ReadFile(finalPath)
	var dsFinal evalDataset
	json.Unmarshal(finalBytes, &dsFinal)

	sidecar := llm.NewEmbedSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        embedModel,
		StartTimeout: 30 * time.Second,
		IdleTimeout:  10 * time.Minute,
	})
	defer sidecar.Close()
	embedder := sidecar.AsEmbedder()
	ctx := context.Background()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "eval_final_02c2.db")

	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	defer ix.Close()

	t.Logf("Indexing %d corpus chunks into Index...", len(dsTuning.Corpus))
	corpusTexts := make([]string, len(dsTuning.Corpus))
	for i, c := range dsTuning.Corpus {
		corpusTexts[i] = (&Chunk{DocName: c.Name, Heading: c.Heading, Text: c.Text}).Embed()
	}
	fvecs, err := embedder.Embed(ctx, llm.KindDocument, corpusTexts)
	if err != nil {
		t.Fatalf("embed corpus: %v", err)
	}

	tx, _ := ix.db.Begin()
	sDoc, _ := tx.Prepare("INSERT OR IGNORE INTO docs(id, context, name, version, hash) VALUES (?, ?, ?, 1, 'h')")
	sChunk, _ := tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
	sFts, _ := tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, ?, ?, ?)")
	sVec, _ := tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
	sBit, _ := tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, ?, vec_bit(?))")

	rowToChunkID := make(map[int64]string)
	for i, c := range dsTuning.Corpus {
		rid := int64(i + 1)
		rowToChunkID[rid] = c.ID
		i8 := Quantize(fvecs[i])
		bit := QuantizeBit(fvecs[i])
		sDoc.Exec(c.DocID, c.Context, c.Name)
		sChunk.Exec(rid, c.DocID, c.Context, i, c.Heading, c.Text, Sha256Hex(c.Text), 1, 10)
		sFts.Exec(rid, ScopeToken(c.Context), c.Heading, c.Text)
		sVec.Exec(rid, c.Context, i8)
		sBit.Exec(rid, c.Context, bit)
		ix.vecStore.Upsert(rid, c.Context, i8, bit)
	}
	sDoc.Close()
	sChunk.Close()
	sFts.Close()
	sVec.Close()
	sBit.Close()
	tx.Commit()

	ix.countsMu.Lock()
	ix.contextCounts = map[string]int{"global": len(dsTuning.Corpus)}
	ix.countsMu.Unlock()
	ix.setVecStoreReady(true)

	// Build textHash to equivalent chunkIDs
	hashToChunks := make(map[string][]string)
	chunkToHash := make(map[string]string)
	for _, c := range dsTuning.Corpus {
		th := Sha256Hex(c.Text)
		hashToChunks[th] = append(hashToChunks[th], c.ID)
		chunkToHash[c.ID] = th
	}

	expandRelevantIDs := func(ids []string) map[string]bool {
		relSet := make(map[string]bool)
		for _, id := range ids {
			relSet[id] = true
			if th, ok := chunkToHash[id]; ok {
				for _, equivalentID := range hashToChunks[th] {
					relSet[equivalentID] = true
				}
			}
		}
		return relSet
	}

	evalSystem := func(name string, queries []evalQueryItem, searchFn func(qItem evalQueryItem) []int64) (overall float64, perClass map[string]float64) {
		classSums := make(map[string]float64)
		classCounts := make(map[string]int)
		overallSum := 0.0

		for _, q := range queries {
			relSet := expandRelevantIDs(q.RelevantIDs)

			hits := searchFn(q)
			mrr := 0.0
			limit := 10
			if len(hits) < limit {
				limit = len(hits)
			}
			for r := 0; r < limit; r++ {
				cID := rowToChunkID[hits[r]]
				if relSet[cID] {
					mrr = 1.0 / float64(r+1)
					break
				}
			}
			classSums[q.Class] += mrr
			classCounts[q.Class]++
			overallSum += mrr
		}

		perClass = make(map[string]float64)
		for cl, sum := range classSums {
			cnt := classCounts[cl]
			if cnt > 0 {
				perClass[cl] = sum / float64(cnt)
			}
		}
		overall = overallSum / float64(len(queries))
		return overall, perClass
	}

	// 1. BM25 search
	searchBM25 := func(qItem evalQueryItem) []int64 {
		san := SanitizeFTS5(qItem.Query, []string{"global"})
		if san == "" {
			return nil
		}
		rList, err := ix.db.Query("SELECT rowid FROM chunks_fts WHERE chunks_fts MATCH ? ORDER BY bm25(chunks_fts) LIMIT 50", san)
		if err != nil {
			return nil
		}
		defer rList.Close()
		var out []int64
		for rList.Next() {
			var rid int64
			rList.Scan(&rid)
			out = append(out, rid)
		}
		return out
	}

	// 2. Vec int8 search
	searchVecInt8 := func(qItem evalQueryItem) []int64 {
		fvecs, err := embedder.Embed(ctx, llm.KindQuery, []string{qItem.Query})
		if err != nil || len(fvecs) == 0 {
			return nil
		}
		hits := ix.vecStore.SearchExact(Quantize(fvecs[0]), []string{"global"}, 50)
		out := make([]int64, len(hits))
		for i, h := range hits {
			out[i] = h.rowid
		}
		return out
	}

	// 3. Old Weighted RRF (w_bm25=0.10)
	searchWeightedRRF := func(qItem evalQueryItem) []int64 {
		fvecs, _ := embedder.Embed(ctx, llm.KindQuery, []string{qItem.Query})
		vHits := ix.vecStore.SearchExact(Quantize(fvecs[0]), []string{"global"}, 50)
		vecRanks := make(map[int64]int)
		for r, h := range vHits {
			vecRanks[h.rowid] = r + 1
		}

		san := SanitizeFTS5(qItem.Query, []string{"global"})
		bmRanks := make(map[int64]int)
		if san != "" {
			rows, err := ix.db.Query("SELECT rowid FROM chunks_fts WHERE chunks_fts MATCH ? ORDER BY bm25(chunks_fts) LIMIT 50", san)
			if err == nil {
				rank := 1
				for rows.Next() {
					var rid int64
					rows.Scan(&rid)
					bmRanks[rid] = rank
					rank++
				}
				rows.Close()
			}
		}

		merged := WeightedRRF(vecRanks, bmRanks, 1.0, 0.10, 10.0)
		var list []*candidateHit
		for _, m := range merged {
			list = append(list, m)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
		out := make([]int64, len(list))
		for i, c := range list {
			out[i] = c.chunkID
		}
		return out
	}

	// 4. Default Search (Confidence-Aware Fusion with in-RAM Two-Stage)
	searchDefault := func(qItem evalQueryItem) []int64 {
		fvecs, _ := embedder.Embed(ctx, llm.KindQuery, []string{qItem.Query})
		vHits := ix.vecStore.SearchTwoStage(Quantize(fvecs[0]), QuantizeBit(fvecs[0]), []string{"global"}, 50, 200)
		vecRanks := make(map[int64]int)
		for r, h := range vHits {
			vecRanks[h.rowid] = r + 1
		}

		san := SanitizeFTS5(qItem.Query, []string{"global"})
		bmRanks := make(map[int64]int)
		bmScores := make(map[int64]float64)
		if san != "" {
			rows, err := ix.db.Query("SELECT rowid, bm25(chunks_fts) FROM chunks_fts WHERE chunks_fts MATCH ? ORDER BY bm25(chunks_fts) LIMIT 50", san)
			if err == nil {
				rank := 1
				for rows.Next() {
					var rid int64
					var s float64
					rows.Scan(&rid, &s)
					bmRanks[rid] = rank
					bmScores[rid] = s
					rank++
				}
				rows.Close()
			}
		}

		merged := ConfidenceRRF(vecRanks, bmRanks, bmScores, qItem.Query, ix.boostAmount, ix.lowWeight)
		var list []*candidateHit
		for _, m := range merged {
			list = append(list, m)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
		out := make([]int64, len(list))
		for i, c := range list {
			out[i] = c.chunkID
		}
		return out
	}

	runEvaluationReport := func(splitName string, queries []evalQueryItem) {
		bmTot, bmClass := evalSystem("BM25", queries, searchBM25)
		vecTot, vecClass := evalSystem("Vec int8", queries, searchVecInt8)
		wTot, wClass := evalSystem("Weighted RRF (02c)", queries, searchWeightedRRF)
		defTot, defClass := evalSystem("Confidence RRF (02c2 Default)", queries, searchDefault)

		t.Logf("\n=================== %s EVALUATION REPORT (%d queries) ===================", strings.ToUpper(splitName), len(queries))
		t.Log("| Query Class | BM25 Alone | Vec int8 Alone | Weighted RRF (02c) | **Confidence RRF (Default)** | Target (Max BM/Vec) | Success Criteria |")
		t.Log("|---|---|---|---|---|---|---|")

		classes := []string{"paraphrase", "exact_id", "confusable", "multi_fact"}
		allPass := true
		for _, cl := range classes {
			better := bmClass[cl]
			if vecClass[cl] > better {
				better = vecClass[cl]
			}
			conf := defClass[cl]
			delta := conf - better
			passStr := "PASS"
			if conf < better-0.03001 {
				passStr = fmt.Sprintf("FAIL (delta=%.4f)", delta)
				allPass = false
			}
			t.Logf("| **%s** | %.4f | %.4f | %.4f | **%.4f** | %.4f | %s |",
				cl, bmClass[cl], vecClass[cl], wClass[cl], conf, better, passStr)
		}

		betterOverall := bmTot
		if vecTot > betterOverall {
			betterOverall = vecTot
		}
		totDelta := defTot - betterOverall
		overallPass := "PASS"
		if defTot < betterOverall-0.01001 {
			overallPass = fmt.Sprintf("FAIL (delta=%.4f)", totDelta)
			allPass = false
		}
		t.Logf("| **OVERALL MRR@10** | %.4f | %.4f | %.4f | **%.4f** | %.4f | %s |",
			bmTot, vecTot, wTot, defTot, betterOverall, overallPass)

		t.Logf("[%s Verdict] All Criteria Satisfied: %v", splitName, allPass)
	}

	t.Log("Evaluating Tuning Split (80 queries)...")
	runEvaluationReport("Tuning Split", dsTuning.Queries)

	t.Log("Evaluating Held-Out Split (40 queries)...")
	runEvaluationReport("Held-Out Split", dsHeldOut.Queries)

	// Print Held-out failing exact_id and multi_fact queries under old Weighted RRF
	t.Log("\n=================== HELD-OUT FAILING QUERIES UNDER WEIGHTED RRF (02c) ===================")
	t.Log("| Class | Query | BM25 Rank | Vec Rank | Weighted RRF Rank | **Confidence RRF Rank** |")
	t.Log("|---|---|---|---|---|---|")
	for _, q := range dsHeldOut.Queries {
		if q.Class != "exact_id" && q.Class != "multi_fact" {
			continue
		}
		relSet := expandRelevantIDs(q.RelevantIDs)

		findRank := func(hits []int64) int {
			for r, h := range hits {
				if r >= 10 {
					break
				}
				cID := rowToChunkID[h]
				if relSet[cID] {
					return r + 1
				}
			}
			return 0
		}

		bmR := findRank(searchBM25(q))
		vecR := findRank(searchVecInt8(q))
		wR := findRank(searchWeightedRRF(q))
		confR := findRank(searchDefault(q))

		if wR != 1 || bmR == 1 {
			bmStr := fmt.Sprintf("%d", bmR)
			if bmR == 0 {
				bmStr = ">10"
			}
			vecStr := fmt.Sprintf("%d", vecR)
			if vecR == 0 {
				vecStr = ">10"
			}
			wStr := fmt.Sprintf("%d", wR)
			if wR == 0 {
				wStr = ">10"
			}
			confStr := fmt.Sprintf("%d", confR)
			if confR == 0 {
				confStr = ">10"
			}
			t.Logf("| %s | `%s` | %s | %s | %s | **%s** |", q.Class, q.Query, bmStr, vecStr, wStr, confStr)
		}
	}

	// 5-Fold Cross Validation over 120 queries (80 tuning + 40 held-out)
	t.Log("\n=================== 5-FOLD CROSS VALIDATION (120 queries: 80 tuning + 40 held-out) ===================")
	t.Log("| Fold | BM25 MRR@10 | Vec int8 MRR@10 | Weighted RRF MRR@10 | **Confidence RRF MRR@10** |")
	t.Log("|---|---|---|---|---|")

	all120 := append([]evalQueryItem{}, dsTuning.Queries...)
	all120 = append(all120, dsHeldOut.Queries...)

	foldSize := len(all120) / 5 // 24 queries per fold
	var sumBM, sumVec, sumW, sumConf float64

	for f := 0; f < 5; f++ {
		startIdx := f * foldSize
		endIdx := startIdx + foldSize
		testQueries := all120[startIdx:endIdx]

		bmO, _ := evalSystem("bm25", testQueries, searchBM25)
		vecO, _ := evalSystem("vec", testQueries, searchVecInt8)
		wO, _ := evalSystem("weighted", testQueries, searchWeightedRRF)
		confO, _ := evalSystem("conf", testQueries, searchDefault)

		sumBM += bmO
		sumVec += vecO
		sumW += wO
		sumConf += confO

		t.Logf("| Fold %d | %.4f | %.4f | %.4f | **%.4f** |", f+1, bmO, vecO, wO, confO)
	}
	t.Logf("| **Mean Test MRR** | **%.4f** | **%.4f** | **%.4f** | **%.4f** |",
		sumBM/5.0, sumVec/5.0, sumW/5.0, sumConf/5.0)

	t.Log("\nEvaluating FINAL EVALUATION Split (40 queries)...")
	runEvaluationReport("FINAL EVALUATION Split", dsFinal.Queries)
}

