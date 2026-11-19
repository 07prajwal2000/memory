package setup

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// markerFileName records what `mem init` installed, next to the binary.
const markerFileName = ".mem-release"

// minSidecarSize is the smallest plausible llama-server. The real CPU builds are
// roughly 10-25 MB; anything under this is a stub, a truncated file or a
// placeholder written by a broken test or an interrupted copy.
const minSidecarSize = 1 << 20 // 1 MiB

// Marker describes an installed sidecar. It is written after a successful
// extraction and re-validated on every init and in `mem status`, so a corrupted
// or replaced binary is reinstalled instead of being trusted forever.
type Marker struct {
	Asset  string `json:"asset"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BinaryProblem explains why an installed sidecar cannot be used.
type BinaryProblem struct {
	Path   string
	Reason string
	// NeedsReinstall is true when `mem init --force` is the right fix.
	NeedsReinstall bool
}

func (p BinaryProblem) Error() string {
	return fmt.Sprintf("llama-server %s: %s", p.Path, p.Reason)
}

// InspectBinary performs the cheap, offline checks used before trusting a
// llama-server binary: it must exist, be at least minSidecarSize bytes, and start
// with an executable header for this OS and the current CPU architecture.
//
// It deliberately does not execute the binary.
func InspectBinary(path string) error {
	if path == "" {
		return BinaryProblem{Path: path, Reason: "path is empty", NeedsReinstall: true}
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BinaryProblem{Path: path, Reason: "not found", NeedsReinstall: true}
		}
		return BinaryProblem{Path: path, Reason: err.Error(), NeedsReinstall: true}
	}
	if info.IsDir() {
		return BinaryProblem{Path: path, Reason: "is a directory", NeedsReinstall: true}
	}
	if info.Size() < minSidecarSize {
		return BinaryProblem{
			Path:           path,
			Reason:         fmt.Sprintf("is only %d bytes; a real llama-server is several megabytes (corrupt, incomplete or a placeholder)", info.Size()),
			NeedsReinstall: true,
		}
	}
	arch, osName, err := executableTarget(path)
	if err != nil {
		return BinaryProblem{Path: path, Reason: fmt.Sprintf("is not a recognised executable: %v", err), NeedsReinstall: true}
	}
	if osName != "" && osName != runtime.GOOS {
		return BinaryProblem{Path: path, Reason: fmt.Sprintf("is a %s binary, not %s", osName, runtime.GOOS), NeedsReinstall: true}
	}
	if arch != "" && !archMatches(arch, runtime.GOARCH) {
		return BinaryProblem{
			Path:           path,
			Reason:         fmt.Sprintf("is built for %s, but this machine is %s", arch, runtime.GOARCH),
			NeedsReinstall: true,
		}
	}
	return nil
}

// archMatches accepts the aliases an executable may use for a GOARCH.
func archMatches(execArch, goarch string) bool {
	if execArch == goarch {
		return true
	}
	pairs := map[string]string{
		"x86_64":  "amd64",
		"x86-64":  "amd64",
		"amd64":   "amd64",
		"aarch64": "arm64",
		"arm64":   "arm64",
	}
	if mapped, ok := pairs[execArch]; ok {
		return mapped == goarch
	}
	return false
}

// executableTarget reads the object header and returns the architecture and OS
// it was built for. Empty strings mean "not stated by this format".
//
// Header fields are read at their fixed offsets rather than through debug/pe,
// debug/elf or debug/macho: those parse the whole image and reject a binary
// whose later sections are damaged, which is not what this check is for. Only
// the first bytes matter here.
func executableTarget(path string) (arch, osName string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	head := make([]byte, 64)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", "", err
	}
	head = head[:n]
	if len(head) < 4 {
		return "", "", errors.New("file is too short to have an executable header")
	}

	switch {
	case head[0] == 'M' && head[1] == 'Z':
		return peTarget(f, head)
	case head[0] == 0x7f && head[1] == 'E' && head[2] == 'L' && head[3] == 'F':
		return elfTarget(head)
	case isMachO(head):
		return machOTarget(head)
	}
	return "", "", fmt.Errorf("unknown file header %#x", head[:4])
}

// peTarget reads the COFF machine of a PE image: e_lfanew at 0x3C points at the
// "PE\0\0" signature and the machine follows it.
func peTarget(f *os.File, head []byte) (arch, osName string, err error) {
	if len(head) < 0x40 {
		return "", "", errors.New("truncated DOS header")
	}
	peOff := uint32(head[0x3c]) | uint32(head[0x3d])<<8 | uint32(head[0x3e])<<16 | uint32(head[0x3f])<<24
	if peOff == 0 || peOff > 1<<20 {
		return "", "", fmt.Errorf("implausible PE header offset %#x", peOff)
	}
	sig := make([]byte, 6)
	if _, err := f.ReadAt(sig, int64(peOff)); err != nil {
		return "", "", fmt.Errorf("read PE header: %w", err)
	}
	if sig[0] != 'P' || sig[1] != 'E' || sig[2] != 0 || sig[3] != 0 {
		return "", "", errors.New("missing PE signature")
	}
	machine := uint16(sig[4]) | uint16(sig[5])<<8
	switch machine {
	case 0x8664:
		return "amd64", "windows", nil
	case 0xaa64:
		return "arm64", "windows", nil
	case 0x014c:
		return "386", "windows", nil
	}
	return fmt.Sprintf("machine %#x", machine), "windows", nil
}

// elfTarget reads e_machine from an ELF header (offset 18, 2 bytes).
func elfTarget(head []byte) (arch, osName string, err error) {
	if len(head) < 20 {
		return "", "", errors.New("truncated ELF header")
	}
	var order binary.ByteOrder = binary.LittleEndian
	if head[5] == 2 { // EI_DATA = big endian
		order = binary.BigEndian
	}
	switch machine := order.Uint16(head[18:20]); machine {
	case 62:
		return "amd64", "linux", nil
	case 183:
		return "arm64", "linux", nil
	case 3:
		return "386", "linux", nil
	default:
		return fmt.Sprintf("e_machine %d", machine), "linux", nil
	}
}

// machOTarget reads cputype from a Mach-O header (offset 4, 4 bytes).
func machOTarget(head []byte) (arch, osName string, err error) {
	if len(head) < 8 {
		return "", "", errors.New("truncated Mach-O header")
	}
	magic := binary.BigEndian.Uint32(head[:4])
	var order binary.ByteOrder = binary.BigEndian
	if magic == 0xcefaedfe || magic == 0xcffaedfe {
		order = binary.LittleEndian
	}
	switch cpu := order.Uint32(head[4:8]); cpu {
	case 0x01000007:
		return "amd64", "darwin", nil
	case 0x0100000c:
		return "arm64", "darwin", nil
	default:
		return fmt.Sprintf("cputype %#x", cpu), "darwin", nil
	}
}

// isMachO reports whether head starts a Mach-O image (32/64 bit, either byte
// order).
func isMachO(head []byte) bool {
	if len(head) < 4 {
		return false
	}
	magic := uint32(head[0])<<24 | uint32(head[1])<<16 | uint32(head[2])<<8 | uint32(head[3])
	switch magic {
	case 0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe:
		return true
	}
	return false
}

// ReadMarker loads the install marker next to a sidecar. A missing or malformed
// marker yields an error, which callers treat as "unknown install".
func ReadMarker(binPath string) (Marker, error) {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binPath), markerFileName))
	if err != nil {
		return Marker{}, err
	}
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		// An older marker was just the asset name on one line.
		name := strings.TrimSpace(string(data))
		if name != "" {
			return Marker{Asset: name}, nil
		}
		return Marker{}, fmt.Errorf("parse install marker: %w", err)
	}
	return m, nil
}

// WriteMarker records an installed sidecar.
func WriteMarker(binPath string, m Marker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(filepath.Dir(binPath), markerFileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write install marker: %w", err)
	}
	return nil
}

// ModelStatus is the result of checking one model file.
type ModelStatus struct {
	File    string `json:"file"`
	Path    string `json:"path"`
	OK      bool   `json:"ok"`
	Present bool   `json:"present"`
	Size    int64  `json:"size,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// CheckModel verifies a model file against a pinned size and hash. A missing
// file is reported as absent rather than broken, so `mem status` can say
// "not downloaded" instead of "corrupt".
func CheckModel(path string, model Model) ModelStatus {
	st := ModelStatus{File: model.File, Path: path}
	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			st.Problem = err.Error()
		}
		return st
	}
	st.Present = true
	st.Size = info.Size()
	if model.Size > 0 && info.Size() != model.Size {
		st.Problem = fmt.Sprintf("%d bytes, expected %d", info.Size(), model.Size)
		return st
	}
	if model.SHA256 != "" {
		got, err := fileSHA256(path)
		if err != nil {
			st.Problem = err.Error()
			return st
		}
		if !strings.EqualFold(got, model.SHA256) {
			st.Problem = fmt.Sprintf("sha256 %s, expected %s", got, model.SHA256)
			return st
		}
	}
	st.OK = true
	return st
}

