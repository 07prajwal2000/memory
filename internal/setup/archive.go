package setup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// InstallArchive extracts the llama-server binary and the shared libraries it
// needs from a release archive into destDir.
//
// The archives are flat (or hold a single top-level directory) and contain many
// tools and backends we do not use. Only these are kept:
//   - the llama-server executable,
//   - every ggml*/llama*/mtmd* shared library (DLL, .so, .dylib), including the
//     CPU backend variants, and
//   - libomp (the OpenMP runtime the Windows build links against) plus its
//     licence.
//
// Files are written with the mode from the archive (executables stay executable
// on unix), and names are flattened, so llama-server finds its libraries next
// to itself.
func InstallArchive(archivePath, destDir string) ([]string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("create bin dir: %w", err)
	}
	var written []string
	var err error
	switch {
	case strings.HasSuffix(archivePath, ".zip"):
		written, err = extractZip(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".tar.gz"), strings.HasSuffix(archivePath, ".tgz"):
		written, err = extractTarGz(archivePath, destDir)
	default:
		return nil, fmt.Errorf("unsupported archive %q: expected .zip or .tar.gz", filepath.Base(archivePath))
	}
	if err != nil {
		return nil, err
	}
	if len(written) == 0 {
		return nil, fmt.Errorf("archive %s contained no llama-server files", filepath.Base(archivePath))
	}
	return written, nil
}

// wantMember reports whether an archive member is part of the sidecar install.
func wantMember(name string) bool {
	base := strings.ToLower(path.Base(strings.ReplaceAll(name, "\\", "/")))
	if base == "" || strings.HasSuffix(base, "/") {
		return false
	}
	if base == "llama-server" || base == "llama-server.exe" {
		return true
	}
	if base == "libomp.dll" || strings.HasPrefix(base, "license-llvm") {
		return true
	}
	stem, ok := libraryStem(base)
	if !ok {
		return false
	}
	// Shared libraries llama-server loads: ggml*, llama*, mtmd*.
	for _, prefix := range []string{"ggml", "llama", "mtmd"} {
		if strings.HasPrefix(stem, prefix) {
			return true
		}
	}
	return false
}

// libraryStem reduces a shared library file name to its base name, dropping the
// "lib" prefix and any version suffix. It reports false for anything that is
// not a DLL, .so/.so.N[.N[.N]] or .dylib/.N.dylib.
//
//	ggml.dll              -> ggml
//	llama.dll             -> llama
//	libllama.so.1.0.0     -> llama
//	libggml-metal.dylib   -> ggml-metal
//	libmtmd.1.dylib       -> mtmd
func libraryStem(base string) (string, bool) {
	switch path.Ext(base) {
	case ".dll":
		stem := base
		return stem, true
	case ".dylib":
		stem := strings.TrimSuffix(base, ".dylib")
		if i := strings.LastIndex(stem, "."); i >= 0 && isAllDigits(stem[i+1:]) {
			stem = stem[:i]
		}
		return strings.TrimPrefix(stem, "lib"), true
	case ".so":
		stem := strings.TrimSuffix(base, ".so")
		return strings.TrimPrefix(stem, "lib"), true
	}
	// Versioned unix libraries: libllama.so.1, libllama.so.1.0.0.
	if i := strings.Index(base, ".so."); i > 0 && isVersionSuffix(base[i+4:]) {
		return strings.TrimPrefix(base[:i], "lib"), true
	}
	return "", false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isVersionSuffix(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if !isAllDigits(part) {
			return false
		}
	}
	return true
}

func extractZip(archivePath, destDir string) ([]string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	var written []string
	for _, f := range zr.File {
		if !wantMember(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s in zip: %w", f.Name, err)
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		name, err := writeMember(destDir, f.Name, rc, mode)
		rc.Close()
		if err != nil {
			return nil, err
		}
		written = append(written, name)
	}
	return written, nil
}

func extractTarGz(archivePath, destDir string) ([]string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var written []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !wantMember(hdr.Name) {
			continue
		}
		mode := os.FileMode(hdr.Mode).Perm()
		if mode == 0 {
			mode = 0o644
		}
		name, err := writeMember(destDir, hdr.Name, tr, mode)
		if err != nil {
			return nil, err
		}
		written = append(written, name)
	}
	return written, nil
}

// writeMember writes one archive member flat into destDir, refusing traversal.
func writeMember(destDir, memberName string, r io.Reader, mode os.FileMode) (string, error) {
	clean := strings.ReplaceAll(memberName, "\\", "/")
	clean = strings.TrimPrefix(clean, "/")
	base := path.Base(clean)
	if base == "" || base == "." || base == ".." || strings.Contains(base, "/") {
		return "", fmt.Errorf("refusing archive member %q", memberName)
	}
	dest := filepath.Join(destDir, base)
	if err := writeFileAtomic(dest, r, mode); err != nil {
		return "", fmt.Errorf("extract %s: %w", base, err)
	}
	return base, nil
}

func writeFileAtomic(path string, r io.Reader, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mem-extract-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
