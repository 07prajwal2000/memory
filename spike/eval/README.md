# Retrieval Evaluation Spike (08)

This directory contains the reproducible benchmark suite, dataset generators, and evaluation harness for evaluating embedding models, ingestion-time enrichment, and optional local SLMs.

## Directory Structure

```
spike/eval/
  cmd/
    eval/
      main.go         # Primary evaluation harness CLI
      crosscheck.go   # Part A.5 verification against REPORT-02c2
      levers.go       # Part C pipeline levers
      slm.go          # Part D optional SLMs
    test_bm25_crosscheck/
      main.go         # Standalone BM25 validation on testdata splits
  internal/
    bm25/             # In-memory BM25 with exact FTS5 tokenizer & stopwords
    client/           # llama-server process lifecycle & on-disk vector cache
    fuse/             # ConfidenceRRF & reciprocal rank fusion schemes
    levers/           # Contextual chunking, doc vectors, Tier-0 extractive TF-IDF
    metrics/          # nDCG@10, MRR@10, Recall@10/50, paired bootstrap 95% CIs
    runner/           # Dataset loaders, concurrent embedding, and evaluation pipeline
  data/
    prepare_beir.py   # Downloader and extractor for BEIR SciFact, NFCorpus, FiQA-sub
    build_memlike_corpus.py  # Markdown section splitter for mem-like corpus
    build_memlike_queries.py # 161 hand-authored queries with pre-declared labels
    scifact.json      # 5,183 docs, 300 test queries
    nfcorpus.json     # 3,633 docs, 323 test queries
    fiqa_sub.json     # 6,706 docs, 648 test queries
    memlike.json      # 364 docs, 161 test queries
    user-queries.json # (Optional) Drop-in slot for real user failing queries
  cache/              # Content-addressed float32 embedding vectors keyed by (model, prefix, text)
  REPORT.md           # Evaluation findings, tables, decision rule, and product specification
  README.md           # This document
```

---

## Reproducing the Datasets

All datasets can be rebuilt deterministically from scratch:

```powershell
# 1. Download and build BEIR public benchmarks (SciFact, NFCorpus, FiQA-sub):
python spike/eval/data/prepare_beir.py

# 2. Extract 364 memories (100-600 tokens) from repo markdown and Go stdlib reference:
python spike/eval/data/build_memlike_corpus.py

# 3. Build the 161 hand-crafted evaluation queries across 5 classes:
python spike/eval/data/build_memlike_queries.py
```

---

## User Queries Drop-in Slot Format

If you have real-world failing queries from production usage, place them at:
`spike/eval/data/user-queries.json`

The file must adhere to this JSON structure:
```json
{
  "name": "user_queries",
  "corpus": [
    {
      "id": "mem-prod-001",
      "doc_id": "mem-prod-001",
      "name": "deploy/runbook.md",
      "heading": "Production Deployment",
      "text": "Full markdown note content..."
    }
  ],
  "queries": [
    {
      "id": "q-user-01",
      "query": "how do I roll back a failed deployment",
      "class": "natural_question",
      "relevant_ids": ["mem-prod-001"]
    }
  ],
  "qrels": {
    "q-user-01": {
      "mem-prod-001": 1.0
    }
  }
}
```
When present, the evaluation harness automatically discovers this file and evaluates every model and lever against it as an additional benchmark set.

---

## Running the Evaluations

```powershell
# Cross-check the harness against REPORT-02c2 on internal/search/testdata:
go run -C spike ./eval/cmd/eval -verify-product

# Run the complete embedder evaluation across all models and datasets (Part B):
go run -C spike ./eval/cmd/eval

# Run pipeline levers (Part C):
go run -C spike ./eval/cmd/eval -levers

# Run small language model generation & faithfulness check (Part D):
go run -C spike ./eval/cmd/eval -slm
```
