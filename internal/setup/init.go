package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"memory/internal/config"
	"memory/internal/engine"
	"memory/internal/store"
)

// Options are the flags `mem init` accepts. Zero values mean "ask" (or, with
// Yes, "use the default").
type Options struct {
	Yes         bool
	DataDir     string
	ModelsDir   string
	Port        int
	ExpiryDays  int
	IdleSeconds int
	WithRerank  bool
	NoDownload  bool
	OfflineDir  string
	// Force reinstalls the sidecar and the models even when they look valid.
	Force      bool
	ConfigPath string
	GOOS       string
	GOARCH     string
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
	InIsTTY    bool
	Downloader *Downloader
	// VerifyModel, when set, replaces the embedding round-trip (tests).
	VerifyModel func() error
}

// Result reports what init did, for the JSON shape and the summary.
type Result struct {
	ConfigPath  string   `json:"config_path"`
	DataDir     string   `json:"data_dir"`
	ModelsDir   string   `json:"models_dir"`
	BinDir      string   `json:"bin_dir"`
	Port        int      `json:"port"`
	ExpiryDays  int      `json:"expiry_days"`
	IdleSeconds int      `json:"idle_timeout_seconds"`
	Rerank      bool     `json:"rerank"`
	EmbedModel  string   `json:"embed_model"`
	RerankModel string   `json:"rerank_model"`
	SidecarPath string   `json:"sidecar_path"`
	Downloaded  []string `json:"downloaded"`
	Skipped     []string `json:"skipped"`
	Verified    bool     `json:"verified"`
	VerifyNote  string   `json:"verify_note"`
	NextSteps   []string `json:"next_steps"`
	ConfigSaved bool     `json:"config_saved"`
}

// Run performs the wizard.
func Run(opts Options) (Result, error) {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	errw := opts.Err
	if errw == nil {
		errw = os.Stderr
	}
	opts.In, opts.Out, opts.Err = in, out, errw
	if opts.GOOS == "" || opts.GOARCH == "" {
		a, err := CurrentAsset()
		if err != nil {
			return Result{}, err
		}
		opts.GOOS, opts.GOARCH = runtimeGOOS(), runtimeGOARCH()
		_ = a
	}
	if opts.Downloader == nil {
		opts.Downloader = DefaultDownloader()
		opts.Downloader.Log = func(msg string) { fmt.Fprintln(errw, "  "+msg) }
		opts.Downloader.Progress = progressPrinter(errw)
	}

	// Existing config becomes the defaults: init is idempotent.
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		p, err := config.Path()
		if err != nil {
			return Result{}, err
		}
		cfgPath = p
	}
	base, err := config.LoadFile(cfgPath)
	if err != nil {
		return Result{}, err
	}

	w := &wizard{opts: opts, cfg: base, r: bufio.NewReader(in)}
	if err := w.ask(); err != nil {
		return Result{}, err
	}
	res := Result{
		ConfigPath:  cfgPath,
		DataDir:     w.cfg.DataDir,
		ModelsDir:   w.cfg.ModelsDir,
		BinDir:      w.cfg.BinDir(),
		Port:        w.cfg.Port,
		ExpiryDays:  w.cfg.DefaultTempExpiryDays,
		IdleSeconds: w.cfg.IdleTimeoutSeconds,
		Rerank:      w.cfg.Rerank,
		EmbedModel:  w.cfg.EmbedModel,
		RerankModel: w.cfg.RerankModel,
	}

	if err := os.MkdirAll(w.cfg.DataDir, 0o755); err != nil {
		return res, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.MkdirAll(w.cfg.ModelsDir, 0o755); err != nil {
		return res, fmt.Errorf("create models dir: %w", err)
	}
	if err := w.cfg.SaveFile(cfgPath); err != nil {
		return res, err
	}
	res.ConfigSaved = true

	// Open the store once so `global` and the default file exist.
	if err := w.bootstrapStore(); err != nil {
		return res, err
	}

	if !w.opts.NoDownload {
		dl, err := w.downloadAll(&res)
		if err != nil {
			return res, err
		}
		res.Downloaded = dl.downloaded
		res.Skipped = dl.skipped
		res.SidecarPath = w.sidecar
		w.cfg.SidecarPath = w.sidecar
		if err := w.cfg.SaveFile(cfgPath); err != nil {
			return res, err
		}
	}

	if res.Verified, res.VerifyNote = w.verify(); res.Verified {
		fmt.Fprintf(out, "\n  ok  model server responds (%s)\n", res.VerifyNote)
	} else if res.VerifyNote != "" {
		fmt.Fprintf(errw, "\n  warning: %s\n", res.VerifyNote)
	}

	res.NextSteps = w.nextSteps()
	fmt.Fprintln(out)
	fmt.Fprintf(out, "mem is set up.\n")
	fmt.Fprintf(out, "  data    %s\n", w.cfg.DataDir)
	fmt.Fprintf(out, "  models  %s\n", w.cfg.ModelsDir)
	if w.sidecar != "" {
		fmt.Fprintf(out, "  server  %s\n", w.sidecar)
	}
	fmt.Fprintf(out, "  config  %s\n", cfgPath)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "next steps:")
	for _, s := range res.NextSteps {
		fmt.Fprintf(out, "  %s\n", s)
	}
	return res, nil
}

