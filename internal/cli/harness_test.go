package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"memory/internal/config"
	"memory/internal/engine"
	"memory/internal/llm"
	"memory/internal/search"
)

// deterministicEmbedder keeps every CLI test off the network and off a model
// process while still producing real vectors.
type deterministicEmbedder struct{}

func (deterministicEmbedder) Embed(ctx context.Context, kind llm.Kind, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, search.ModelDim)
		h := sha256.Sum256([]byte(text))
		for d := range vec {
			vec[d] = float32(int8(h[d%len(h)])) / 128.0
		}
		var sum float64
		for _, x := range vec {
			sum += float64(x * x)
		}
		if sum > 0 {
			norm := float32(math.Sqrt(sum))
			for d := range vec {
				vec[d] /= norm
			}
		}
		out[i] = vec
	}
	return out, nil
}

// harness drives commands in process with a temp config and fake embedder.
type harness struct {
	t      *testing.T
	dir    string
	cfg    config.Config
	cfgF   string
	stdin  *bytes.Buffer
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:               filepath.Join(dir, "data"),
		ModelsDir:             filepath.Join(dir, "models"),
		Port:                  7777,
		DefaultTempExpiryDays: 7,
		IdleTimeoutSeconds:    300,
		EmbedModel:            config.DefaultEmbedModel,
		RerankModel:           config.DefaultRerankModel,
	}
	cfgFile := filepath.Join(dir, "config.toml")
	if err := cfg.SaveFile(cfgFile); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	return &harness{t: t, dir: dir, cfg: cfg, cfgF: cfgFile, stdin: &bytes.Buffer{}, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
}

// env swaps in the deterministic embedder; every real option (clock, logger,
// sidecar skipping) is still applied by the CLI.
func (h *harness) env() Env {
	return Env{OpenEngine: func(cfg config.Config, opts ...engine.Option) (*engine.Engine, error) {
		all := append([]engine.Option{engine.WithEmbedder(deterministicEmbedder{})}, opts...)
		return engine.Open(cfg, all...)
	}}
}

// run executes one command line and returns code, stdout and stderr.
func (h *harness) run(args ...string) (int, string, string) {
	h.t.Helper()
	h.stdout.Reset()
	h.stderr.Reset()
	full := append([]string{"--config", h.cfgF}, args...)
	code := Run(full, IO{
		In:       h.stdin,
		Out:      h.stdout,
		Err:      h.stderr,
		InIsTTY:  false,
		OutIsTTY: false,
	}, h.env())
	return code, h.stdout.String(), h.stderr.String()
}

// runJSON runs a command with --json and decodes stdout into v.
func (h *harness) runJSON(v any, args ...string) (int, string) {
	h.t.Helper()
	code, out, errOut := h.run(append(args, "--json")...)
	if code == ExitOK {
		dec := json.NewDecoder(strings.NewReader(out))
		if err := dec.Decode(v); err != nil {
			h.t.Fatalf("decode JSON from %v: %v\nstdout: %s\nstderr: %s", args, err, out, errOut)
		}
	}
	return code, errOut
}

// openEngine opens the engine the same way the CLI does, for direct assertions.
func (h *harness) openEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := h.env().OpenEngine(h.cfg)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}
