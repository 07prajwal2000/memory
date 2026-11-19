package setup

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"memory/internal/config"
)

// offlineFixture builds a directory with a fake archive and fake models, plus
// a downloader pointed at a local HTTP server that serves the same bytes, so
// tests exercise both the offline copy path and the network path.
type offlineFixture struct {
	dir      string
	asset    Asset
	archive  string
	models   map[string][]byte
	server   *httptest.Server
	requests []string
}

func newOfflineFixture(t *testing.T, goos, goarch string) *offlineFixture {
	t.Helper()
	dir := t.TempDir()
	asset, err := AssetFor(goos, goarch)
	if err != nil {
		t.Fatalf("AssetFor: %v", err)
	}

	// A minimal archive in the right format holding llama-server plus a lib.
	fakeServer := fakeExecutable(t, 9728)
	var archiveBytes []byte
	if asset.Format == "zip" {
		archiveBytes = makeZip(t, map[string][]byte{
			"llama-server.exe": fakeServer,
			"llama.dll":        []byte("fake lib"),
		})
	} else {
		archiveBytes = makeTarGz(t, map[string][]byte{
			"llama-b12503/llama-server": fakeServer,
			"llama-b12503/libllama.so":  []byte("fake lib"),
		}, map[string]int64{"llama-b12503/llama-server": 0o755})
	}
	archive := filepath.Join(dir, asset.Name)
	if err := os.WriteFile(archive, archiveBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	embed := []byte("fake embedding model bytes")
	rerank := []byte("fake rerank model bytes")
	if err := os.WriteFile(filepath.Join(dir, EmbeddingModel.File), embed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RerankModel.File), rerank, 0o644); err != nil {
		t.Fatal(err)
	}

	f := &offlineFixture{
		dir:     dir,
		asset:   asset,
		archive: archive,
		models:  map[string][]byte{EmbeddingModel.File: embed, RerankModel.File: rerank},
	}
	// Serve the same bytes so a network run installs an identical tree.
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, asset.Name):
			w.Write(archiveBytes)
		case strings.HasSuffix(r.URL.Path, EmbeddingModel.File):
			w.Write(embed)
		case strings.HasSuffix(r.URL.Path, RerankModel.File):
			w.Write(rerank)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// pinFixture repoints the pinned sizes and hashes at the fixture's fake bytes
// so runs can install them. Only the values, not the shapes, differ.
func (f *offlineFixture) pinFixture(t *testing.T) {
	t.Helper()
	oldEmbed, oldRerank := EmbeddingModel, RerankModel
	oldAsset := Assets[Key{runtime.GOOS, runtime.GOARCH}]
	t.Cleanup(func() {
		EmbeddingModel, RerankModel = oldEmbed, oldRerank
		Assets[Key{runtime.GOOS, runtime.GOARCH}] = oldAsset
	})

	EmbeddingModel.Size = int64(len(f.models[EmbeddingModel.File]))
	EmbeddingModel.SHA256 = shaOf(f.models[EmbeddingModel.File])
	RerankModel.Size = int64(len(f.models[RerankModel.File]))
	RerankModel.SHA256 = shaOf(f.models[RerankModel.File])

	data, err := os.ReadFile(f.archive)
	if err != nil {
		t.Fatalf("read fixture archive: %v", err)
	}
	a := oldAsset
	a.Size = int64(len(data))
	a.SHA256 = shaOf(data)
	Assets[Key{runtime.GOOS, runtime.GOARCH}] = a
}

// initOptions wires Run against either the offline dir or the fake server.
func (f *offlineFixture) initOptions(t *testing.T, offline bool, mutate func(*Options)) Options {
	t.Helper()
	dir := t.TempDir()
	opts := Options{
		Yes:        true,
		DataDir:    filepath.Join(dir, "data"),
		ModelsDir:  filepath.Join(dir, "models"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		Out:        &bytes.Buffer{},
		Err:        &bytes.Buffer{},
		In:         &bytes.Buffer{},
		// The fake binary cannot answer --version or embed, so verification is
		// injected instead of exercised here.
		VerifyModel: func() error { return nil },
	}
	opts.Downloader = &Downloader{
		Client:        f.server.Client(),
		UserAgent:     "mem/test",
		Attempts:      2,
		Backoff:       0,
		DisableResume: true,
	}
	// Both paths verify the pinned bytes, which in tests are the fixture's.
	f.pinFixture(t)
	if offline {
		opts.OfflineDir = f.dir
	} else {
		// Route the pinned URLs at the local server.
		oldRelease, oldEmbed, oldRerank := releaseBaseURL, embedModelURL, rerankModelURL
		releaseBaseURL = f.server.URL + "/"
		embedModelURL = f.server.URL + "/" + EmbeddingModel.File
		rerankModelURL = f.server.URL + "/" + RerankModel.File
		t.Cleanup(func() {
			releaseBaseURL, embedModelURL, rerankModelURL = oldRelease, oldEmbed, oldRerank
		})
	}
	if mutate != nil {
		mutate(&opts)
	}
	return opts
}

func TestInitNonInteractiveWritesConfigAndStore(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, nil)

	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ConfigSaved {
		t.Fatal("config was not saved")
	}
	cfg, err := config.LoadFile(opts.ConfigPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.DataDir != opts.DataDir || cfg.ModelsDir != opts.ModelsDir {
		t.Fatalf("saved config = %+v", cfg)
	}
	if cfg.IdleTimeoutSeconds != config.DefaultIdleTimeoutSeconds {
		t.Errorf("idle timeout = %d, want %d", cfg.IdleTimeoutSeconds, config.DefaultIdleTimeoutSeconds)
	}
	if cfg.Rerank {
		t.Error("rerank must default to off")
	}

	// The store was bootstrapped: global plus the default file exist.
	def := filepath.Join(opts.DataDir, "contexts", "global", "readme.md")
	if _, err := os.Stat(def); err != nil {
		t.Fatalf("default file missing: %v", err)
	}

	// The sidecar and the embedding model were installed from the offline dir.
	bin := filepath.Join(opts.ModelsDir, "..", "bin", llamaServerName(runtime.GOOS))
	if _, err := os.Stat(bin); err != nil {
		t.Errorf("llama-server not installed at %s: %v", bin, err)
	}
	if _, err := os.Stat(filepath.Join(opts.ModelsDir, EmbeddingModel.File)); err != nil {
		t.Errorf("embedding model not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.ModelsDir, RerankModel.File)); err == nil {
		t.Error("rerank model was installed without --with-rerank")
	}
	if res.SidecarPath == "" {
		t.Error("result did not report the installed sidecar")
	}
}

func TestInitNoDownload(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, func(o *Options) {
		o.NoDownload = true
		o.OfflineDir = ""
	})
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Downloaded) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("--no-download still downloaded: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(opts.ModelsDir, EmbeddingModel.File)); err == nil {
		t.Error("--no-download installed a model")
	}
}

