package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"spike/eval/internal/bm25"
	"spike/eval/internal/client"
	"spike/eval/internal/metrics"
	"spike/eval/internal/runner"
)

func findPath(rel string) string {
	candidates := []string{
		rel,
		filepath.Join("..", rel),
		filepath.Join(".", strings.TrimPrefix(rel, "spike/")),
		filepath.Join("..", strings.TrimPrefix(rel, "spike/")),
	}
	cwd, err := os.Getwd()
	if err == nil {
		dir := cwd
		for i := 0; i < 5; i++ {
			// Check if this dir is repo root (has internal/ and spike/)
			if _, err := os.Stat(filepath.Join(dir, "internal")); err == nil {
				candidates = append(candidates, filepath.Join(dir, rel))
				candidates = append(candidates, filepath.Join(dir, "spike", strings.TrimPrefix(rel, "spike/")))
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	return rel
}

type ModelEntry struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Quant       string  `json:"quant"`
	Dims        int     `json:"dims"`
	Path        string  `json:"path"`
	File        string  `json:"file"`
	SHA256      string  `json:"sha256"`
	SizeMB      float64 `json:"size_mb"`
	ContextLen  int     `json:"context_len"`
	PrefixQuery string  `json:"prefix_query"`
	PrefixDoc   string  `json:"prefix_doc"`
	Pooling     string  `json:"pooling"`
}

type ModelResult struct {
	ModelID         string                                 `json:"model_id"`
	Name            string                                 `json:"name"`
	Quant           string                                 `json:"quant"`
	Dims            int                                    `json:"dims"`
	SizeMB          float64                                `json:"size_mb"`
	Fits100MB       bool                                   `json:"fits_100mb"`
	ThroughputTps   float64                                `json:"throughput_tps"`
	LatencyMs       float64                                `json:"latency_ms"`
	OptimalScale    float64                                `json:"optimal_scale_s"`
	Int8Overlap10   float64                                `json:"int8_overlap10"`
	Int8DeltaMRR    float64                                `json:"int8_delta_mrr"`
	Int8DeltaNDCG   float64                                `json:"int8_delta_ndcg"`
	MacroNDCG10     float64                                `json:"macro_ndcg10"`
	DatasetResults  map[string]map[string]runner.EvalResult `json:"dataset_results"` // dataset -> mode -> result
	BootstrapVsBase map[string]metrics.BootstrapCI         `json:"bootstrap_vs_base"` // dataset -> CI
}

func main() {
	runVerifyProduct := flag.Bool("verify-product", false, "Run cross-check against internal/search/testdata")
	flagPort := flag.Int("port", 8199, "llama-server port")
	flagModelFilter := flag.String("models", "", "Comma-separated model IDs to run (empty = all)")
	flagDatasetFilter := flag.String("datasets", "", "Comma-separated datasets to run (empty = all 4)")
	flagRunLevers := flag.Bool("levers", false, "Run Part C pipeline levers")
	flagRunSLM := flag.Bool("slm", false, "Run Part D small language model enrichment")
	flagOutput := flag.String("output", "spike/eval/eval_results.json", "Output JSON results file")
	flag.Parse()

	evaluator, err := runner.NewEvaluator(findPath("spike/eval/cache"))
	if err != nil {
		fmt.Printf("Error creating evaluator: %v\n", err)
		os.Exit(1)
	}

	// 1. Part A.5 Cross-check against product
	if *runVerifyProduct {
		runHarnessCrossCheck(evaluator, *flagPort)
		return
	}

	// Load manifest
	manifestData, err := os.ReadFile(findPath("spike/eval/models_manifest.json"))
	if err != nil {
		fmt.Printf("Error reading models_manifest.json: %v\n", err)
		os.Exit(1)
	}
	var models []ModelEntry
	if err := json.Unmarshal(manifestData, &models); err != nil {
		fmt.Printf("Error unmarshaling models_manifest.json: %v\n", err)
		os.Exit(1)
	}

	// Load datasets
	datasetNames := []string{"scifact", "nfcorpus", "fiqa_sub", "memlike"}
	if *flagDatasetFilter != "" {
		datasetNames = strings.Split(*flagDatasetFilter, ",")
	}

	datasets := make(map[string]*runner.Dataset)
	bm25Indices := make(map[string]*bm25.Index)

	for _, name := range datasetNames {
		p := findPath(fmt.Sprintf("spike/eval/data/%s.json", name))
		ds, err := runner.LoadDataset(p)
		if err != nil {
			fmt.Printf("Warning: failed to load dataset %s from %s: %v\n", name, p, err)
			continue
		}
		datasets[name] = ds
		bm25Indices[name] = runner.BuildBM25Index(ds, nil)
		fmt.Printf("Loaded dataset %s: %d corpus docs, %d test queries\n", name, len(ds.Corpus), len(ds.Queries))
	}

	// Optional user-queries dataset
	userQueriesPath := findPath("spike/eval/data/user-queries.json")
	if _, err := os.Stat(userQueriesPath); err == nil {
		userDS, err := runner.LoadDataset(userQueriesPath)
		if err == nil {
			datasets["user_queries"] = userDS
			bm25Indices["user_queries"] = runner.BuildBM25Index(userDS, nil)
			datasetNames = append(datasetNames, "user_queries")
			fmt.Printf("Loaded optional user-queries dataset: %d docs, %d queries\n", len(userDS.Corpus), len(userDS.Queries))
		}
	}

	filterMap := make(map[string]bool)
	if *flagModelFilter != "" {
		for _, m := range strings.Split(*flagModelFilter, ",") {
			filterMap[strings.TrimSpace(m)] = true
		}
	}

	var allResults []ModelResult
	var baselineResult *ModelResult

	currentPort := *flagPort

	for _, m := range models {
		if len(filterMap) > 0 && !filterMap[m.ID] && !filterMap[m.Name] {
			continue
		}

		fmt.Printf("\n=======================================================\n")
		fmt.Printf("Evaluating Model: %s (%s, %d-d, %.1f MB)\n", m.ID, m.Quant, m.Dims, m.SizeMB)
		fmt.Printf("=======================================================\n")

		spec := runner.ModelSpec{
			ID:          m.ID,
			Name:        m.Name,
			Quant:       m.Quant,
			Dims:        m.Dims,
			Path:        findPath(m.Path),
			SHA256:      m.SHA256,
			PrefixQuery: m.PrefixQuery,
			PrefixDoc:   m.PrefixDoc,
			Pooling:     m.Pooling,
			SizeMB:      m.SizeMB,
		}

		// Start llama-server
		server, err := client.StartLlamaServer(client.ServerOptions{
			BinaryPath: findPath("spike/bin/llama-server.exe"),
			ModelPath:  spec.Path,
			Port:       currentPort,
			Embedding:  true,
			Pooling:    spec.Pooling,
			Parallel:   2,
			BatchSize:  2048,
			UbatchSize: 2048,
		})
		currentPort++
		if err != nil {
			fmt.Printf("Error starting llama-server for %s: %v\n", m.ID, err)
			continue
		}

		// Measure latency on 20-word query
		sampleQuery := "Why was the SQLite WebAssembly library pinned to an older version instead of latest in our storage engine?"
		latMs, _ := server.MeasureLatency(spec.PrefixQuery+sampleQuery, 5)

		// Measure throughput on sample texts (~200 tokens each)
		var throughputSample []string
		for _, ds := range datasets {
			if ds != nil && len(ds.Corpus) > 0 {
				for _, d := range ds.Corpus[:min(32, len(ds.Corpus))] {
					throughputSample = append(throughputSample, spec.PrefixDoc+runner.TruncateToMaxWords(d.Text, 200))
				}
				break
			}
		}
		tps, tErr := server.MeasureThroughput(throughputSample, 16, 2)
		if tErr != nil {
			fmt.Printf("Warning: throughput measurement for %s failed: %v\n", m.ID, tErr)
		}
		fmt.Printf("Latency: %.2f ms | Throughput: %.1f texts/s\n", latMs, tps)

		res := ModelResult{
			ModelID:        m.ID,
			Name:           m.Name,
			Quant:          m.Quant,
			Dims:           m.Dims,
			SizeMB:         m.SizeMB,
			Fits100MB:      m.SizeMB <= 100.0,
			ThroughputTps:  tps,
			LatencyMs:      latMs,
			DatasetResults: make(map[string]map[string]runner.EvalResult),
			BootstrapVsBase: make(map[string]metrics.BootstrapCI),
		}

		var ndcgSum float64
		var ndcgCount float64

		for _, dName := range datasetNames {
			ds := datasets[dName]
			if ds == nil {
				continue
			}

			pe, err := evaluator.PrepareCorpusEmbeddings(ds, spec, server, nil)
			if err != nil {
				fmt.Printf("Error embedding %s on %s: %v\n", dName, m.ID, err)
				continue
			}

			// If nomic, we also test Matryoshka 384 and 256 on memlike!
			modes := []string{"vec_f32", "bm25", "hybrid_confidence"}
			dsRes := make(map[string]runner.EvalResult)

			for _, mode := range modes {
				evalRes := runner.Evaluate(ds, pe, bm25Indices[dName], mode, 0, false, 0)
				dsRes[mode] = evalRes
			}

			// Int8 evaluation on memlike
			if dName == "memlike" {
				// Derive scale S
				scaleS := deriveScaleS(pe.CorpusVecs)
				res.OptimalScale = scaleS

				// Int8 eval
				i8Res := runner.Evaluate(ds, pe, bm25Indices[dName], "vec_i8", 0, true, scaleS)
				dsRes["vec_i8"] = i8Res

				// Overlap@10 on 50 sample queries
				overlap10 := computeInt8Overlap10(pe.CorpusVecs, pe.QueryVecs, scaleS, 50)
				res.Int8Overlap10 = overlap10
				res.Int8DeltaMRR = i8Res.Overall.MeanMRR10 - dsRes["vec_f32"].Overall.MeanMRR10
				res.Int8DeltaNDCG = i8Res.Overall.MeanNDCG10 - dsRes["vec_f32"].Overall.MeanNDCG10
				fmt.Printf("Int8 Quantization on memlike (Scale S=%.1f): Overlap@10=%.2f%%, delta MRR=%+.4f, delta NDCG=%+.4f\n",
					scaleS, overlap10*100.0, res.Int8DeltaMRR, res.Int8DeltaNDCG)

				// If nomic, run Matryoshka 384 and 256
				if strings.Contains(m.ID, "nomic") {
					resM384 := runner.Evaluate(ds, pe, bm25Indices[dName], "vec_f32", 384, false, 0)
					dsRes["vec_f32_m384"] = resM384
					resM256 := runner.Evaluate(ds, pe, bm25Indices[dName], "vec_f32", 256, false, 0)
					dsRes["vec_f32_m256"] = resM256
					fmt.Printf("Nomic Matryoshka memlike: full-768 nDCG=%.4f | 384-d nDCG=%.4f | 256-d nDCG=%.4f\n",
						dsRes["vec_f32"].Overall.MeanNDCG10, resM384.Overall.MeanNDCG10, resM256.Overall.MeanNDCG10)
				}
			}

			res.DatasetResults[dName] = dsRes
			ndcg := dsRes["hybrid_confidence"].Overall.MeanNDCG10
			ndcgSum += ndcg
			ndcgCount++

			fmt.Printf("  Dataset %-10s | Hybrid nDCG@10: %.4f | MRR@10: %.4f | R@10: %.4f | Vec nDCG@10: %.4f | BM25: %.4f\n",
				dName, ndcg, dsRes["hybrid_confidence"].Overall.MeanMRR10, dsRes["hybrid_confidence"].Overall.MeanRecall10,
				dsRes["vec_f32"].Overall.MeanNDCG10, dsRes["bm25"].Overall.MeanNDCG10)
		}

		server.Stop()

		if ndcgCount > 0 {
			res.MacroNDCG10 = ndcgSum / ndcgCount
		}
		fmt.Printf("===> Macro nDCG@10: %.4f (Fits <= 100MB: %t, %.1f MB)\n", res.MacroNDCG10, res.Fits100MB, res.SizeMB)

		if m.ID == "bge-small-en-v1.5-q8_0" {
			baselineResult = &res
		}

		allResults = append(allResults, res)

		// Incremental save
		incBytes, _ := json.MarshalIndent(allResults, "", "  ")
		_ = os.WriteFile(findPath(*flagOutput), incBytes, 0644)
	}

	// Compute paired bootstrap CIs vs baseline
	if baselineResult != nil {
		for i := range allResults {
			cand := &allResults[i]
			for _, dName := range datasetNames {
				baseDSRes := baselineResult.DatasetResults[dName]
				candDSRes := cand.DatasetResults[dName]
				if baseDSRes == nil || candDSRes == nil {
					continue
				}
				baseScores := getQueryNDCGs(baseDSRes["hybrid_confidence"].PerQueryScores)
				candScores := getQueryNDCGs(candDSRes["hybrid_confidence"].PerQueryScores)
				ci := metrics.PairedBootstrapCI(candScores, baseScores, 10000, 42)
				cand.BootstrapVsBase[dName] = ci
			}
		}
	}

	// Sort results by Macro nDCG@10 descending
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].MacroNDCG10 > allResults[j].MacroNDCG10
	})

	// Print summary table
	fmt.Printf("\n\n========================================================================================\n")
	fmt.Printf("PART B — EMBEDDER COMPARISON TABLE (Sorted by Macro nDCG@10)\n")
	fmt.Printf("========================================================================================\n")
	fmt.Printf("%-28s %-7s %-5s %-7s %-7s %-7s %-9s %-9s %-9s %-9s %-9s\n",
		"Model ID", "Quant", "Dims", "SizeMB", "<=100MB", "Thru(tps)", "SciFact", "NFCorpus", "FiQA-sub", "memlike", "MacroNDCG")
	for _, r := range allResults {
		sf := r.DatasetResults["scifact"]["hybrid_confidence"].Overall.MeanNDCG10
		nf := r.DatasetResults["nfcorpus"]["hybrid_confidence"].Overall.MeanNDCG10
		fq := r.DatasetResults["fiqa_sub"]["hybrid_confidence"].Overall.MeanNDCG10
		ml := r.DatasetResults["memlike"]["hybrid_confidence"].Overall.MeanNDCG10
		fmt.Printf("%-28s %-7s %-5d %-7.1f %-7t %-7.1f %-9.4f %-9.4f %-9.4f %-9.4f %-9.4f\n",
			r.ModelID, r.Quant, r.Dims, r.SizeMB, r.Fits100MB, r.ThroughputTps, sf, nf, fq, ml, r.MacroNDCG10)
	}

	// Save results JSON
	outBytes, _ := json.MarshalIndent(allResults, "", "  ")
	_ = os.WriteFile(findPath(*flagOutput), outBytes, 0644)
	fmt.Printf("\nSaved all evaluation results to %s\n", *flagOutput)

	modelsMap := make(map[string]ModelEntry)
	for _, m := range models {
		modelsMap[m.ID] = m
	}

	// Run Levers if requested
	if *flagRunLevers && len(allResults) >= 3 {
		runPartCLevers(evaluator, datasets, bm25Indices, allResults, modelsMap, *flagPort)
	}

	// Run SLM if requested
	if *flagRunSLM {
		runPartDSLMs(datasets, *flagPort)
	}
}

