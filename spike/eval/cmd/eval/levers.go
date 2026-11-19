package main

import (
	"fmt"
	"strings"

	"spike/eval/internal/bm25"
	"spike/eval/internal/client"
	"spike/eval/internal/levers"
	"spike/eval/internal/metrics"
	"spike/eval/internal/runner"
)

func runPartCLevers(
	evaluator *runner.Evaluator,
	datasets map[string]*runner.Dataset,
	baseBM25 map[string]*bm25.Index,
	modelResults []ModelResult,
	modelsMap map[string]ModelEntry,
	port int,
) {
	fmt.Printf("\n========================================================================================\n")
	fmt.Printf("PART C — PIPELINE LEVERS EVALUATION\n")
	fmt.Printf("========================================================================================\n")

	// Pick baseline (bge-small) + top 2 <= 100MB models
	var chosenModels []ModelResult
	for _, r := range modelResults {
		if r.ModelID == "bge-small-en-v1.5-q8_0" {
			chosenModels = append(chosenModels, r)
			break
		}
	}
	for _, r := range modelResults {
		if r.Fits100MB && r.ModelID != "bge-small-en-v1.5-q8_0" && len(chosenModels) < 3 {
			chosenModels = append(chosenModels, r)
		}
	}

	fmt.Printf("Evaluating Levers on Models: ")
	for _, m := range chosenModels {
		fmt.Printf("%s, ", m.ModelID)
	}
	fmt.Printf("\n")

	memlikeDS := datasets["memlike"]
	if memlikeDS == nil {
		fmt.Printf("memlike dataset missing, skipping Part C\n")
		return
	}

	corpusIDF := levers.BuildCorpusIDF(memlikeDS.Corpus)

	for _, m := range chosenModels {
		entry, ok := modelsMap[m.ModelID]
		if !ok {
			fmt.Printf("ModelEntry not found for %s, skipping\n", m.ModelID)
			continue
		}
		fmt.Printf("\n--- Model: %s ---\n", m.ModelID)

		server, err := client.StartLlamaServer(client.ServerOptions{
			BinaryPath: findPath("spike/bin/llama-server.exe"),
			ModelPath:  findPath(entry.Path),
			Port:       port,
			Embedding:  true,
			Pooling:    entry.Pooling,
			Parallel:   2,
		})
		if err != nil {
			fmt.Printf("Could not start server for %s (%s): %v, skipping\n", m.ModelID, entry.Path, err)
			continue
		}

		spec := runner.ModelSpec{
			ID:          entry.ID,
			Name:        entry.Name,
			Path:        findPath(entry.Path),
			SHA256:      entry.SHA256,
			PrefixQuery: entry.PrefixQuery,
			PrefixDoc:   entry.PrefixDoc,
			Pooling:     entry.Pooling,
		}

		// 1. Baseline Run on memlike
		peBase, _ := evaluator.PrepareCorpusEmbeddings(memlikeDS, spec, server, nil)
		resBase := runner.Evaluate(memlikeDS, peBase, baseBM25["memlike"], "hybrid_confidence", 0, false, 0)
		baseScores := getQueryNDCGs(resBase.PerQueryScores)
		fmt.Printf("  [0] Baseline:                   nDCG@10: %.4f | MRR@10: %.4f\n",
			resBase.Overall.MeanNDCG10, resBase.Overall.MeanMRR10)

		// Lever 1: Contextual chunk text
		specContext := spec
		specContext.SHA256 = spec.SHA256 + "_context"
		peContext, _ := evaluator.PrepareCorpusEmbeddings(memlikeDS, specContext, server, func(d runner.CorpusDoc) string {
			return levers.BuildContextualText(d)
		})
		bm25Context := runner.BuildBM25Index(memlikeDS, func(d runner.CorpusDoc) string {
			return levers.BuildContextualText(d)
		})
		resContext := runner.Evaluate(memlikeDS, peContext, bm25Context, "hybrid_confidence", 0, false, 0)
		ciContext := metrics.PairedBootstrapCI(getQueryNDCGs(resContext.PerQueryScores), baseScores, 10000, 42)
		fmt.Printf("  [1] Contextual Chunk Text:      nDCG@10: %.4f | CI: %s\n",
			resContext.Overall.MeanNDCG10, ciContext.String())

		// Lever 2: Document-level vector
		// Title + first 120 tokens
		specDocVec := spec
		specDocVec.SHA256 = spec.SHA256 + "_docvec"
		peDocVec, _ := evaluator.PrepareCorpusEmbeddings(memlikeDS, specDocVec, server, func(d runner.CorpusDoc) string {
			words := strings.Fields(d.Text)
			cutoff := 120
			if len(words) < cutoff {
				cutoff = len(words)
			}
			return d.Heading + "\n\n" + strings.Join(words[:cutoff], " ")
		})
		resDocVec := runner.Evaluate(memlikeDS, peDocVec, baseBM25["memlike"], "hybrid_confidence", 0, false, 0)
		ciDocVec := metrics.PairedBootstrapCI(getQueryNDCGs(resDocVec.PerQueryScores), baseScores, 10000, 42)
		fmt.Printf("  [2] Document-Level Vector:      nDCG@10: %.4f | CI: %s\n",
			resDocVec.Overall.MeanNDCG10, ciDocVec.String())

		// Lever 3: Tier-0 Extractive Enrichment
		specTier0 := spec
		specTier0.SHA256 = spec.SHA256 + "_tier0"
		peTier0, _ := evaluator.PrepareCorpusEmbeddings(memlikeDS, specTier0, server, func(d runner.CorpusDoc) string {
			enr := corpusIDF.ExtractEnrichment(d)
			return levers.BuildTier0EnrichedText(d, enr)
		})
		bm25Tier0 := runner.BuildBM25Index(memlikeDS, func(d runner.CorpusDoc) string {
			enr := corpusIDF.ExtractEnrichment(d)
			return levers.BuildTier0EnrichedText(d, enr)
		})
		resTier0 := runner.Evaluate(memlikeDS, peTier0, bm25Tier0, "hybrid_confidence", 0, false, 0)
		ciTier0 := metrics.PairedBootstrapCI(getQueryNDCGs(resTier0.PerQueryScores), baseScores, 10000, 42)
		fmt.Printf("  [3] Tier-0 Extractive Enrich:   nDCG@10: %.4f | CI: %s\n",
			resTier0.Overall.MeanNDCG10, ciTier0.String())

		// Lever 5: Query-side Prefix on vs off
		specNoPrefix := spec
		specNoPrefix.SHA256 = spec.SHA256 + "_noprefix"
		specNoPrefix.PrefixQuery = ""
		peNoPrefix, _ := evaluator.PrepareCorpusEmbeddings(memlikeDS, specNoPrefix, server, nil)
		resNoPrefix := runner.Evaluate(memlikeDS, peNoPrefix, baseBM25["memlike"], "hybrid_confidence", 0, false, 0)
		ciPrefix := metrics.PairedBootstrapCI(baseScores, getQueryNDCGs(resNoPrefix.PerQueryScores), 10000, 42)
		fmt.Printf("  [5] Query Prefix Impact:        nDCG with prefix: %.4f vs without: %.4f | Gain: %s\n",
			resBase.Overall.MeanNDCG10, resNoPrefix.Overall.MeanNDCG10, ciPrefix.String())

		// Lever 6: Fusion re-check
		resVecOnly := runner.Evaluate(memlikeDS, peBase, baseBM25["memlike"], "vec_f32", 0, false, 0)
		resBM25Only := runner.Evaluate(memlikeDS, peBase, baseBM25["memlike"], "bm25", 0, false, 0)
		resPlainRRF := runner.Evaluate(memlikeDS, peBase, baseBM25["memlike"], "hybrid_plain", 0, false, 0)
		resWeightedRRF := runner.Evaluate(memlikeDS, peBase, baseBM25["memlike"], "hybrid_weighted", 0, false, 0)

		fmt.Printf("  [6] Fusion Comparison on memlike:\n")
		fmt.Printf("      - Vec Only:                 nDCG@10: %.4f | MRR@10: %.4f\n", resVecOnly.Overall.MeanNDCG10, resVecOnly.Overall.MeanMRR10)
		fmt.Printf("      - BM25 Only:                nDCG@10: %.4f | MRR@10: %.4f\n", resBM25Only.Overall.MeanNDCG10, resBM25Only.Overall.MeanMRR10)
		fmt.Printf("      - Plain RRF (k=60):         nDCG@10: %.4f | MRR@10: %.4f\n", resPlainRRF.Overall.MeanNDCG10, resPlainRRF.Overall.MeanMRR10)
		fmt.Printf("      - Weighted RRF (w=0.10):    nDCG@10: %.4f | MRR@10: %.4f\n", resWeightedRRF.Overall.MeanNDCG10, resWeightedRRF.Overall.MeanMRR10)
		fmt.Printf("      - Confidence RRF (Product): nDCG@10: %.4f | MRR@10: %.4f\n", resBase.Overall.MeanNDCG10, resBase.Overall.MeanMRR10)

		server.Stop()
	}
}
