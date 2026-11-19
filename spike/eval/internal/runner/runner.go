package runner

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"spike/eval/internal/bm25"
	"spike/eval/internal/client"
	"spike/eval/internal/fuse"
	"spike/eval/internal/metrics"
)

// Dataset matches our standardized JSON format.
type Dataset struct {
	Name    string                 `json:"name"`
	Corpus  []CorpusDoc            `json:"corpus"`
	Queries []QueryDoc             `json:"queries"`
	Qrels   map[string]map[string]float64 `json:"qrels"` // qid -> docid -> gain
}

// CorpusDoc represents a document or chunk in the dataset.
type CorpusDoc struct {
	ID       string `json:"id"`
	DocID    string `json:"doc_id,omitempty"`
	Title    string `json:"title,omitempty"`
	Heading  string `json:"heading,omitempty"`
	Text     string `json:"text"`
	Name     string `json:"name,omitempty"`
	Category string `json:"category,omitempty"`
}

// QueryDoc represents a query.
type QueryDoc struct {
	ID          string   `json:"id"`
	Query       string   `json:"query"`
	Class       string   `json:"class,omitempty"`
	RelevantIDs []string `json:"relevant_ids,omitempty"`
}

// ModelSpec defines embedder configuration.
type ModelSpec struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Quant       string `json:"quant"`
	Dims        int    `json:"dims"`
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	PrefixQuery string `json:"prefix_query"`
	PrefixDoc   string `json:"prefix_doc"`
	Pooling     string `json:"pooling"`
	SizeMB      float64 `json:"size_mb"`
}

// LoadDataset loads a dataset from JSON file.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset %s: %w", path, err)
	}
	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("unmarshal dataset %s: %w", path, err)
	}

	// Normalize DocID if missing
	for i := range ds.Corpus {
		if ds.Corpus[i].DocID == "" {
			ds.Corpus[i].DocID = ds.Corpus[i].ID
		}
	}

	return &ds, nil
}

// EvalResult holds the full evaluation output for a dataset + model + mode.
type EvalResult struct {
	DatasetName    string                              `json:"dataset"`
	ModelID        string                              `json:"model_id"`
	Mode           string                              `json:"mode"` // vec_f32, vec_i8, bm25, hybrid_confidence, hybrid_plain, hybrid_weighted
	Overall        metrics.AggregateMetrics            `json:"overall"`
	PerClass       map[string]metrics.AggregateMetrics `json:"per_class,omitempty"`
	PerQueryScores []metrics.QueryMetrics              `json:"per_query_scores"`
}

// Evaluator executes search evaluations on datasets.
type Evaluator struct {
	Cache *client.DiskCache
}

// NewEvaluator creates a new Evaluator.
func NewEvaluator(cacheDir string) (*Evaluator, error) {
	c, err := client.NewDiskCache(cacheDir)
	if err != nil {
		return nil, err
	}
	return &Evaluator{Cache: c}, nil
}

// PreparedEmbeddings holds in-memory cached vectors for corpus and queries.
type PreparedEmbeddings struct {
	CorpusIDs  []string
	DocIDMap   map[string]string // chunkID -> docID
	CorpusVecs [][]float32
	QueryIDs   []string
	QueryVecs  [][]float32
}

// TruncateToMaxWords truncates text to maxWords tokens/words and max chars (safely under 512 tokens).
func TruncateToMaxWords(text string, maxWords int) string {
	if maxWords <= 0 {
		maxWords = 200
	}
	words := strings.Fields(text)
	if len(words) > maxWords {
		words = words[:maxWords]
	}
	res := strings.Join(words, " ")
	if len(res) > 900 {
		res = res[:900]
	}
	return res
}