type wizard struct {
	opts    Options
	cfg     config.Config
	r       *bufio.Reader
	sidecar string
}

func (w *wizard) ask() error {
	var err error
	if w.cfg.DataDir, err = w.askString("Data folder", w.cfg.DataDir); err != nil {
		return err
	}
	if w.cfg.ModelsDir, err = w.askString("Models folder", w.cfg.ModelsDir); err != nil {
		return err
	}
	if w.cfg.Port, err = w.askInt("Dashboard port", w.cfg.Port); err != nil {
		return err
	}
	if w.cfg.DefaultTempExpiryDays, err = w.askInt("Default temp-context expiry (days)", w.cfg.DefaultTempExpiryDays); err != nil {
		return err
	}
	if w.cfg.IdleTimeoutSeconds, err = w.askInt("Model server idle timeout (seconds)", w.cfg.IdleTimeoutSeconds); err != nil {
		return err
	}
	// Reranking is opt-in and off by default.
	w.cfg.Rerank = w.opts.WithRerank || w.cfg.Rerank
	if !w.opts.Yes && w.opts.InIsTTY {
		fmt.Fprintf(w.opts.Out, "\nEnable the reranker model download? [y/N]\n")
		fmt.Fprintf(w.opts.Out, "  Off by default: the bundled tiny reranker measured worse than plain\n")
		fmt.Fprintf(w.opts.Out, "  fusion on technical notes (MRR 0.75 vs 0.83 in the evaluation).\n")
		ans, err := w.readLine("")
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "y", "yes":
			w.cfg.Rerank = true
		case "":
		default:
			w.cfg.Rerank = false
		}
	}
	if w.cfg.EmbedModel == "" {
		w.cfg.EmbedModel = config.DefaultEmbedModel
	}
	if w.cfg.RerankModel == "" {
		w.cfg.RerankModel = config.DefaultRerankModel
	}
	return nil
}

// askString returns the flag value, the interactive answer, or the default.
func (w *wizard) askString(label, def string) (string, error) {
	if v := w.flagString(label); v != "" {
		return v, nil
	}
	if w.opts.Yes || !w.opts.InIsTTY {
		return def, nil
	}
	fmt.Fprintf(w.opts.Out, "%s [%s]: ", label, def)
	line, err := w.readLine("")
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func (w *wizard) askInt(label string, def int) (int, error) {
	if s := w.flagString(label); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("%s must be a whole number, got %q", label, s)
		}
		return n, nil
	}
	if w.opts.Yes || !w.opts.InIsTTY {
		return def, nil
	}
	for {
		fmt.Fprintf(w.opts.Out, "%s [%d]: ", label, def)
		line, err := w.readLine("")
		if err != nil {
			return 0, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return def, nil
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			fmt.Fprintf(w.opts.Err, "  please enter a number\n")
			continue
		}
		return n, nil
	}
}

// flagString maps a prompt label to its CLI flag value.
func (w *wizard) flagString(label string) string {
	switch {
	case strings.HasPrefix(label, "Data"):
		return w.opts.DataDir
	case strings.HasPrefix(label, "Models"):
		return w.opts.ModelsDir
	case strings.HasPrefix(label, "Dashboard"):
		if w.opts.Port > 0 {
			return strconv.Itoa(w.opts.Port)
		}
	case strings.HasPrefix(label, "Default temp"):
		if w.opts.ExpiryDays > 0 {
			return strconv.Itoa(w.opts.ExpiryDays)
		}
	case strings.HasPrefix(label, "Model server"):
		if w.opts.IdleSeconds > 0 {
			return strconv.Itoa(w.opts.IdleSeconds)
		}
	}
	return ""
}

