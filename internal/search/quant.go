package search

import (
	"math"
)

const (
	// QuantVersion is incremented if the quantization scheme changes, forcing reindex.
	QuantVersion = 1

	// ModelID is the expected embedding model identifier.
	ModelID = "bge-small-en-v1.5-q8_0"

	// ModelDim is the expected dimensionality of the embedding vectors.
	ModelDim = 384

	// QuantScale is the global scaling factor applied before clamping to [-128, 127].
	// Empirically measured on 2,000 real embeddings (768,000 components) with bge-small-en-v1.5:
	// - Component distribution: p50=0.0266, p90=0.0695, p95=0.0856, p99=0.1945, p99.9=0.3872, max=0.4266.
	// - Naive 127.0 scale achieved only 57.6% Overlap@10 and 83.3% Recall@50.
	// - Per-vector max-abs achieved 79.4% Overlap@10 and 91.0% Recall@50.
	// - Fixed scale S = 127 / 0.12 = 1058.0 achieved peak 93.60% Overlap@10 and 95.76% Recall@50
	//   under L2 distance (sqlite-vec default metric), dramatically outperforming naive scaling.
	QuantScale = 1058.0
)

// Quantize converts an L2-normalized float32 vector to int8 byte slice using
// fixed global percentile scaling and saturation clamping.
func Quantize(v []float32) []byte {
	out := make([]byte, len(v))
	for i, x := range v {
		val := math.Round(float64(x) * QuantScale)
		if val > 127 {
			val = 127
		} else if val < -128 {
			val = -128
		}
		out[i] = byte(int8(val))
	}
	return out
}

// Dequantize converts an int8 byte slice back to float32 for comparison/rescoring.
func Dequantize(b []byte) []float32 {
	out := make([]float32, len(b))
	const invScale = float32(1.0 / QuantScale)
	for i, x := range b {
		out[i] = float32(int8(x)) * invScale
	}
	return out
}

// QuantizeBit converts an L2-normalized float32 vector to a 1-bit packed byte slice (48 bytes for 384-d).
// Bit i is set to 1 if v[i] > 0, else 0.
func QuantizeBit(v []float32) []byte {
	out := make([]byte, (len(v)+7)/8)
	for i, x := range v {
		if x > 0 {
			out[i/8] |= byte(1 << (i % 8))
		}
	}
	return out
}

// QuantizeBitFromInt8 converts an int8 quantized vector (384 bytes) to a 1-bit packed byte slice (48 bytes).
// Bit i is set to 1 if b[i] > 0, else 0.
func QuantizeBitFromInt8(b []byte) []byte {
	out := make([]byte, (len(b)+7)/8)
	for i, x := range b {
		if int8(x) > 0 {
			out[i/8] |= byte(1 << (i % 8))
		}
	}
	return out
}
