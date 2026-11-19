package client

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// DiskCache caches embeddings on disk keyed by (model sha256, prefix, text hash).
type DiskCache struct {
	cacheDir string
	mu       sync.RWMutex
}

// NewDiskCache creates or opens a disk cache directory.
func NewDiskCache(dir string) (*DiskCache, error) {
	if dir == "" {
		dir = "spike/eval/cache"
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &DiskCache{cacheDir: dir}, nil
}

// CacheKey computes a stable hash for a model, prefix, and text.
func CacheKey(modelSHA256, prefix, text string) string {
	h := sha256.New()
	h.Write([]byte(modelSHA256))
	h.Write([]byte("\x00"))
	h.Write([]byte(prefix))
	h.Write([]byte("\x00"))
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

// Get retrieves an embedding from disk cache if present.
func (c *DiskCache) Get(modelSHA256, prefix, text string) ([]float32, bool) {
	key := CacheKey(modelSHA256, prefix, text)
	subDir := filepath.Join(c.cacheDir, key[:2])
	filePath := filepath.Join(subDir, key+".bin")

	c.mu.RLock()
	defer c.mu.RUnlock()

	f, err := os.Open(filePath)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	var dim uint16
	if err := binary.Read(f, binary.LittleEndian, &dim); err != nil {
		return nil, false
	}

	emb := make([]float32, dim)
	if err := binary.Read(f, binary.LittleEndian, emb); err != nil {
		return nil, false
	}

	return emb, true
}

// Put writes an embedding to disk cache.
func (c *DiskCache) Put(modelSHA256, prefix, text string, emb []float32) error {
	key := CacheKey(modelSHA256, prefix, text)
	subDir := filepath.Join(c.cacheDir, key[:2])
	filePath := filepath.Join(subDir, key+".bin")

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(subDir, 0755); err != nil {
		return err
	}

	tmpPath := filePath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	dim := uint16(len(emb))
	if err := binary.Write(f, binary.LittleEndian, dim); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := binary.Write(f, binary.LittleEndian, emb); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	return os.Rename(tmpPath, filePath)
}

// BatchGet retrieves multiple embeddings, returning missed indices and texts.
func (c *DiskCache) BatchGet(modelSHA256, prefix string, texts []string) ([][]float32, []int, []string) {
	results := make([][]float32, len(texts))
	var missedIndices []int
	var missedTexts []string

	for i, t := range texts {
		if emb, ok := c.Get(modelSHA256, prefix, t); ok {
			results[i] = emb
		} else {
			missedIndices = append(missedIndices, i)
			missedTexts = append(missedTexts, t)
		}
	}

	return results, missedIndices, missedTexts
}

// BatchPut saves multiple embeddings.
func (c *DiskCache) BatchPut(modelSHA256, prefix string, texts []string, embs [][]float32) {
	for i, t := range texts {
		if i < len(embs) && len(embs[i]) > 0 {
			_ = c.Put(modelSHA256, prefix, t, embs[i])
		}
	}
}

// CosineSim computes the cosine similarity between two float32 slices.
// If vectors are already unit-normalized, dot product equals cosine similarity.
func CosineSim(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		x := float64(a[i])
		y := float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// MatryoshkaTruncate truncates an embedding to newDim and re-normalizes to unit length.
func MatryoshkaTruncate(v []float32, newDim int) []float32 {
	if newDim >= len(v) {
		return v
	}
	out := make([]float32, newDim)
	copy(out, v[:newDim])

	var sumSq float64
	for _, x := range out {
		sumSq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sumSq)
	if norm > 0 {
		inv := float32(1.0 / norm)
		for i := range out {
			out[i] *= inv
		}
	}
	return out
}
