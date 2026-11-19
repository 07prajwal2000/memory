package setup

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- asset selection -------------------------------------------------------

func TestAssetForAllTargets(t *testing.T) {
	cases := []struct {
		goos   string
		goarch string
		name   string
		format string
	}{
		{"windows", "amd64", "llama-" + Release + "-bin-win-cpu-x64.zip", "zip"},
		{"windows", "arm64", "llama-" + Release + "-bin-win-cpu-arm64.zip", "zip"},
		{"linux", "amd64", "llama-" + Release + "-bin-ubuntu-x64.tar.gz", "tar.gz"},
		{"linux", "arm64", "llama-" + Release + "-bin-ubuntu-arm64.tar.gz", "tar.gz"},
		{"darwin", "amd64", "llama-" + Release + "-bin-macos-x64.tar.gz", "tar.gz"},
		{"darwin", "arm64", "llama-" + Release + "-bin-macos-arm64.tar.gz", "tar.gz"},
	}
	seen := make(map[string]bool)
	for _, c := range cases {
		a, err := AssetFor(c.goos, c.goarch)
		if err != nil {
			t.Fatalf("AssetFor(%s,%s): %v", c.goos, c.goarch, err)
		}
		if a.Name != c.name {
			t.Errorf("%s/%s asset = %q, want %q", c.goos, c.goarch, a.Name, c.name)
		}
		if a.Format != c.format {
			t.Errorf("%s/%s format = %q, want %q", c.goos, c.goarch, a.Format, c.format)
		}
		if a.Size <= 0 || len(a.SHA256) != 64 {
			t.Errorf("%s/%s missing size or sha256: %+v", c.goos, c.goarch, a)
		}
		if seen[a.Name] {
			t.Errorf("asset %q used for two targets", a.Name)
		}
		seen[a.Name] = true
		wantURL := "https://github.com/ggml-org/llama.cpp/releases/download/" + Release + "/" + c.name
		if URL(a) != wantURL {
			t.Errorf("URL = %q, want %q", URL(a), wantURL)
		}
	}
	if len(Assets) != len(cases) {
		t.Errorf("Assets has %d entries, want %d", len(Assets), len(cases))
	}
}

func TestAssetForUnsupported(t *testing.T) {
	for _, tc := range [][2]string{{"plan9", "amd64"}, {"linux", "mips"}, {"freebsd", "amd64"}} {
		if _, err := AssetFor(tc[0], tc[1]); err == nil {
			t.Errorf("AssetFor(%s,%s) accepted an unsupported platform", tc[0], tc[1])
		} else if !strings.Contains(err.Error(), "supported") {
			t.Errorf("error should list supported platforms, got: %v", err)
		}
	}
}

func TestModelsHavePins(t *testing.T) {
	for _, m := range []Model{EmbeddingModel, RerankModel} {
		if m.File == "" || !strings.HasPrefix(m.URL, "https://") || m.Size <= 0 || len(m.SHA256) != 64 {
			t.Errorf("model not fully pinned: %+v", m)
		}
		if !strings.HasSuffix(m.URL, "/"+m.File) {
			t.Errorf("model URL %q does not end with its file name %q", m.URL, m.File)
		}
	}
}

// --- downloader ------------------------------------------------------------

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newTestDownloader() *Downloader {
	return &Downloader{
		Client:    &http.Client{Timeout: 10 * time.Second},
		UserAgent: "mem/test",
		Attempts:  3,
		Backoff:   time.Millisecond,
	}
}

func TestFetchHappyPathAndAtomicInstall(t *testing.T) {
	payload := bytes.Repeat([]byte("mem-bytes-"), 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "mem/test" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "file.bin")
	d := newTestDownloader()
	if err := d.Fetch(srv.URL, dest, int64(len(payload)), shaOf(payload)); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("downloaded content differs")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("part file survived the install")
	}
}

func TestFetchSkipsValidExistingFile(t *testing.T) {
	payload := []byte("already here")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(dest, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := newTestDownloader().Fetch(srv.URL, dest, int64(len(payload)), shaOf(payload)); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("server was contacted %d times for a valid file", n)
	}
}

