package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"testing"
	"time"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"

	"memory/internal/llm"
)

func TestQuantize_Properties(t *testing.T) {
	// Zero vector
	zeroVec := make([]float32, ModelDim)
	qZero := Quantize(zeroVec)
	if len(qZero) != ModelDim {
		t.Fatalf("expected length %d, got %d", ModelDim, len(qZero))
	}
	for i, b := range qZero {
		if int8(b) != 0 {
			t.Errorf("expected 0 at index %d, got %d", i, int8(b))
		}
	}

	// Extreme saturation
	extreme := []float32{10.0, -10.0, 0.5, -0.5}
	qExtreme := Quantize(extreme)
	if int8(qExtreme[0]) != 127 {
		t.Errorf("expected saturated +127, got %d", int8(qExtreme[0]))
	}
	if int8(qExtreme[1]) != -128 {
		t.Errorf("expected saturated -128, got %d", int8(qExtreme[1]))
	}

	// Normal component roundtrip bound
	val := float32(0.05)
	qVal := Quantize([]float32{val})
	deq := Dequantize(qVal)[0]
	diff := math.Abs(float64(deq - val))
	maxErr := 1.0 / (2.0 * QuantScale) // quantization error bound
	if diff > maxErr+1e-5 {
		t.Errorf("roundtrip error %f exceeds theoretical bound %f", diff, maxErr)
	}
}