func getQueryNDCGs(qms []metrics.QueryMetrics) []float64 {
	out := make([]float64, len(qms))
	for i, qm := range qms {
		out[i] = qm.NDCG10
	}
	return out
}

func deriveScaleS(vecs [][]float32) float64 {
	if len(vecs) == 0 {
		return 1058.0
	}
	// Sample coordinates
	var absCoords []float64
	sampleCount := min(500, len(vecs))
	for i := 0; i < sampleCount; i++ {
		for _, x := range vecs[i] {
			absCoords = append(absCoords, math.Abs(float64(x)))
		}
	}
	sort.Float64s(absCoords)
	// p99.5
	pIdx := int(float64(len(absCoords)) * 0.995)
	if pIdx >= len(absCoords) {
		pIdx = len(absCoords) - 1
	}
	pVal := absCoords[pIdx]
	if pVal <= 0 {
		pVal = 0.12
	}
	scale := 127.0 / pVal
	return math.Round(scale)
}

func computeInt8Overlap10(corpusVecs, queryVecs [][]float32, scaleS float64, numQueries int) float64 {
	if len(corpusVecs) == 0 || len(queryVecs) == 0 {
		return 0
	}
	if numQueries > len(queryVecs) {
		numQueries = len(queryVecs)
	}

	// Pre-quantize corpus
	cI8 := make([][]int8, len(corpusVecs))
	for i, v := range corpusVecs {
		cI8[i] = quantizeVector(v, scaleS)
	}

	totalOverlap := 0.0

	for q := 0; q < numQueries; q++ {
		qVec := queryVecs[q]
		qI8 := quantizeVector(qVec, scaleS)

		// Float32 top 10
		type sc struct {
			idx int
			val float64
		}
		var fList []sc
		for i, c := range corpusVecs {
			fList = append(fList, sc{idx: i, val: client.CosineSim(qVec, c)})
		}
		sort.Slice(fList, func(i, j int) bool { return fList[i].val > fList[j].val })

		top10F := make(map[int]bool)
		for i := 0; i < min(10, len(fList)); i++ {
			top10F[fList[i].idx] = true
		}

		// Int8 top 10
		var iList []sc
		for i, c := range cI8 {
			dot := 0
			for d := 0; d < len(c); d++ {
				dot += int(qI8[d]) * int(c[d])
			}
			iList = append(iList, sc{idx: i, val: float64(dot)})
		}
		sort.Slice(iList, func(i, j int) bool { return iList[i].val > iList[j].val })

		hits := 0
		for i := 0; i < min(10, len(iList)); i++ {
			if top10F[iList[i].idx] {
				hits++
			}
		}
		totalOverlap += float64(hits) / 10.0
	}

	return totalOverlap / float64(numQueries)
}

func quantizeVector(v []float32, scale float64) []int8 {
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