func (w *wizard) readLine(prompt string) (string, error) {
	if prompt != "" {
		fmt.Fprint(w.opts.Out, prompt)
	}
	line, err := w.r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read answer: %w", err)
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// bootstrapStore opens the store once so global and the default file exist.
func (w *wizard) bootstrapStore() error {
	s, err := store.Open(w.cfg)
	if err != nil {
		return fmt.Errorf("initialize store: %w", err)
	}
	return s.Close()
}

type downloadOutcome struct {
	downloaded []string
	skipped    []string
}

// downloadAll fetches the sidecar and the models, or copies them from
// --offline-dir.
func (w *wizard) downloadAll(res *Result) (downloadOutcome, error) {
	var out downloadOutcome
	asset, err := AssetFor(w.opts.GOOS, w.opts.GOARCH)
	if err != nil {
		return out, err
	}
	binDir := w.cfg.BinDir()

	if w.opts.OfflineDir != "" {
		// Offline install: copy the files the user already has, skipping any
		// that are already in place.
		binName := llamaServerName(w.opts.GOOS)
		binPath := filepath.Join(binDir, binName)
		if w.sidecarInstalled(binPath, asset) {
			out.skipped = append(out.skipped, asset.Name)
		} else {
			src := filepath.Join(w.opts.OfflineDir, asset.Name)
			if !fileExists(src) {
				// Fall back to an already-unpacked binary in the offline dir.
				fallback := filepath.Join(w.opts.OfflineDir, binName)
				if !fileExists(fallback) {
					return out, fmt.Errorf("offline dir %s has neither %s nor %s", w.opts.OfflineDir, asset.Name, binName)
				}
				if err := copyFile(fallback, binPath, 0o755); err != nil {
					return out, err
				}
			} else if err := w.installSidecar(src, binDir, asset); err != nil {
				return out, err
			}
			out.downloaded = append(out.downloaded, asset.Name)
		}
		w.sidecar = binPath
		for _, m := range w.modelsToFetch() {
			src := filepath.Join(w.opts.OfflineDir, m.File)
			dst := filepath.Join(w.cfg.ModelsDir, m.File)
			if !w.opts.Force {
				if ok, _ := verifyFile(dst, m.Size, m.SHA256); ok {
					out.skipped = append(out.skipped, m.File)
					continue
				}
			}
			if ok, _ := verifyFile(src, m.Size, m.SHA256); !ok {
				return out, fmt.Errorf("offline file %s is missing or does not match the pinned sha256", src)
			}
			if err := copyFile(src, dst, 0o644); err != nil {
				return out, err
			}
			out.downloaded = append(out.downloaded, m.File)
		}
		return out, nil
	}

	binName := llamaServerName(w.opts.GOOS)
	binPath := filepath.Join(binDir, binName)
	if w.sidecarInstalled(binPath, asset) {
		out.skipped = append(out.skipped, asset.Name)
		w.sidecar = binPath
	} else {
		fmt.Fprintf(w.opts.Err, "downloading llama-server %s (%s)\n", Release, humanBytes(asset.Size))
		tmp := filepath.Join(os.TempDir(), "mem-"+asset.Name)
		defer os.Remove(tmp)
		if err := w.opts.Downloader.Fetch(URL(asset), tmp, asset.Size, asset.SHA256); err != nil {
			return out, err
		}
		if err := w.installSidecar(tmp, binDir, asset); err != nil {
			return out, err
		}
		out.downloaded = append(out.downloaded, asset.Name)
		w.sidecar = binPath
	}

	for _, m := range w.modelsToFetch() {
		dst := filepath.Join(w.cfg.ModelsDir, m.File)
		if !w.opts.Force {
			if ok, _ := verifyFile(dst, m.Size, m.SHA256); ok {
				out.skipped = append(out.skipped, m.File)
				continue
			}
		}
		fmt.Fprintf(w.opts.Err, "downloading %s (%s)\n", m.File, humanBytes(m.Size))
		if err := w.opts.Downloader.Fetch(m.URL, dst, m.Size, m.SHA256); err != nil {
			return out, err
		}
		out.downloaded = append(out.downloaded, m.File)
	}
	return out, nil
}

func (w *wizard) modelsToFetch() []Model {
	models := []Model{EmbeddingModel}
	if w.cfg.Rerank {
		models = append(models, RerankModel)
	}
	for i := range models {
		switch models[i].File {
		case EmbeddingModel.File:
			models[i].URL = embedModelURL
		case RerankModel.File:
			models[i].URL = rerankModelURL
		}
	}
	return models
}

func (w *wizard) installSidecar(archivePath, binDir string, asset Asset) error {
	if _, err := InstallArchive(archivePath, binDir); err != nil {
		return err
	}
	// Record what was installed, including the hash, so a later init or
	// `mem status` can detect a corrupted or replaced binary instead of trusting
	// whatever happens to sit at that path.
	binPath := filepath.Join(binDir, llamaServerName(w.opts.GOOS))
	info, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("verify extracted llama-server: %w", err)
	}
	sum, err := fileSHA256(binPath)
	if err != nil {
		return err
	}
	return WriteMarker(binPath, Marker{Asset: asset.Name, Size: info.Size(), SHA256: sum})
}

