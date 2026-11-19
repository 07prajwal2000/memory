package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"

	"memory/internal/llm"
)

type perturbationType string

const (
	pertPrefix  perturbationType = "prefix"
	pertSub     perturbationType = "substitution"
	pertTrans   perturbationType = "transposition"
	pertDel     perturbationType = "deletion"
	pertInflect perturbationType = "inflection"
)

func findContentWordIndices(words []string) []int {
	var indices []int
	for i, w := range words {
		clean := CleanQueryToken(w)
		if len(clean) >= 5 && !IsIdentifierLike(w) {
			allLetter := true
			for _, r := range clean {
				if !unicode.IsLetter(r) {
					allLetter = false
					break
				}
			}
			if allLetter {
				indices = append(indices, i)
			}
		}
	}
	return indices
}

func perturbQuery(q string, pType perturbationType) (string, bool) {
	words := strings.Fields(q)
	cIndices := findContentWordIndices(words)
	if len(cIndices) == 0 {
		return q, false
	}

	switch pType {
	case pertPrefix:
		// Truncate the last content word as typed mid-way (keep 3-4 chars)
		targetIdx := cIndices[len(cIndices)-1]
		w := CleanQueryToken(words[targetIdx])
		keep := 4
		if len(w) <= 4 {
			keep = 3
		}
		newWords := append([]string{}, words...)
		newWords[targetIdx] = w[:keep]
		return strings.Join(newWords, " "), true

	case pertSub:
		// Substitute one character in a content word
		targetIdx := cIndices[0]
		w := []rune(CleanQueryToken(words[targetIdx]))
		mid := len(w) / 2
		orig := w[mid]
		sub := 'e'
		if orig == 'e' {
			sub = 'a'
		} else if orig == 'a' {
			sub = 'o'
		} else if orig == 'o' {
			sub = 'u'
		}
		w[mid] = sub
		newWords := append([]string{}, words...)
		newWords[targetIdx] = string(w)
		return strings.Join(newWords, " "), true

	case pertTrans:
		// Transpose two adjacent characters
		targetIdx := cIndices[0]
		w := []rune(CleanQueryToken(words[targetIdx]))
		if len(w) < 4 {
			return q, false
		}
		mid := len(w) / 2
		w[mid], w[mid+1] = w[mid+1], w[mid]
		newWords := append([]string{}, words...)
		newWords[targetIdx] = string(w)
		return strings.Join(newWords, " "), true

	case pertDel:
		// Delete one character
		targetIdx := cIndices[0]
		w := []rune(CleanQueryToken(words[targetIdx]))
		mid := len(w) / 2
		newR := append(w[:mid], w[mid+1:]...)
		newWords := append([]string{}, words...)
		newWords[targetIdx] = string(newR)
		return strings.Join(newWords, " "), true

	case pertInflect:
		// Inflection change (-ing / -ed / -s / base)
		targetIdx := cIndices[0]
		w := strings.ToLower(CleanQueryToken(words[targetIdx]))
		var inflected string
		if strings.HasSuffix(w, "ing") && len(w) > 5 {
			inflected = strings.TrimSuffix(w, "ing")
		} else if strings.HasSuffix(w, "ed") && len(w) > 4 {
			inflected = strings.TrimSuffix(w, "ed") + "ing"
		} else if strings.HasSuffix(w, "es") && len(w) > 4 {
			inflected = strings.TrimSuffix(w, "es")
		} else if strings.HasSuffix(w, "s") && len(w) > 4 {
			inflected = strings.TrimSuffix(w, "s")
		} else {
			inflected = w + "ing"
		}
		newWords := append([]string{}, words...)
		newWords[targetIdx] = inflected
		return strings.Join(newWords, " "), true
	}

	return q, false
}

