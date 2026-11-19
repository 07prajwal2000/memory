package metrics

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// GradedDoc represents a document retrieval candidate with its ground-truth relevance score.
type GradedDoc struct {
	DocID string
	Score float64 // model score (higher is better)
}

// ComputeNDCG computes nDCG@k for a ranked list of retrieved document IDs given ground-truth relevance scores.
// relevance is a map of docID -> gain (e.g. 0, 1, 2, ...).
func ComputeNDCG(retrieved []string, relevance map[string]float64, k int) float64 {
	if k <= 0 || len(retrieved) == 0 || len(relevance) == 0 {
		return 0.0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}

	// 1. DCG@k
	dcg := 0.0
	for i := 0; i < k; i++ {
		docID := retrieved[i]
		rel := relevance[docID]
		if rel > 0 {
			// standard TREC / BEIR formula: (2^rel - 1) / log2(i + 2)
			num := math.Pow(2.0, rel) - 1.0
			denom := math.Log2(float64(i + 2))
			dcg += num / denom
		}
	}

	// 2. Ideal DCG@k (IDCG)
	var gains []float64
	for _, rel := range relevance {
		if rel > 0 {
			gains = append(gains, rel)
		}
	}
	if len(gains) == 0 {
		return 0.0
	}
	sort.Slice(gains, func(i, j int) bool {
		return gains[i] > gains[j]
	})

	idcgK := k
	if idcgK > len(gains) {
		idcgK = len(gains)
	}

	idcg := 0.0
	for i := 0; i < idcgK; i++ {
		num := math.Pow(2.0, gains[i]) - 1.0
		denom := math.Log2(float64(i + 2))
		idcg += num / denom
	}

	if idcg == 0 {
		return 0.0
	}
	return dcg / idcg
}

// ComputeMRR computes MRR@k for retrieved document IDs given ground-truth relevance scores (> 0 is relevant).
func ComputeMRR(retrieved []string, relevance map[string]float64, k int) float64 {
	if k <= 0 || len(retrieved) == 0 || len(relevance) == 0 {
		return 0.0
	}
	if k > len(retrieved) {
		k = len(retrieved)
	}

	for i := 0; i < k; i++ {
		docID := retrieved[i]
		if relevance[docID] > 0 {
			return 1.0 / float64(i+1)
		}
	}
	return 0.0
}

// ComputeRecall computes Recall@k: (number of relevant docs retrieved in top k) / (total relevant docs for query).
func ComputeRecall(retrieved []string, relevance map[string]float64, k int) float64 {
	if k <= 0 || len(retrieved) == 0 || len(relevance) == 0 {
		return 0.0
	}
	totalRelevant := 0
	for _, rel := range relevance {
		if rel > 0 {
			totalRelevant++
		}
	}
	if totalRelevant == 0 {
		return 0.0
	}

	if k > len(retrieved) {
		k = len(retrieved)
	}

	hits := 0
	seen := make(map[string]bool)
	for i := 0; i < k; i++ {
		docID := retrieved[i]
		if relevance[docID] > 0 && !seen[docID] {
			hits++
			seen[docID] = true
		}
	}
	return float64(hits) / float64(totalRelevant)
}

// QueryMetrics holds evaluation metrics for a single query.
type QueryMetrics struct {
	QueryID  string
	NDCG10   float64
	MRR10    float64
	Recall10 float64
	Recall50 float64
}

// AggregateMetrics summarizes metrics across all queries.
type AggregateMetrics struct {
	NumQueries  int
	MeanNDCG10  float64
	MeanMRR10   float64
	MeanRecall10 float64
	MeanRecall50 float64
}

// ComputeAggregate computes average metrics across a slice of QueryMetrics.
func ComputeAggregate(qm []QueryMetrics) AggregateMetrics {
	if len(qm) == 0 {
		return AggregateMetrics{}
	}
	var sumNDCG, sumMRR, sumR10, sumR50 float64
	for _, m := range qm {
		sumNDCG += m.NDCG10
		sumMRR += m.MRR10
		sumR10 += m.Recall10
		sumR50 += m.Recall50
	}
	n := float64(len(qm))
	return AggregateMetrics{
		NumQueries:   len(qm),
		MeanNDCG10:   sumNDCG / n,
		MeanMRR10:    sumMRR / n,
		MeanRecall10: sumR10 / n,
		MeanRecall50: sumR50 / n,
	}
}

// BootstrapCI represents a 95% paired bootstrap confidence interval for delta (candidate - baseline).
type BootstrapCI struct {
	MeanDelta   float64
	Lower95     float64
	Upper95     float64
	Significant bool // true if CI does not include 0
}

func (ci BootstrapCI) String() string {
	sigStr := "sig"
	if !ci.Significant {
		sigStr = "n.s."
	}
	return fmt.Sprintf("%+.4f [%+.4f, %+.4f] (%s)", ci.MeanDelta, ci.Lower95, ci.Upper95, sigStr)
}

// PairedBootstrapCI computes a 95% confidence interval for candidate vs baseline with 10,000 resamples.
// cand and base must be matched query-by-query with identical length.
func PairedBootstrapCI(cand, base []float64, numResamples int, seed int64) BootstrapCI {
	n := len(cand)
	if n == 0 || n != len(base) {
		return BootstrapCI{}
	}
	if numResamples <= 0 {
		numResamples = 10000
	}

	deltas := make([]float64, n)
	sumDelta := 0.0
	for i := 0; i < n; i++ {
		deltas[i] = cand[i] - base[i]
		sumDelta += deltas[i]
	}
	meanDelta := sumDelta / float64(n)

	rng := rand.New(rand.NewSource(seed))
	resampledMeans := make([]float64, numResamples)
	for b := 0; b < numResamples; b++ {
		bSum := 0.0
		for i := 0; i < n; i++ {
			idx := rng.Intn(n)
			bSum += deltas[idx]
		}
		resampledMeans[b] = bSum / float64(n)
	}

	sort.Float64s(resampledMeans)
	// 2.5th and 97.5th percentiles
	lowIdx := int(math.Floor(0.025 * float64(numResamples)))
	highIdx := int(math.Floor(0.975 * float64(numResamples)))
	if lowIdx < 0 {
		lowIdx = 0
	}
	if highIdx >= numResamples {
		highIdx = numResamples - 1
	}

	low := resampledMeans[lowIdx]
	high := resampledMeans[highIdx]
	significant := (low > 0 && high > 0) || (low < 0 && high < 0)

	return BootstrapCI{
		MeanDelta:   meanDelta,
		Lower95:     low,
		Upper95:     high,
		Significant: significant,
	}
}