// sidecarInstalled reports whether the installed binary is this release and
// still matches its recorded hash. A binary without a marker (a hand install)
// only has to pass the executable header check.
func (w *wizard) sidecarInstalled(binPath string, asset Asset) bool {
	if w.opts.Force {
		return false
	}
	if err := InspectBinary(binPath); err != nil {
		return false
	}
	m, err := ReadMarker(binPath)
	if err != nil {
		// Hand-installed binary: accept it, the next init will mark it.
		return true
	}
	if m.Asset != asset.Name {
		return false
	}
	if m.SHA256 != "" {
		got, err := fileSHA256(binPath)
		if err != nil || !strings.EqualFold(got, m.SHA256) {
			return false
		}
	}
	return true
}

// verify executes `llama-server --version`, then one embedding round-trip.
func (w *wizard) verify() (bool, string) {
	if w.sidecar == "" {
		bin := filepath.Join(w.cfg.BinDir(), llamaServerName(w.opts.GOOS))
		if fileExists(bin) {
			w.sidecar = bin
		}
	}
	if w.sidecar == "" {
		return false, "llama-server was not installed; indexed search will stay unavailable until `mem init` succeeds"
	}
	// Cheap structural check first: a stub, a truncated file or a binary for
	// another platform fails here with a precise reason instead of an opaque
	// "This version of %1 is not compatible" from exec.
	if err := InspectBinary(w.sidecar); err != nil {
		return false, fmt.Sprintf("%v; run `mem init --force`", err)
	}
	if err := runVersion(w.sidecar); err != nil {
		return false, fmt.Sprintf("llama-server --version failed: %v; run `mem init --force`", err)
	}
	if w.opts.VerifyModel != nil {
		if err := w.opts.VerifyModel(); err != nil {
			return false, fmt.Sprintf("model check failed: %v", err)
		}
		return true, "verified by test hook"
	}
	cfg := w.cfg
	cfg.SidecarPath = w.sidecar
	eng, err := engine.Open(cfg, engine.WithSidecarBinary(w.sidecar), engine.WithIdleTimeout(time.Duration(w.cfg.IdleTimeoutSeconds)*time.Second))
	if err != nil {
		return false, fmt.Sprintf("open engine: %v", err)
	}
	defer eng.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := embedProbe(eng, ctx); err != nil {
		return false, fmt.Sprintf("embedding round-trip failed: %v", err)
	}
	return true, "384-dimensional embedding"
}

// embedProbe runs one embedding through the engine to prove the sidecar works
// end to end.
func embedProbe(eng *engine.Engine, ctx context.Context) error {
	if _, err := eng.Search(ctx, engine.SearchRequest{Query: "hello", Limit: 1}); err != nil {
		return err
	}
	for _, note := range eng.Warnings() {
		if strings.Contains(note, "index") && strings.Contains(note, "unavailable") {
			return fmt.Errorf("%s", note)
		}
	}
	return nil
}

func (w *wizard) nextSteps() []string {
	steps := []string{
		"Claude Code:  claude mcp add mem -- mem mcp",
		`generic MCP:  {"mcpServers":{"mem":{"command":"mem","args":["mcp"]}}}`,
		fmt.Sprintf("dashboard:    mem server --port %d", w.cfg.Port),
	}
	steps = append(steps, "agent skill:  npx skills add <owner>/<repo> (or `mem skill --install`)")
	steps = append(steps, "restart your agent so it picks up the MCP server")
	return steps
}

func runVersion(bin string) error {
	cmd := exec.Command(bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func llamaServerName(goos string) string {
	if goos == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

func runtimeGOOS() string   { return runtime.GOOS }
func runtimeGOARCH() string { return runtime.GOARCH }

func progressPrinter(w io.Writer) func(Progress) {
	last := time.Time{}
	return func(p Progress) {
		if time.Since(last) < 200*time.Millisecond {
			return
		}
		last = time.Now()
		if p.Total > 0 {
			pct := float64(p.Done) * 100 / float64(p.Total)
			fmt.Fprintf(w, "\r  %s %5.1f%% %s/%s %s/s", p.File, pct, humanBytes(p.Done), humanBytes(p.Total), humanBytes(int64(p.Speed)))
		} else {
			fmt.Fprintf(w, "\r  %s %s %s/s", p.File, humanBytes(p.Done), humanBytes(int64(p.Speed)))
		}
		if p.Done >= p.Total && p.Total > 0 {
			fmt.Fprintln(w)
		}
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(dst, in, mode)
}