func TestInitWithRerankDownloadsBothModels(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, func(o *Options) {
		o.WithRerank = true
	})
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Rerank {
		t.Fatal("--with-rerank did not enable reranking")
	}
	for _, m := range []Model{EmbeddingModel, RerankModel} {
		if _, err := os.Stat(filepath.Join(opts.ModelsDir, m.File)); err != nil {
			t.Errorf("%s not installed: %v", m.File, err)
		}
	}
}

func TestInitIsIdempotent(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	first := f.initOptions(t, true, nil)
	if _, err := Run(first); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Put a memory in the store; a second init must not delete it.
	memoryDir := filepath.Join(first.DataDir, "contexts", "global")
	if err := os.WriteFile(filepath.Join(memoryDir, "keep.md"), []byte("# keep\n\nprecious\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := first
	second.Out = &bytes.Buffer{}
	second.Err = &bytes.Buffer{}
	res, err := Run(second)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(memoryDir, "keep.md")); err != nil {
		t.Fatalf("re-running init deleted data: %v", err)
	}
	// Everything already present and valid is skipped, not re-downloaded.
	if len(res.Downloaded) != 0 {
		t.Errorf("second init downloaded %v", res.Downloaded)
	}
	if len(res.Skipped) == 0 {
		t.Error("second init reported no skipped files")
	}

	// Existing config values are the defaults: an explicit change sticks only
	// when asked for, and a re-run keeps a previously set port.
	cfg, err := config.LoadFile(second.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Port = 9123
	if err := cfg.SaveFile(second.ConfigPath); err != nil {
		t.Fatal(err)
	}
	third := second
	third.Port = 9123
	if _, err := Run(third); err != nil {
		t.Fatalf("third Run: %v", err)
	}
	got, err := config.LoadFile(second.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9123 {
		t.Errorf("port = %d, want 9123", got.Port)
	}
}

func TestInitNonInteractiveUsesProvidedFlags(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, func(o *Options) {
		o.Port = 8123
		o.ExpiryDays = 3
		o.IdleSeconds = 42
	})
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Port != 8123 || res.ExpiryDays != 3 || res.IdleSeconds != 42 {
		t.Fatalf("res = %+v", res)
	}
	cfg, err := config.LoadFile(opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8123 || cfg.DefaultTempExpiryDays != 3 || cfg.IdleTimeoutSeconds != 42 {
		t.Fatalf("saved = %+v", cfg)
	}
}

func TestInitInteractivePromptsUseDefaults(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, func(o *Options) {
		// Five blank answers accept every default; the rerank question defaults
		// to no.
		o.In = strings.NewReader("\n\n\n\n\n\n")
		o.InIsTTY = true
		o.Yes = false
		o.DataDir = ""
		o.ModelsDir = ""
	})
	out := opts.Out.(*bytes.Buffer)
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "Data folder [") {
		t.Errorf("prompts were not shown: %q", out.String())
	}
	if !strings.Contains(out.String(), "reranker") {
		t.Error("the reranker question was not asked")
	}
	if res.Rerank {
		t.Error("blank answer must leave reranking off")
	}
	if res.Port != config.DefaultPort {
		t.Errorf("port = %d, want %d", res.Port, config.DefaultPort)
	}
}

func TestInitInteractiveEnablesRerank(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, func(o *Options) {
		o.In = strings.NewReader("\n\n\n\n\ny\n")
		o.InIsTTY = true
		o.Yes = false
		o.DataDir = ""
		o.ModelsDir = ""
	})
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Rerank {
		t.Fatal("answering yes did not enable reranking")
	}
}

func TestInitRejectsBadOfflineDir(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, func(o *Options) {
		o.OfflineDir = t.TempDir() // empty
	})
	if _, err := Run(opts); err == nil {
		t.Fatal("init accepted an empty offline dir")
	}
}

func TestInitNextStepsMentionMCP(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, nil)
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(res.NextSteps, "\n")
	for _, want := range []string{"claude mcp add", "mcpServers", "mem server", "mem skill"} {
		if !strings.Contains(joined, want) {
			t.Errorf("next steps missing %q:\n%s", want, joined)
		}
	}
}

// TestInitNetworkPath exercises the real downloader against a local server.
func TestInitNetworkPath(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, false, nil)
	if _, err := Run(opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.ModelsDir, EmbeddingModel.File)); err != nil {
		t.Errorf("embedding model missing: %v", err)
	}
	if len(f.requests) == 0 {
		t.Fatal("the network path never contacted the server")
	}
}

var _ = fmt.Sprint