func TestQuantization_EmpiricalEvaluation(t *testing.T) {
	if os.Getenv("MEM_RUN_QUANT_EVAL") != "1" && os.Getenv("MEM_E2E") != "1" {
		t.Skip("Skipping empirical quantization benchmark (set MEM_RUN_QUANT_EVAL=1 or MEM_E2E=1 to run)")
	}

	// Check sqlite-vec metric support
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3: %v", err)
	}
	defer db.Close()

	var cosineSupported bool
	_, err = db.Exec("CREATE VIRTUAL TABLE t_cos USING vec0(embedding int8[384] distance_metric=cosine);")
	if err == nil {
		cosineSupported = true
		t.Log("sqlite-vec: int8[384] distance_metric=cosine is SUPPORTED")
	} else {
		t.Logf("sqlite-vec: int8[384] distance_metric=cosine is NOT supported: %v", err)
	}

	binaryPath := `D:\ProgrammingFiles\OSS\memory\spike\bin\llama-server.exe`
	modelPath := `D:\ProgrammingFiles\OSS\memory\spike\models\bge-small-en-v1.5-q8_0.gguf`

	if _, err := os.Stat(binaryPath); err != nil {
		t.Skipf("llama-server binary not found at %s", binaryPath)
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("model not found at %s", modelPath)
	}

	sidecar := llm.NewEmbedSidecar(llm.SidecarConfig{
		Binary:       binaryPath,
		Model:        modelPath,
		StartTimeout: 30 * time.Second,
		IdleTimeout:  5 * time.Minute,
	})
	defer sidecar.Close()

	embedder := sidecar.AsEmbedder(func(c *llm.HTTPEmbedder) {
		c.BatchSize = 32
	})

	ctx := context.Background()

	topics := []string{
		"database indexing WAL checkpointing sqlite-vec vector search",
		"OAuth2 JWT tokens HttpOnly cookies refresh token rotation",
		"gRPC HTTP2 multiplexing protocol buffers client side load balancing",
		"distributed cache stampede probabilistic early expiration redis memcached",
		"Go garbage collector tri color mark sweep write barrier memory pooling",
		"React virtual DOM fiber reconciliation state management hooks",
		"Kubernetes pod eviction horizontal pod autoscaler ingress controller",
		"Linux kernel cgroups epoll non-blocking IO TCP congestion control",
		"PostgreSQL MVCC vacuuming GiST and GIN indexes query planner",
		"Rust memory safety ownership borrow checker lifetimes RAII pattern",
		"Docker container overlayfs namespaces bridge networking security profiles",
		"Kafka partition consumer lag commit log replication factor zookeeper",
		"Elasticsearch inverted index BM25 scoring token filters analyzers",
		"TLS 1.3 handshake cryptographic cipher suites forward secrecy certificate authority",
		"GraphQL schema stitching federation resolver batching dataloader",
		"WebAssembly runtime sandboxing memory linear heap WASI system interface",
		"Git internals commit dag tree blob reflog packed objects",
		"Microservices saga pattern event sourcing CQRS distributed transactions",
		"Prometheus metrics scraping TSDB write ahead log alerting rules",
		"CORS preflight request Access-Control-Allow-Origin headers security",
	}

	t.Log("Generating 2,000 document passages and 50 queries...")
	docs := make([]string, 2000)
	for i := range docs {
		topic := topics[i%len(topics)]
		docs[i] = fmt.Sprintf("Engineering Note #%d: Architectural analysis of %s. Optimization requires evaluating latency percentiles, throughput boundaries, and fault tolerance under network partitions.", i, topic)
	}

	queries := make([]string, 50)
	for i := range queries {
		topic := topics[i%len(topics)]
		queries[i] = fmt.Sprintf("How to configure and optimize %s for production cloud services?", topic)
	}

	t.Log("Embedding 2,000 documents...")
	t0 := time.Now()
	docVecs, err := embedder.Embed(ctx, llm.KindDocument, docs)
	if err != nil {
		t.Fatalf("embed docs failed: %v", err)
	}
	t.Logf("Embedded 2,000 documents in %v", time.Since(t0))

	t.Log("Embedding 50 queries...")
	qryVecs, err := embedder.Embed(ctx, llm.KindQuery, queries)
	if err != nil {
		t.Fatalf("embed queries failed: %v", err)
	}

	// Analyze component distribution
	allAbs := make([]float64, 2000*ModelDim)
	idx := 0
	for _, v := range docVecs {
		for _, x := range v {
			allAbs[idx] = math.Abs(float64(x))
			idx++
		}
	}
	sort.Float64s(allAbs)

	p50 := allAbs[int(float64(len(allAbs))*0.50)]
	p90 := allAbs[int(float64(len(allAbs))*0.90)]
	p95 := allAbs[int(float64(len(allAbs))*0.95)]
	p99 := allAbs[int(float64(len(allAbs))*0.99)]
	p995 := allAbs[int(float64(len(allAbs))*0.995)]
	p999 := allAbs[int(float64(len(allAbs))*0.999)]
	maxVal := allAbs[len(allAbs)-1]

	t.Logf("Components (768k): p50=%.5f, p90=%.5f, p95=%.5f, p99=%.5f, p99.5=%.5f, p99.9=%.5f, max=%.5f",
		p50, p90, p95, p99, p995, p999, maxVal)

	// Ground truth: float32 dot products
	type neighbor struct {
		id    int
		score float64
	}

	gtTop10 := make([][]int, len(qryVecs))
	gtTop50 := make([][]int, len(qryVecs))

	for qIdx, qVec := range qryVecs {
		scores := make([]neighbor, len(docVecs))
		for dIdx, dVec := range docVecs {
			var dot float64
			for k := 0; k < ModelDim; k++ {
				dot += float64(qVec[k]) * float64(dVec[k])
			}
			scores[dIdx] = neighbor{id: dIdx, score: dot}
		}
		sort.Slice(scores, func(i, j int) bool {
			return scores[i].score > scores[j].score
		})

		top10 := make([]int, 10)
		for i := 0; i < 10; i++ {
			top10[i] = scores[i].id
		}
		gtTop10[qIdx] = top10

		top50 := make([]int, 50)
		for i := 0; i < 50; i++ {
			top50[i] = scores[i].id
		}
		gtTop50[qIdx] = top50
	}

	type quantFunc func(v []float32) []int8

	quantFixed := func(scale float64) quantFunc {
		return func(v []float32) []int8 {
			out := make([]int8, len(v))
			for i, x := range v {
				val := math.Round(float64(x) * scale)
				if val > 127 {
					val = 127
				} else if val < -128 {
					val = -128
				}
				out[i] = int8(val)
			}
			return out
		}
	}

	quantMaxAbs := func(v []float32) []int8 {
		var maxAbs float64
		for _, x := range v {
			a := math.Abs(float64(x))
			if a > maxAbs {
				maxAbs = a
			}
		}
		if maxAbs < 1e-9 {
			maxAbs = 1e-9
		}
		scale := 127.0 / maxAbs
		out := make([]int8, len(v))
		for i, x := range v {
			val := math.Round(float64(x) * scale)
			if val > 127 {
				val = 127
			} else if val < -128 {
				val = -128
			}
			out[i] = int8(val)
		}
		return out
	}

	scale999 := 127.0 / p999

	candidates := []struct {
		name string
		fn   quantFunc
	}{
		{"1. Naive round(v*127)", quantFixed(127.0)},
		{"2. Per-vector max-abs", quantMaxAbs},
		{"3a. Fixed scale 127/0.30 (423.3)", quantFixed(127.0 / 0.30)},
		{"3b. Fixed scale 127/0.25 (508.0)", quantFixed(127.0 / 0.25)},
		{fmt.Sprintf("3c. Fixed scale 127/p99.9 (%.1f)", scale999), quantFixed(scale999)},
		{"3d. Fixed scale 127/0.20 (635.0)", quantFixed(127.0 / 0.20)},
		{"3e. Fixed scale 127/0.18 (705.5)", quantFixed(127.0 / 0.18)},
		{"3f. Fixed scale 127/0.15 (846.7)", quantFixed(127.0 / 0.15)},
		{"3g. Fixed scale 127/0.12 (1058.3)", quantFixed(127.0 / 0.12)},
		{"3h. Fixed scale 127/0.10 (1270.0)", quantFixed(127.0 / 0.10)},
		{"3i. Fixed scale 127/0.08 (1587.5)", quantFixed(127.0 / 0.08)},
	}

	type resultRecord struct {
		Name      string  `json:"name"`
		Metric    string  `json:"metric"`
		Overlap10 float64 `json:"overlap10"`
		Recall50  float64 `json:"recall50"`
	}

	evalScheme := func(name string, fn quantFunc, metric string) resultRecord {
		qDocs := make([][]int8, len(docVecs))
		for i, d := range docVecs {
			qDocs[i] = fn(d)
		}
		qQrys := make([][]int8, len(qryVecs))
		for i, q := range qryVecs {
			qQrys[i] = fn(q)
		}

		var totalOverlap10, totalRecall50 float64

		for qIdx, qVec := range qQrys {
			scores := make([]neighbor, len(qDocs))
			for dIdx, dVec := range qDocs {
				if metric == "dot" || metric == "cosine" {
					var dot int64
					for k := 0; k < ModelDim; k++ {
						dot += int64(qVec[k]) * int64(dVec[k])
					}
					scores[dIdx] = neighbor{id: dIdx, score: float64(dot)}
				} else if metric == "l2" {
					var l2 int64
					for k := 0; k < ModelDim; k++ {
						diff := int64(qVec[k]) - int64(dVec[k])
						l2 += diff * diff
					}
					scores[dIdx] = neighbor{id: dIdx, score: float64(-l2)}
				}
			}
			sort.Slice(scores, func(i, j int) bool {
				return scores[i].score > scores[j].score
			})

			setGt10 := make(map[int]bool, 10)
			for _, id := range gtTop10[qIdx] {
				setGt10[id] = true
			}
			var hit10 int
			for i := 0; i < 10; i++ {
				if setGt10[scores[i].id] {
					hit10++
				}
			}
			totalOverlap10 += float64(hit10) / 10.0

			setGt50 := make(map[int]bool, 50)
			for _, id := range gtTop50[qIdx] {
				setGt50[id] = true
			}
			var hit50 int
			for i := 0; i < 50; i++ {
				if setGt50[scores[i].id] {
					hit50++
				}
			}
			totalRecall50 += float64(hit50) / 50.0
		}

		avgOverlap10 := (totalOverlap10 / float64(len(qryVecs))) * 100.0
		avgRecall50 := (totalRecall50 / float64(len(qryVecs))) * 100.0

		t.Logf("%-38s | metric=%-6s | Overlap@10: %6.2f%% | Recall@50: %6.2f%%",
			name, metric, avgOverlap10, avgRecall50)

		return resultRecord{
			Name:      name,
			Metric:    metric,
			Overlap10: math.Round(avgOverlap10*100) / 100,
			Recall50:  math.Round(avgRecall50*100) / 100,
		}
	}

	var results []resultRecord
	for _, cand := range candidates {
		results = append(results, evalScheme(cand.name, cand.fn, "dot"))
		results = append(results, evalScheme(cand.name, cand.fn, "l2"))
		results = append(results, evalScheme(cand.name, cand.fn, "cosine"))
	}

	// Write results to JSON
	jsonBytes, err := json.MarshalIndent(map[string]interface{}{
		"percentiles": map[string]float64{
			"p50":   p50,
			"p90":   p90,
			"p95":   p95,
			"p99":   p99,
			"p99.5": p995,
			"p99.9": p999,
			"max":   maxVal,
		},
		"metric_cosine_supported": cosineSupported,
		"results":                 results,
	}, "", "  ")
	if err == nil {
		_ = os.WriteFile("testdata_quant_results.json", jsonBytes, 0644)
	}

	// Log chosen scheme results
	var chosenOverlap float64
	for _, r := range results {
		if r.Name == "3e. Fixed scale 127/0.18 (705.5)" && r.Metric == "l2" {
			chosenOverlap = r.Overlap10
		}
	}
	t.Logf("Optimal scheme Overlap@10 is %.2f%% under L2", chosenOverlap)
}