type SidecarStatus struct {
	Path     string `json:"path"`
	OK       bool   `json:"ok"`
	Problem  string `json:"problem,omitempty"`
	Asset    string `json:"asset,omitempty"`
	Size     int64  `json:"size,omitempty"`
	MarkerOK bool   `json:"marker_ok"`
	// NeedsReinstall is set when `mem init --force` should be suggested.
	NeedsReinstall bool `json:"needs_reinstall"`
}

// CheckSidecar validates a binary and the marker that claims it is installed.
// The binary must be a plausible executable for this platform and, when the
// marker carries a hash, match it.
func CheckSidecar(path string) SidecarStatus {
	st := SidecarStatus{Path: path}
	if err := InspectBinary(path); err != nil {
		st.Problem = err.Error()
		var bp BinaryProblem
		if errors.As(err, &bp) {
			st.NeedsReinstall = bp.NeedsReinstall
		}
		return st
	}
	info, err := os.Stat(path)
	if err != nil {
		st.Problem = err.Error()
		st.NeedsReinstall = true
		return st
	}
	st.Size = info.Size()

	m, err := ReadMarker(path)
	if err != nil {
		// No marker at all: a hand-installed binary. It passed the header check,
		// so accept it; `mem init` will add a marker the next time it runs.
		st.OK = true
		return st
	}
	st.Asset = m.Asset
	if m.SHA256 != "" {
		got, err := fileSHA256(path)
		if err != nil {
			st.Problem = err.Error()
			st.NeedsReinstall = true
			return st
		}
		if !strings.EqualFold(got, m.SHA256) {
			st.Problem = fmt.Sprintf("%s has sha256 %s but the install marker recorded %s", path, got, m.SHA256)
			st.NeedsReinstall = true
			return st
		}
		st.MarkerOK = true
	}
	st.OK = true
	return st
}