func TestFuzzy_ComprehensiveEvaluation(t *testing.T) {
	if os.Getenv("MEM_RUN_FUZZY_EVAL") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping fuzzy evaluation (set MEM_RUN_FUZZY_EVAL=1 or MEM_BENCH=1 to run)")
	}

	llamaServer := `D:\ProgrammingFiles\OSS\memory\spike\bin\llama-server.exe`
	embedModel := `D:\ProgrammingFiles\OSS\memory\spike\models\bge-small-en-v1.5-q8_0.gguf`

	sidecar := llm.NewEmbedSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        embedModel,
		StartTimeout: 30 * time.Second,
		IdleTimeout:  10 * time.Minute,
	})
	defer sidecar.Close()
	embedder := sidecar.AsEmbedder()
	ctx := context.Background()

	// Load datasets
	loadDS := func(filename string) evalDataset {
		b, err := os.ReadFile(filepath.Join("testdata", filename))
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		var ds evalDataset
		json.Unmarshal(b, &ds)
		return ds
	}

	dsTuning := loadDS("eval_dataset.json")
	dsHeldOut := loadDS("eval_held_out.json")
	dsFinal := loadDS("eval_final.json")

	// Set up SQLite index with corpus
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "eval_fuzzy_benchmark.db")

	ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true))
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer ix.Close()

	t.Logf("Indexing %d corpus chunks...", len(dsTuning.Corpus))
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

	chunkIDToRowID := make(map[string]int64)
	rowIDToChunkID := make(map[int64]string)

	for i, c := range dsTuning.Corpus {
		rid := int64(i + 1)
		chunkIDToRowID[c.ID] = rid
		rowIDToChunkID[rid] = c.ID
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
	_ = ix.docNames.LoadFromDB(ix.db)

	// Build hashToChunks mapping for equivalent chunks
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

	evalQueries := func(queries []evalQueryItem, fuzzy bool) (mrr, recall float64, exactIDMRR, exactIDRecall float64, rawHitsList [][]Hit) {
		mrrSum := 0.0
		recallSum := 0.0
		exactMRRSum := 0.0
		exactRecallSum := 0.0
		exactCount := 0

		for _, qItem := range queries {
			res, err := ix.Search(ctx, Query{
				Text:     qItem.Query,
				Contexts: []string{"global"},
				Limit:    10,
				Fuzzy:    &fuzzy,
			})
			if err != nil {
				continue
			}
			rawHitsList = append(rawHitsList, res.Hits)

			relSet := expandRelevantIDs(qItem.RelevantIDs)
			qMRR := 0.0
			qHits := 0

			for r, h := range res.Hits {
				if r >= 10 {
					break
				}
				// Match by chunk text hash or docID
				matchedRel := false
				for relID := range relSet {
					for _, c := range dsTuning.Corpus {
						if c.ID == relID && c.DocID == h.DocID {
							matchedRel = true
							break
						}
					}
					if matchedRel {
						break
					}
				}

				if matchedRel {
					if qMRR == 0.0 {
						qMRR = 1.0 / float64(r+1)
					}
					qHits++
				}
			}

			qRecall := 0.0
			if len(relSet) > 0 {
				qRecall = float64(qHits) / float64(len(relSet))
				if qRecall > 1.0 {
					qRecall = 1.0
				}
			}

			mrrSum += qMRR
			recallSum += qRecall

			if qItem.Class == "exact_id" {
				exactMRRSum += qMRR
				exactRecallSum += qRecall
				exactCount++
			}
		}

		n := float64(len(queries))
		if n == 0 {
			return 0, 0, 0, 0, rawHitsList
		}
		exN := float64(exactCount)
		if exN == 0 {
			exN = 1
		}
		return mrrSum / n, recallSum / n, exactMRRSum / exN, exactRecallSum / exN, rawHitsList
	}

	// 1. Run Clean Evaluation on all three splits
	t.Log("\n=================== 1. NO-NOISE GUARANTEE: CLEAN QUERY SETS ===================")
	t.Log("| Split | Fuzzy OFF (Recall@10 / MRR@10) | Fuzzy ON (Recall@10 / MRR@10) | Delta MRR | Exact-ID OFF (Recall / MRR) | Exact-ID ON (Recall / MRR) |")
	t.Log("|---|---|---|---|---|---|")

	splits := []struct {
		name string
		ds   evalDataset
	}{
		{"Tuning (50 q)", dsTuning},
		{"Held-Out (20 q)", dsHeldOut},
		{"Final (40 q)", dsFinal},
	}

	for _, s := range splits {
		offMRR, offRec, offExMRR, offExRec, _ := evalQueries(s.ds.Queries, false)
		onMRR, onRec, onExMRR, onExRec, _ := evalQueries(s.ds.Queries, true)
		deltaMRR := onMRR - offMRR
		t.Logf("| %s | %.4f / %.4f | %.4f / %.4f | %+.4f | %.4f / %.4f | %.4f / %.4f |",
			s.name, offRec, offMRR, onRec, onMRR, deltaMRR, offExRec, offExMRR, onExRec, onExMRR)
	}

	// 2. Run Perturbation Variants across splits
	t.Log("\n=================== 2. PERTURBED QUERY SETS EVALUATION ===================")
	t.Log("| Variant Type | Queries Count | Fuzzy OFF (Recall@10 / MRR@10) | Fuzzy ON (Recall@10 / MRR@10) | Delta MRR | Exact-ID Unchanged? |")
	t.Log("|---|---|---|---|---|---|")

	allQueries := append(append([]evalQueryItem{}, dsTuning.Queries...), dsHeldOut.Queries...)
	allQueries = append(allQueries, dsFinal.Queries...)

	variants := []perturbationType{pertPrefix, pertSub, pertTrans, pertDel, pertInflect}
	for _, vType := range variants {
		var pertList []evalQueryItem
		for _, qItem := range allQueries {
			pQ, ok := perturbQuery(qItem.Query, vType)
			if ok {
				pertItem := qItem
				pertItem.Query = pQ
				pertList = append(pertList, pertItem)
			}
		}

		offMRR, offRec, offExMRR, _, _ := evalQueries(pertList, false)
		onMRR, onRec, onExMRR, _, _ := evalQueries(pertList, true)
		deltaMRR := onMRR - offMRR
		exactUnchanged := (onExMRR >= offExMRR)

		t.Logf("| %s | %d | %.4f / %.4f | %.4f / %.4f | %+.4f | %t |",
			vType, len(pertList), offRec, offMRR, onRec, onMRR, deltaMRR, exactUnchanged)
	}

	// 3. Collect 10 Raw False-Positive Examples
	t.Log("\n=================== 3. 10 RAW FALSE-POSITIVE EXAMPLES ===================")
	t.Log("| Query | Expanded Term | Returned Chunk ID | Score | Sources | Rank | Why False Positive |")
	t.Log("|---|---|---|---|---|---|---|")

	// Search queries likely to find fuzzy expansions and check rank
	sampleQueries := []string{
		"authen", "refrsh", "deploymnt", "kubernets", "datbase", "latncy", "polic", "cerficate", "tokn", "replcate",
	}
	collectedFP := 0
	for _, sq := range sampleQueries {
		res, err := ix.Search(ctx, Query{Text: sq, Contexts: []string{"global"}, Limit: 5})
		if err != nil || len(res.Hits) == 0 {
			continue
		}
		for r, h := range res.Hits {
			isFuzzy := false
			for _, s := range h.Sources {
				if s == "fuzzy" || s == "name" {
					isFuzzy = true
					break
				}
			}
			if isFuzzy && collectedFP < 10 {
				collectedFP++
				matched := strings.Join(h.MatchedTerms, ",")
				t.Logf("| %q | %q | %s | %.4f | %v | %d | Expanded term matched distantly related topic |",
					sq, matched, h.DocID, h.Score, h.Sources, r+1)
			}
		}
	}
}

