package search

import (
	"math/rand"
	"testing"
	"time"
)

func TestVecStore_BasicOperations(t *testing.T) {
	vs := newVecStore()

	rnd := rand.New(rand.NewSource(42))
	makeVec := func() ([]byte, []byte) {
		f := make([]float32, ModelDim)
		for i := 0; i < ModelDim; i++ {
			f[i] = float32(rnd.NormFloat64())
		}
		return Quantize(f), QuantizeBit(f)
	}

	// 1. Upsert
	for i := 1; i <= 100; i++ {
		i8, bit := makeVec()
		ctx := "ctx-a"
		if i > 50 {
			ctx = "ctx-b"
		}
		vs.Upsert(int64(i), ctx, i8, bit)
	}

	if vs.Count() != 100 {
		t.Fatalf("expected count 100, got %d", vs.Count())
	}

	// 2. Query scoped to ctx-a
	qI8, qBit := makeVec()
	hitsA := vs.SearchTwoStage(qI8, qBit, []string{"ctx-a"}, 10, 50)
	if len(hitsA) != 10 {
		t.Fatalf("expected 10 hits, got %d", len(hitsA))
	}
	for _, h := range hitsA {
		if h.rowid > 50 {
			t.Errorf("expected rowid <= 50 for ctx-a, got %d", h.rowid)
		}
	}

	// 3. Delete some rows
	vs.Delete([]int64{1, 2, 3})
	if vs.Count() != 97 {
		t.Fatalf("expected count 97, got %d", vs.Count())
	}

	// 4. Delete context
	vs.DeleteContext("ctx-b")
	if vs.Count() != 47 {
		t.Fatalf("expected count 47, got %d", vs.Count())
	}
}

func TestVecStore_RecallAndLatencyBenchmark(t *testing.T) {
	vs := newVecStore()

	rnd := rand.New(rand.NewSource(12345))
	makeVec := func() ([]byte, []byte) {
		f := make([]float32, ModelDim)
		for i := 0; i < ModelDim; i++ {
			f[i] = float32(rnd.NormFloat64())
		}
		return Quantize(f), QuantizeBit(f)
	}

	// Populate 10,000 vectors
	for i := 1; i <= 10000; i++ {
		i8, bit := makeVec()
		vs.Upsert(int64(i), "global", i8, bit)
	}

	// Test recall against exact search
	var totalRecall float64
	numQueries := 20
	for q := 0; q < numQueries; q++ {
		qI8, qBit := makeVec()
		exact := vs.SearchExact(qI8, []string{"global"}, 50)
		twoStage := vs.SearchTwoStage(qI8, qBit, []string{"global"}, 50, 200)

		exactSet := make(map[int64]bool)
		for _, e := range exact {
			exactSet[e.rowid] = true
		}
		matches := 0
		for _, ts := range twoStage {
			if exactSet[ts.rowid] {
				matches++
			}
		}
		totalRecall += float64(matches) / float64(len(exact))
	}

	avgRecall := totalRecall / float64(numQueries)
	t.Logf("10k Chunks Recall@50 against exact: %.2f%%", avgRecall*100)
	if avgRecall < 0.85 {
		t.Errorf("recall@50 too low: %.4f", avgRecall)
	}

	// Latency across scales
	scales := []int{10000, 39000, 100000, 200000}
	currentCount := 10000
	for _, targetCount := range scales {
		for i := currentCount + 1; i <= targetCount; i++ {
			i8, bit := makeVec()
			vs.Upsert(int64(i), "global", i8, bit)
		}
		currentCount = targetCount

		qI8, qBit := makeVec()
		// Warmup
		_ = vs.SearchTwoStage(qI8, qBit, []string{"global"}, 50, 200)

		t0 := time.Now()
		const iters = 50
		for it := 0; it < iters; it++ {
			_ = vs.SearchTwoStage(qI8, qBit, []string{"global"}, 50, 200)
		}
		avgDuration := time.Since(t0) / iters

		t.Logf("Scale %d vectors: TwoStage KNN avg latency = %v", targetCount, avgDuration)
	}
}