func TestFetchResumesFromPartFile(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789"), 500)
	const have = 2000
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "" {
			t.Errorf("expected a Range request, got none")
			w.Write(payload)
			return
		}
		if want := fmt.Sprintf("bytes=%d-", have); rng != want {
			t.Errorf("Range = %q, want %q", rng, want)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", have, len(payload)-1, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)-have))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload[have:])
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	if err := os.WriteFile(dest+".part", payload[:have], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := newTestDownloader().Fetch(srv.URL, dest, int64(len(payload)), shaOf(payload)); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("resumed content mismatch: got %d bytes", len(got))
	}
}

func TestFetchHashMismatchRemovesPart(t *testing.T) {
	payload := []byte("the wrong bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	d := newTestDownloader()
	d.Attempts = 1
	err := d.Fetch(srv.URL, dest, 0, strings.Repeat("ab", 32))
	if err == nil {
		t.Fatal("Fetch accepted a hash mismatch")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("error = %v, want a sha256 mismatch", err)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("mismatched part file was left behind")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("mismatched file was installed")
	}
}

func TestFetchRetriesThenSucceeds(t *testing.T) {
	payload := []byte("flaky but fine")
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := newTestDownloader().Fetch(srv.URL, dest, int64(len(payload)), shaOf(payload)); err != nil {
		t.Fatalf("Fetch after retries: %v", err)
	}
	if got := atomic.LoadInt32(&n); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestFetchGivesUpAfterAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	d := newTestDownloader()
	d.Attempts = 2
	err := d.Fetch(srv.URL, filepath.Join(t.TempDir(), "file.bin"), 0, "")
	if err == nil {
		t.Fatal("Fetch succeeded against a failing server")
	}
	if !strings.Contains(err.Error(), "2 attempts") {
		t.Fatalf("error = %v, want the attempt count", err)
	}
}

func TestFetchSizeMismatch(t *testing.T) {
	payload := []byte("short")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	d := newTestDownloader()
	d.Attempts = 1
	dest := filepath.Join(t.TempDir(), "f.bin")
	err := d.Fetch(srv.URL, dest, 999, "")
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("err = %v, want a size mismatch", err)
	}
	if _, serr := os.Stat(dest); !os.IsNotExist(serr) {
		t.Fatal("size-mismatched file was installed")
	}
}

// --- archives --------------------------------------------------------------

func makeZip(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeTarGz(t *testing.T, members map[string][]byte, modes map[string]int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range members {
		mode := int64(0o644)
		if modes != nil {
			if m, ok := modes[name]; ok {
				mode = m
			}
		}
		hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallArchiveZipKeepsOnlySidecarFiles(t *testing.T) {
	data := makeZip(t, map[string][]byte{
		"llama-server.exe":     []byte("exe"),
		"llama.dll":            []byte("lib"),
		"ggml-base.dll":        []byte("lib"),
		"ggml-cpu-haswell.dll": []byte("lib"),
		"libomp.dll":           []byte("lib"),
		"llama-bench.exe":      []byte("unwanted tool"),
		"README.md":            []byte("unwanted doc"),
		"LICENSE-LLVM-OpenMP":  []byte("licence"),
	})
	archive := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(archive, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	written, err := InstallArchive(archive, dest)
	if err != nil {
		t.Fatalf("InstallArchive: %v", err)
	}
	want := map[string]bool{
		"llama-server.exe": true, "llama.dll": true, "ggml-base.dll": true,
		"ggml-cpu-haswell.dll": true, "libomp.dll": true, "LICENSE-LLVM-OpenMP": true,
	}
	if len(written) != len(want) {
		t.Fatalf("written = %v, want %d files", written, len(want))
	}
	for _, name := range written {
		if !want[name] {
			t.Errorf("unexpected extracted file %q", name)
		}
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Errorf("file %q missing on disk: %v", name, err)
		}
	}
	for _, unwanted := range []string{"llama-bench.exe", "README.md"} {
		if _, err := os.Stat(filepath.Join(dest, unwanted)); !os.IsNotExist(err) {
			t.Errorf("%q should not have been extracted", unwanted)
		}
	}
}

func TestInstallArchiveTarGzFlattensAndKeepsMode(t *testing.T) {
	data := makeTarGz(t, map[string][]byte{
		"llama-b12503/llama-server":             []byte("exe"),
		"llama-b12503/libllama.so.1.0.0":        []byte("lib"),
		"llama-b12503/libggml-cpu-alderlake.so": []byte("lib"),
		"llama-b12503/llama-bench":              []byte("unwanted"),
	}, map[string]int64{"llama-b12503/llama-server": 0o755})
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(archive, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	written, err := InstallArchive(archive, dest)
	if err != nil {
		t.Fatalf("InstallArchive: %v", err)
	}
	want := map[string]bool{"llama-server": true, "libllama.so.1.0.0": true, "libggml-cpu-alderlake.so": true}
	if len(written) != len(want) {
		t.Fatalf("written = %v", written)
	}
	for _, name := range written {
		if !want[name] {
			t.Errorf("unexpected extracted file %q", name)
		}
	}
	info, err := os.Stat(filepath.Join(dest, "llama-server"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Errorf("llama-server lost its executable bit: %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(dest, "llama-bench")); !os.IsNotExist(err) {
		t.Error("llama-bench should not have been extracted")
	}
}

func TestInstallArchiveRejectsTraversalAndUnknownFormat(t *testing.T) {
	data := makeTarGz(t, map[string][]byte{"../../evil": []byte("x")}, nil)
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(archive, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallArchive(archive, t.TempDir()); err == nil {
		t.Fatal("archive traversal was accepted")
	}

	if _, err := InstallArchive(filepath.Join(t.TempDir(), "a.rar"), t.TempDir()); err == nil {
		t.Fatal("unknown archive format was accepted")
	}
}

func TestWantMember(t *testing.T) {
	cases := map[string]bool{
		"llama-server":              true,
		"llama-server.exe":          true,
		"llama-b12503/llama-server": true,
		"libllama.so.1.0.0":         true,
		"libggml-metal.dylib":       true,
		"ggml-vulkan.dll":           true,
		"mtmd.dll":                  true,
		"libmtmd.1.dylib":           true,
		"libomp.dll":                true,
		"llama-cli":                 false,
		"README.md":                 false,
		"LICENSE":                   false,
		"llama-bench.exe":           false,
		"ggml-rpc-server.exe":       false,
	}
	for name, want := range cases {
		if got := wantMember(name); got != want {
			t.Errorf("wantMember(%q) = %v, want %v", name, got, want)
		}
	}
}
