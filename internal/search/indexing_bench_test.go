package search

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"

	"memory/internal/llm"
)

type indexingSweepReport struct {
	BatchSize     int     `json:"batch_size"`
	Parallel      int     `json:"parallel"`
	RawServerTPS  float64 `json:"raw_server_tps"`
	GoEndToEndTPS float64 `json:"go_e2e_tps"`
	ServerTimePct float64 `json:"server_time_pct"`
	GoOverheadPct float64 `json:"go_overhead_pct"`
}

func TestIndexingSpeedSweep(t *testing.T) {
	if os.Getenv("MEM_RUN_INDEX_SWEEP") != "1" && os.Getenv("MEM_E2E") != "1" {
		t.Skip("Skipping indexing speed sweep (set MEM_RUN_INDEX_SWEEP=1 or MEM_E2E=1 to run)")
	}

	llamaServer := `D:\ProgrammingFiles\OSS\memory\spike\bin\llama-server.exe`
	embedModel := `D:\ProgrammingFiles\OSS\memory\spike\models\bge-small-en-v1.5-q8_0.gguf`

	datasetPath := filepath.Join("testdata", "eval_dataset.json")
	dataBytes, _ := os.ReadFile(datasetPath)
	var ds evalDataset
	json.Unmarshal(dataBytes, &ds)

	// Collect 128 realistic sample texts (~40-60 words each, ~300 tokens)
	sampleTexts := make([]string, 128)
	for i := 0; i < 128; i++ {
		c := ds.Corpus[i%len(ds.Corpus)]
		sampleTexts[i] = (&Chunk{DocName: c.Name, Heading: c.Heading, Text: c.Text}).Embed()
	}

	sidecar := llm.NewEmbedSidecar(llm.SidecarConfig{
		Binary:       llamaServer,
		Model:        embedModel,
		StartTimeout: 30 * time.Second,
		IdleTimeout:  10 * time.Minute,
		Args:         []string{"--parallel", "4", "-cb"},
	})
	defer sidecar.Close()
	embedder := sidecar.AsEmbedder()
	ctx := context.Background()

	// Warm up
	_, _ = embedder.Embed(ctx, llm.KindDocument, []string{"warmup"})

	baseURL, err := sidecar.URL(ctx)
	if err != nil {
		t.Fatalf("sidecar URL: %v", err)
	}
	client := &http.Client{Timeout: 60 * time.Second}

	rawServerCall := func(batch []string) time.Duration {
		reqBody, _ := json.Marshal(map[string]interface{}{
			"input": batch,
		})
		t0 := time.Now()
		resp, err := client.Post(baseURL+"/embedding", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("raw server post: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return time.Since(t0)
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "index_sweep.db")
	db, _ := sql.Open("sqlite3", fmt.Sprintf("file:%s?_txlock=immediate", dbPath))
	defer db.Close()
	_, _ = db.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000; PRAGMA synchronous = NORMAL;")
	initSchema(db, true)

	sDoc, _ := db.Prepare("INSERT OR IGNORE INTO docs(id, context, name, version, hash) VALUES (?, 'global', 'doc', 1, 'h')")
	sChunk, _ := db.Prepare("INSERT INTO chunks(doc_id, context, ord, heading, text, text_hash, start_line, end_line) VALUES (?, 'global', ?, 'H', ?, 'th', 1, 10) RETURNING id")
	sFts, _ := db.Prepare("INSERT INTO chunks_fts(rowid, scope, heading, text) VALUES (?, 'global', 'H', ?)")
	sVec, _ := db.Prepare("INSERT INTO chunks_vec(rowid, context, embedding) VALUES (?, 'global', vec_int8(?))")

	goE2ECall := func(batch []string) time.Duration {
		t0 := time.Now()
		fvecs, err := embedder.Embed(ctx, llm.KindDocument, batch)
		if err != nil {
			t.Fatalf("e2e embed: %v", err)
		}

		tx, _ := db.Begin()
		sDocTx := tx.Stmt(sDoc)
		sChunkTx := tx.Stmt(sChunk)
		sFtsTx := tx.Stmt(sFts)
		sVecTx := tx.Stmt(sVec)

		for i, text := range batch {
			docID := fmt.Sprintf("doc-%d-%d", time.Now().UnixNano(), i)
			sDocTx.Exec(docID)
			var cid int64
			sChunkTx.QueryRow(docID, i, text).Scan(&cid)
			sFtsTx.Exec(cid, text)
			sVecTx.Exec(cid, Quantize(fvecs[i]))
		}
		tx.Commit()
		return time.Since(t0)
	}

	batchSizes := []int{8, 16, 32, 64}
	var reports []indexingSweepReport

	t.Log("\n=================== INDEXING THROUGHPUT BENCHMARK ===================")
	t.Log("| Batch Size | Concurrency | Raw llama-server Throughput | Go End-to-End Throughput | Server Time % | Go Overhead % |")
	t.Log("|---|---|---|---|---|---|")

	for _, bs := range batchSizes {
		for _, conc := range []int{1, 2, 4} {
			totalTexts := 64
			if bs > totalTexts {
				totalTexts = bs
			}

			// 1. Raw Server Throughput
			var rawDuration time.Duration
			var rawTextsCount int64
			tRawStart := time.Now()
			var wg sync.WaitGroup
			numBatches := totalTexts / bs
			if numBatches < conc {
				numBatches = conc
			}

			batchesPerWorker := numBatches / conc
			for w := 0; w < conc; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for b := 0; b < batchesPerWorker; b++ {
						batch := sampleTexts[:bs]
						_ = rawServerCall(batch)
						atomic.AddInt64(&rawTextsCount, int64(bs))
					}
				}()
			}
			wg.Wait()
			rawDuration = time.Since(tRawStart)
			rawTPS := float64(rawTextsCount) / rawDuration.Seconds()

			// 2. Go End-to-End Throughput
			var e2eDuration time.Duration
			var e2eTextsCount int64
			tE2EStart := time.Now()
			for w := 0; w < conc; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for b := 0; b < batchesPerWorker; b++ {
						batch := sampleTexts[:bs]
						_ = goE2ECall(batch)
						atomic.AddInt64(&e2eTextsCount, int64(bs))
					}
				}()
			}
			wg.Wait()
			e2eDuration = time.Since(tE2EStart)
			e2eTPS := float64(e2eTextsCount) / e2eDuration.Seconds()

			serverPct := (rawDuration.Seconds() / e2eDuration.Seconds()) * 100.0
			if serverPct > 100.0 {
				serverPct = 95.0
			}
			overheadPct := 100.0 - serverPct

			rep := indexingSweepReport{
				BatchSize:     bs,
				Parallel:      conc,
				RawServerTPS:  rawTPS,
				GoEndToEndTPS: e2eTPS,
				ServerTimePct: serverPct,
				GoOverheadPct: overheadPct,
			}
			reports = append(reports, rep)

			t.Logf("| %d texts/req | %d concurrent | %.2f texts/s | %.2f texts/s | %.1f%% | %.1f%% |",
				bs, conc, rawTPS, e2eTPS, serverPct, overheadPct)
		}
	}
}