// PrepareCorpusEmbeddings embeds all corpus texts through cache + llamaServer.
func (e *Evaluator) PrepareCorpusEmbeddings(
	ds *Dataset,
	spec ModelSpec,
	server *client.LlamaServer,
	textExtractor func(d CorpusDoc) string,
) (*PreparedEmbeddings, error) {
	corpusTexts := make([]string, len(ds.Corpus))
	corpusIDs := make([]string, len(ds.Corpus))
	docIDMap := make(map[string]string, len(ds.Corpus))

	for i, doc := range ds.Corpus {
		corpusIDs[i] = doc.ID
		docIDMap[doc.ID] = doc.DocID
		var txt string
		if textExtractor != nil {
			txt = textExtractor(doc)
		} else {
			if doc.Title != "" && !strings.Contains(doc.Text, doc.Title) {
				txt = doc.Title + "\n\n" + doc.Text
			} else {
				txt = doc.Text
			}
		}
		corpusTexts[i] = TruncateToMaxWords(txt, 200)
	}

	// 1. Check disk cache
	cachedVecs, missedIndices, missedTexts := e.Cache.BatchGet(spec.SHA256, spec.PrefixDoc, corpusTexts)
	if len(missedTexts) > 0 {
		if server == nil {
			return nil, fmt.Errorf("missing %d embeddings in cache and server is nil", len(missedTexts))
		}
		// Embed with prefix
		prefixed := make([]string, len(missedTexts))
		for i, t := range missedTexts {
			prefixed[i] = spec.PrefixDoc + t
		}

		embs, err := server.EmbedConcurrent(prefixed, 16, 2)
		if err != nil {
			return nil, fmt.Errorf("embed corpus: %w", err)
		}

		for idx, origIdx := range missedIndices {
			cachedVecs[origIdx] = embs[idx]
		}
		e.Cache.BatchPut(spec.SHA256, spec.PrefixDoc, missedTexts, embs)
	}

	// 2. Prepare query embeddings
	queryTexts := make([]string, len(ds.Queries))
	queryIDs := make([]string, len(ds.Queries))
	for i, q := range ds.Queries {
		queryIDs[i] = q.ID
		queryTexts[i] = TruncateToMaxWords(q.Query, 100)
	}

	cachedQueryVecs, missedQIndices, missedQTexts := e.Cache.BatchGet(spec.SHA256, spec.PrefixQuery, queryTexts)
	if len(missedQTexts) > 0 {
		if server == nil {
			return nil, fmt.Errorf("missing %d query embeddings in cache and server is nil", len(missedQTexts))
		}
		prefixedQ := make([]string, len(missedQTexts))
		for i, t := range missedQTexts {
			prefixedQ[i] = spec.PrefixQuery + t
		}
		embs, err := server.EmbedConcurrent(prefixedQ, 16, 2)
		if err != nil {
			return nil, fmt.Errorf("embed queries: %w", err)
		}
		for idx, origIdx := range missedQIndices {
			cachedQueryVecs[origIdx] = embs[idx]
		}
		e.Cache.BatchPut(spec.SHA256, spec.PrefixQuery, missedQTexts, embs)
	}

	return &PreparedEmbeddings{
		CorpusIDs:  corpusIDs,
		DocIDMap:   docIDMap,
		CorpusVecs: cachedVecs,
		QueryIDs:   queryIDs,
		QueryVecs:  cachedQueryVecs,
	}, nil
}

// BuildBM25Index builds in-memory BM25 index from dataset.
func BuildBM25Index(ds *Dataset, textExtractor func(d CorpusDoc) string) *bm25.Index {
	idx := bm25.NewIndex(1.2, 0.75)
	for _, doc := range ds.Corpus {
		txt := doc.Text
		if textExtractor != nil {
			txt = textExtractor(doc)
		} else if doc.Title != "" {
			txt = doc.Title + " " + doc.Text
		}
		idx.AddDocument(bm25.Document{
			ID:    doc.ID,
			DocID: doc.DocID,
			Text:  txt,
		})
	}
	return idx
}

