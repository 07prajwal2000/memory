package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

type Kind int

const (
	KindQuery Kind = iota
	KindDocument
)

const (
	DefaultQueryPrefix = "Represent this sentence for searching relevant passages: "
	DefaultBatchSize   = 16
	DefaultDim         = 384
	DefaultMaxRerankDocs = 30
	DefaultMaxDocLen     = 2048
	DefaultHTTPTimeout   = 60 * time.Second
)

type Embedder interface {
	Embed(ctx context.Context, kind Kind, texts []string) ([][]float32, error)
}

type Reranker interface {
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

type HTTPEmbedder struct {
	BaseURL     string
	Model       string
	QueryPrefix string
	BatchSize   int
	Dim         int
	Client      *http.Client
}

type embeddingReq struct {
	Model string   `json:"model,omitempty"`
	Input []string `json:"input"`
}

type embeddingItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embeddingResp struct {
	Data  []embeddingItem `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (e *HTTPEmbedder) getClient() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return &http.Client{Timeout: DefaultHTTPTimeout}
}

func (e *HTTPEmbedder) getBatchSize() int {
	if e.BatchSize > 0 {
		return e.BatchSize
	}
	return DefaultBatchSize
}

func (e *HTTPEmbedder) getDim() int {
	if e.Dim > 0 {
		return e.Dim
	}
	return DefaultDim
}

func (e *HTTPEmbedder) getQueryPrefix() string {
	if e.QueryPrefix != "" {
		return e.QueryPrefix
	}
	return DefaultQueryPrefix
}

func (e *HTTPEmbedder) Embed(ctx context.Context, kind Kind, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	baseURL := strings.TrimRight(e.BaseURL, "/")
	if baseURL == "" {
		return nil, errors.New("embedder: BaseURL is required")
	}

	batchSize := e.getBatchSize()
	targetDim := e.getDim()
	prefix := e.getQueryPrefix()
	client := e.getClient()

	results := make([][]float32, len(texts))

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		batchTexts := make([]string, end-i)
		for j, t := range texts[i:end] {
			if kind == KindQuery {
				batchTexts[j] = prefix + t
			} else {
				batchTexts[j] = t
			}
		}

		reqBody, err := json.Marshal(embeddingReq{
			Model: e.Model,
			Input: batchTexts,
		})
		if err != nil {
			return nil, fmt.Errorf("embedder (%s): marshal request: %w", baseURL, err)
		}

		url := baseURL + "/v1/embeddings"
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("embedder (%s): create request: %w", baseURL, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("embedder (%s): request failed: %w", baseURL, err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("embedder (%s): read response body: %w", baseURL, err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("embedder (%s): http %d: %s", baseURL, resp.StatusCode, string(body))
		}

		var parsed embeddingResp
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("embedder (%s): unmarshal response: %w", baseURL, err)
		}

		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, fmt.Errorf("embedder (%s): API error: %s", baseURL, parsed.Error.Message)
		}

		if len(parsed.Data) != len(batchTexts) {
			return nil, fmt.Errorf("embedder (%s): expected %d embeddings, got %d", baseURL, len(batchTexts), len(parsed.Data))
		}

		for _, item := range parsed.Data {
			if item.Index < 0 || item.Index >= len(batchTexts) {
				return nil, fmt.Errorf("embedder (%s): response index out of bounds: %d", baseURL, item.Index)
			}
			vec := item.Embedding
			if len(vec) != targetDim {
				return nil, fmt.Errorf("embedder (%s): invalid embedding dimension %d, expected %d", baseURL, len(vec), targetDim)
			}

			// Defensive L2 normalization
			normalizeL2(vec)

			results[i+item.Index] = vec
		}
	}

	return results, nil
}

func normalizeL2(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x * x)
	}
	if sum > 0 {
		norm := float32(math.Sqrt(sum))
		invNorm := 1.0 / norm
		for i := range v {
			v[i] *= invNorm
		}
	}
}

type HTTPReranker struct {
	BaseURL   string
	Model     string
	MaxDocs   int
	MaxDocLen int
	Client    *http.Client
}

type rerankReq struct {
	Model     string   `json:"model,omitempty"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type rerankItem struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type rerankResp struct {
	Results []rerankItem `json:"results"`
	Data    []rerankItem `json:"data"` // alternative OpenAI / Cohere compatibility
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (r *HTTPReranker) getClient() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: DefaultHTTPTimeout}
}

func (r *HTTPReranker) getMaxDocs() int {
	if r.MaxDocs > 0 {
		return r.MaxDocs
	}
	return DefaultMaxRerankDocs
}

func (r *HTTPReranker) getMaxDocLen() int {
	if r.MaxDocLen > 0 {
		return r.MaxDocLen
	}
	return DefaultMaxDocLen
}

func (r *HTTPReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	if len(docs) == 0 {
		return []float64{}, nil
	}

	baseURL := strings.TrimRight(r.BaseURL, "/")
	if baseURL == "" {
		return nil, errors.New("reranker: BaseURL is required")
	}

	maxDocs := r.getMaxDocs()
	maxDocLen := r.getMaxDocLen()
	client := r.getClient()

	candidates := docs
	if len(candidates) > maxDocs {
		candidates = candidates[:maxDocs]
	}

	truncatedDocs := make([]string, len(candidates))
	for i, d := range candidates {
		if len(d) > maxDocLen {
			truncatedDocs[i] = d[:maxDocLen]
		} else {
			truncatedDocs[i] = d
		}
	}

	reqBody, err := json.Marshal(rerankReq{
		Model:     r.Model,
		Query:     query,
		Documents: truncatedDocs,
	})
	if err != nil {
		return nil, fmt.Errorf("reranker (%s): marshal request: %w", baseURL, err)
	}

	// Try /v1/rerank first, fallback to /rerank if 404
	endpoints := []string{"/v1/rerank", "/rerank"}
	var lastErr error

	for _, ep := range endpoints {
		url := baseURL + ep
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("reranker (%s): create request: %w", baseURL, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("reranker (%s): request failed: %w", baseURL, err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("reranker (%s): read response body: %w", baseURL, err)
			continue
		}

		if resp.StatusCode == http.StatusNotFound && ep == "/v1/rerank" {
			// Try fallback /rerank
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("reranker (%s): http %d: %s", baseURL, resp.StatusCode, string(body))
		}

		var parsed rerankResp
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("reranker (%s): unmarshal response: %w", baseURL, err)
		}

		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, fmt.Errorf("reranker (%s): API error: %s", baseURL, parsed.Error.Message)
		}

		items := parsed.Results
		if len(items) == 0 && len(parsed.Data) > 0 {
			items = parsed.Data
		}

		if len(items) != len(candidates) {
			return nil, fmt.Errorf("reranker (%s): expected %d scores, got %d", baseURL, len(candidates), len(items))
		}

		scores := make([]float64, len(candidates))
		for _, it := range items {
			if it.Index < 0 || it.Index >= len(candidates) {
				return nil, fmt.Errorf("reranker (%s): response index %d out of bounds", baseURL, it.Index)
			}
			scores[it.Index] = it.RelevanceScore
		}

		return scores, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("reranker (%s): failed to query endpoint", baseURL)
}