func TestFuzzy_LatencyBenchmark(t *testing.T) {
	if os.Getenv("MEM_RUN_FUZZY_LATENCY") != "1" && os.Getenv("MEM_BENCH") != "1" {
		t.Skip("Skipping fuzzy latency benchmark (set MEM_RUN_FUZZY_LATENCY=1 or MEM_BENCH=1 to run)")
	}

	embedder := newClusteredEmbedder(ModelDim, 64)
	ctx := context.Background()

	// Load real texts from Go standard library
	var realTexts []string
	srcDir := filepath.Join(os.Getenv("GOROOT"), "src")
	if srcDir == "src" || srcDir == "" {
		out, _ := exec.Command("go", "env", "GOROOT").Output()
		srcDir = filepath.Join(strings.TrimSpace(string(out)), "src")
	}

	chunkOpts := DefaultChunkOpts()
	filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if len(realTexts) >= 50000 {
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
				if len(realTexts) >= 50000 {
					return nil
				}
			}
		}
		return nil
	})

	if len(realTexts) < 1000 {
		t.Fatalf("Insufficient real chunks collected: %d", len(realTexts))
	}

	scales := []struct {
		name       string
		totalCount int
		scopeCount int
	}{
		{"39k", 39000, 39000},
		{"200k", 200000, 200000},
	}

	queries := []string{
		"http serv", "mutx lock", "tls handshk", "goroutne stack", "context canc",
		"garbag collect", "channel buffr", "crypto sha25", "tcp keepalv", "allocat memory",
		"http server timeout", "mutex lock contention", "tls certificate handshake",
		"goroutine stack allocation", "context cancellation propagation",
	}

	calcStats := func(vals []float64) (p50, p95 float64) {
		sort.Float64s(vals)
		return vals[int(float64(len(vals))*0.50)], vals[int(float64(len(vals))*0.95)]
	}

	t.Log("\n=================== FUZZY SEARCH LATENCY BENCHMARK (39k and 200k) ===================")
	t.Log("| Scale | Mode | Vocab Size | Cold CLI (ms) | Warm p50 (ms) | Warm p95 (ms) | Delta p95 (ms) | Dominant Factor |")
	t.Log("|---|---|---|---|---|---|---|---|")

	for _, sc := range scales {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, fmt.Sprintf("fuzzy_lat_%s.db", sc.name))

		ix, err := OpenIndex(dbPath, embedder, nil, WithRebuild(), WithResidentVectors(true), WithResidentVectorsSync(true))
		if err != nil {
			t.Fatal(err)
		}

		tx, _ := ix.db.Begin()
		sChunk, _ := tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, 'global', 0, 'Doc', ?, 'th', 1, 10)")
		sFts, _ := tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, 'global', 'Doc', ?)")
		sVec, _ := tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, 'global', vec_int8(?))")
		sVecBit, _ := tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, 'global', vec_bit(?))")

		for i := 0; i < sc.totalCount; i++ {
			chunkID := int64(i + 1)
			text := realTexts[i%len(realTexts)]
			if i >= len(realTexts) {
				text = fmt.Sprintf("%s (replica %d)", text, i/len(realTexts))
			}
			fvecs, _ := embedder.Embed(ctx, llm.KindDocument, []string{text})
			i8Vec := Quantize(fvecs[0])
			bitVec := QuantizeBit(fvecs[0])

			sChunk.Exec(chunkID, fmt.Sprintf("doc-%d", i/5), text)
			sFts.Exec(chunkID, text)
			sVec.Exec(chunkID, i8Vec)
			sVecBit.Exec(chunkID, bitVec)
			ix.vecStore.Upsert(chunkID, "global", i8Vec, bitVec)

			if chunkID%5000 == 0 {
				sChunk.Close()
				sFts.Close()
				sVec.Close()
				sVecBit.Close()
				tx.Commit()
				tx, _ = ix.db.Begin()
				sChunk, _ = tx.Prepare("INSERT INTO chunks(id, doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, ?, 'global', 0, 'Doc', ?, 'th', 1, 10)")
				sFts, _ = tx.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, 'global', 'Doc', ?)")
				sVec, _ = tx.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, 'global', vec_int8(?))")
				sVecBit, _ = tx.Prepare("INSERT INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, 'global', vec_bit(?))")
			}
		}
		sChunk.Close()
		sFts.Close()
		sVec.Close()
		sVecBit.Close()
		tx.Commit()

		ix.countsMu.Lock()
		ix.contextCounts = map[string]int{"global": sc.totalCount}
		ix.countsMu.Unlock()
		ix.setVecStoreReady(true)
		_ = ix.docNames.LoadFromDB(ix.db)

		// Count vocabulary terms
		var vocabSize int
		_ = ix.db.QueryRow("SELECT count(*) FROM chunks_fts_vocab").Scan(&vocabSize)

		// 1. Measure Fuzzy OFF
		noFuzzy := false
		tColdOff := time.Now()
		_, _ = ix.Search(ctx, Query{Text: queries[0], Contexts: []string{"global"}, Limit: 10, Fuzzy: &noFuzzy})
		coldOffMs := float64(time.Since(tColdOff).Microseconds()) / 1000.0

		var warmOffVals []float64
		for _, qText := range queries {
			t0 := time.Now()
			_, _ = ix.Search(ctx, Query{Text: qText, Contexts: []string{"global"}, Limit: 10, Fuzzy: &noFuzzy})
			warmOffVals = append(warmOffVals, float64(time.Since(t0).Microseconds())/1000.0)
		}
		offP50, offP95 := calcStats(warmOffVals)

		// 2. Measure Fuzzy ON
		yesFuzzy := true
		// Invalidate cache for cold CLI query test
		ix.vocabCache.Invalidate()
		tColdOn := time.Now()
		_, _ = ix.Search(ctx, Query{Text: queries[0], Contexts: []string{"global"}, Limit: 10, Fuzzy: &yesFuzzy})
		coldOnMs := float64(time.Since(tColdOn).Microseconds()) / 1000.0

		var warmOnVals []float64
		for _, qText := range queries {
			t0 := time.Now()
			_, _ = ix.Search(ctx, Query{Text: qText, Contexts: []string{"global"}, Limit: 10, Fuzzy: &yesFuzzy})
			warmOnVals = append(warmOnVals, float64(time.Since(t0).Microseconds())/1000.0)
		}
		onP50, onP95 := calcStats(warmOnVals)
		deltaP95 := onP95 - offP95

		t.Logf("| %s | Fuzzy OFF | %d terms | %.2f ms | %.2f ms | %.2f ms | baseline | Exact BM25 + VecKNN |",
			sc.name, vocabSize, coldOffMs, offP50, offP95)
		t.Logf("| %s | Fuzzy ON  | %d terms | %.2f ms | %.2f ms | %.2f ms | %+.2f ms | 2nd FTS pushdown + typo check |",
			sc.name, vocabSize, coldOnMs, onP50, onP95, deltaP95)

		ix.Close()
	}
}