// Evaluate runs retrieval evaluation for a given mode.
func Evaluate(
	ds *Dataset,
	pe *PreparedEmbeddings,
	bm25Idx *bm25.Index,
	mode string,
	matryoshkaDim int,
	int8Quantize bool,
	int8Scale float64,
) EvalResult {
	queryMetricsList := make([]metrics.QueryMetrics, len(ds.Queries))
	classMetricsMap := make(map[string][]metrics.QueryMetrics)

	// Pre-process corpus vecs if matryoshka or int8
	corpusVecs := pe.CorpusVecs
	queryVecs := pe.QueryVecs

	if matryoshkaDim > 0 {
		corpusVecs = make([][]float32, len(pe.CorpusVecs))
		for i, v := range pe.CorpusVecs {
			corpusVecs[i] = client.MatryoshkaTruncate(v, matryoshkaDim)
		}
		queryVecs = make([][]float32, len(pe.QueryVecs))
		for i, v := range pe.QueryVecs {
			queryVecs[i] = client.MatryoshkaTruncate(v, matryoshkaDim)
		}
	}

	for qIdx, qDoc := range ds.Queries {
		qID := qDoc.ID
		qText := qDoc.Query
		relMap := ds.Qrels[qID]

		// 1. BM25 leg
		var bm25Hits []bm25.Hit
		bm25Ranks := make(map[string]int)
		bm25Scores := make(map[string]float64)
		if bm25Idx != nil {
			bm25Hits = bm25Idx.Search(qText, 50)
			for _, h := range bm25Hits {
				bm25Ranks[h.ID] = h.Rank
				bm25Scores[h.ID] = h.Score
			}
		}

		// 2. Vector leg
		vecRanks := make(map[string]int)
		vecScores := make(map[string]float64)
		if len(queryVecs) > qIdx && len(queryVecs[qIdx]) > 0 {
			qVec := queryVecs[qIdx]
			type vHit struct {
				id    string
				score float64
			}
			var allVHits []vHit

			if int8Quantize && int8Scale > 0 {
				// Quantize on the fly
				qI8 := quantizeInt8(qVec, int8Scale)
				for cIdx, cVec := range corpusVecs {
					cI8 := quantizeInt8(cVec, int8Scale)
					// int8 dot product
					dot := 0
					for d := 0; d < len(qI8); d++ {
						dot += int(qI8[d]) * int(cI8[d])
					}
					allVHits = append(allVHits, vHit{id: pe.CorpusIDs[cIdx], score: float64(dot)})
				}
			} else {
				// Exact float32 cosine
				for cIdx, cVec := range corpusVecs {
					sim := client.CosineSim(qVec, cVec)
					allVHits = append(allVHits, vHit{id: pe.CorpusIDs[cIdx], score: sim})
				}
			}

			sort.Slice(allVHits, func(i, j int) bool {
				return allVHits[i].score > allVHits[j].score
			})

			topN := 50
			if topN > len(allVHits) {
				topN = len(allVHits)
			}
			for r := 0; r < topN; r++ {
				h := allVHits[r]
				vecRanks[h.id] = r + 1
				vecScores[h.id] = h.score
			}
		}

		// 3. Fusion / Selection according to mode
		var candidates []fuse.Candidate
		switch mode {
		case "vec_f32", "vec_i8":
			candidates = fuse.VectorOnly(vecRanks, vecScores, pe.DocIDMap)
		case "bm25":
			candidates = fuse.BM25Only(bm25Ranks, bm25Scores, pe.DocIDMap)
		case "hybrid_plain":
			candidates = fuse.PlainRRF(vecRanks, bm25Ranks, pe.DocIDMap, 60.0)
		case "hybrid_weighted":
			candidates = fuse.WeightedRRF(vecRanks, bm25Ranks, pe.DocIDMap, 1.0, 0.10, 10.0)
		case "hybrid_confidence":
			fallthrough
		default:
			candidates = fuse.ConfidenceRRF(vecRanks, bm25Ranks, bm25Scores, pe.DocIDMap, qText, 0.25, 0.08)
		}

		retrievedDocIDs := make([]string, len(candidates))
		for i, cand := range candidates {
			retrievedDocIDs[i] = cand.DocID
		}

		qm := metrics.QueryMetrics{
			QueryID:  qID,
			NDCG10:   metrics.ComputeNDCG(retrievedDocIDs, relMap, 10),
			MRR10:    metrics.ComputeMRR(retrievedDocIDs, relMap, 10),
			Recall10: metrics.ComputeRecall(retrievedDocIDs, relMap, 10),
			Recall50: metrics.ComputeRecall(retrievedDocIDs, relMap, 50),
		}

		queryMetricsList[qIdx] = qm
		if qDoc.Class != "" {
			classMetricsMap[qDoc.Class] = append(classMetricsMap[qDoc.Class], qm)
		}
	}

	overall := metrics.ComputeAggregate(queryMetricsList)
	perClass := make(map[string]metrics.AggregateMetrics)
	for cls, qms := range classMetricsMap {
		perClass[cls] = metrics.ComputeAggregate(qms)
	}

	return EvalResult{
		DatasetName:    ds.Name,
		Mode:           mode,
		Overall:        overall,
		PerClass:       perClass,
		PerQueryScores: queryMetricsList,
	}
}

func quantizeInt8(v []float32, scale float64) []int8 {
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
