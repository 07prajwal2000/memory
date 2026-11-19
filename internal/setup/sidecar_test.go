package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeExecutable builds a byte slice with a real executable header for the
// current platform.
func fakeExecutable(t *testing.T, size int) []byte {
	t.Helper()
	if size < 256 {
		size = 256
	}
	buf := make([]byte, size)
	// Fill the body, leaving the header window at both ends intact: on Windows
	// the PE header sits at 0x80, and the DOS header uses 0x3C for that offset.
	switch runtime.GOOS {
	case "windows":
		for i := 0x40; i < len(buf); i++ {
			if i >= 0x80 && i < 0x86 {
				continue
			}
			buf[i] = byte(i % 251)
		}
	default:
		for i := 4; i < len(buf); i++ {
			buf[i] = byte(i % 251)
		}
	}
	switch runtime.GOOS {
	case "windows":
		buf[0], buf[1] = 'M', 'Z'
		peOff := 0x80
		buf[0x3c] = byte(peOff)
		copy(buf[peOff:], []byte("PE\x00\x00"))
		var machine uint16
		switch runtime.GOARCH {
		case "arm64":
			machine = 0xaa64
		default:
			machine = 0x8664
		}
		buf[peOff+4] = byte(machine)
		buf[peOff+5] = byte(machine >> 8)
	case "darwin":
		// 64-bit little-endian Mach-O magic, then cputype.
		copy(buf, []byte{0xcf, 0xfa, 0xed, 0xfe})
		cpu := uint32(0x01000007) // x86-64
		if runtime.GOARCH == "arm64" {
			cpu = 0x0100000c
		}
		buf[4], buf[5], buf[6], buf[7] = byte(cpu), byte(cpu>>8), byte(cpu>>16), byte(cpu>>24)
	default:
		// ELF64 little-endian.
		copy(buf, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
		if runtime.GOARCH == "arm64" {
			buf[18], buf[19] = 183, 0 // EM_AARCH64
		} else {
			buf[18], buf[19] = 62, 0 // EM_X86_64
		}
	}
	return buf
}

func fakeLibName(goos string) string {
	switch goos {
	case "windows":
		return "llama.dll"
	case "darwin":
		return "libllama.dylib"
	default:
		return "libllama.so"
	}
}

func shaOfBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestInspectBinaryRejectsTheRealWorldFailure(t *testing.T) {
	dir := t.TempDir()

	// The exact file the owner hit: an 8-byte placeholder left by a broken test.
	stub := filepath.Join(dir, "llama-server-stub.exe")
	if err := os.WriteFile(stub, []byte("fake exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := InspectBinary(stub)
	if err == nil {
		t.Fatal("an 8-byte stub must be rejected")
	}
	if !strings.Contains(err.Error(), "not a recognised executable") {
		t.Errorf("error should name the executable failure, got: %v", err)
	}
	var bp BinaryProblem
	if !asBinaryProblem(err, &bp) || !bp.NeedsReinstall {
		t.Errorf("stub should be flagged for reinstall: %v", err)
	}

	// A missing file and an empty path are also "reinstall".
	if err := InspectBinary(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing binary accepted")
	}
	if err := InspectBinary(""); err == nil {
		t.Error("empty path accepted")
	}

	// Without companion libraries, even a valid binary is rejected.
	good := filepath.Join(dir, "llama-server-good")
	if err := os.WriteFile(good, fakeExecutable(t, 9728), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InspectBinary(good); err == nil {
		t.Fatal("expected binary without companion libraries to be rejected")
	}

	// With companion library, it is accepted.
	if err := os.WriteFile(filepath.Join(dir, fakeLibName(runtime.GOOS)), []byte("lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InspectBinary(good); err != nil {
		t.Fatalf("a plausible executable with companion library was rejected: %v", err)
	}

	// A file with no recognisable header is rejected.
	junk := filepath.Join(dir, "junk.bin")
	junkBuf := make([]byte, 2048)
	for i := range junkBuf {
		junkBuf[i] = 0x41
	}
	if err := os.WriteFile(junk, junkBuf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InspectBinary(junk); err == nil {
		t.Error("a file with no executable header was accepted")
	}
}

func TestInspectBinaryRejectsWrongArchitecture(t *testing.T) {
	dir := t.TempDir()
	buf := fakeExecutable(t, 2048)

	// Rewrite the machine field to the architecture this test is not running on.
	switch runtime.GOOS {
	case "windows":
		other := uint16(0xaa64)
		if runtime.GOARCH == "arm64" {
			other = 0x8664
		}
		buf[0x84] = byte(other)
		buf[0x85] = byte(other >> 8)
	case "darwin":
		cpu := uint32(0x01000007) // claim x86-64
		if runtime.GOARCH != "arm64" {
			cpu = 0x0100000c // claim arm64
		}
		buf[4], buf[5], buf[6], buf[7] = byte(cpu), byte(cpu>>8), byte(cpu>>16), byte(cpu>>24)
	default:
		if runtime.GOARCH == "arm64" {
			buf[18], buf[19] = 62, 0 // claim x86-64
		} else {
			buf[18], buf[19] = 183, 0 // claim aarch64
		}
	}
	path := filepath.Join(dir, "wrong-arch")
	if err := os.WriteFile(path, buf, 0o755); err != nil {
		t.Fatal(err)
	}
	err := InspectBinary(path)
	if err == nil {
		t.Fatal("a binary for another architecture was accepted")
	}
	if !strings.Contains(err.Error(), "this machine is") {
		t.Errorf("error should mention the mismatch, got: %v", err)
	}
}

func TestMarkerHashMismatchTriggersReinstall(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, nil)

	if _, err := Run(opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	binPath := filepath.Join(opts.ModelsDir, "..", "bin", llamaServerName(runtime.GOOS))
	m, err := ReadMarker(binPath)
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	if len(m.SHA256) != 64 || m.Asset != f.asset.Name || m.Size == 0 {
		t.Fatalf("marker is incomplete: %+v", m)
	}

	// Replace the binary with something that still looks like an executable but
	// has different content: a corrupted install.
	tampered := fakeExecutable(t, 9728)
	if err := os.WriteFile(binPath, tampered, 0o755); err != nil {
		t.Fatal(err)
	}
	amended := m
	amended.SHA256 = shaOfBytes(fakeExecutable(t, 4096)) // deliberately wrong
	if err := WriteMarker(binPath, amended); err != nil {
		t.Fatal(err)
	}
	if st := CheckSidecar(binPath); st.OK {
		t.Fatal("a hash mismatch must not report ok")
	} else if !st.NeedsReinstall {
		t.Error("hash mismatch should require a reinstall")
	}

	// A plain re-run must detect the mismatch and reinstall.
	again := opts
	again.Out = &discard{}
	again.Err = &discard{}
	res, err := Run(again)
	if err != nil {
		t.Fatalf("re-run after tampering: %v", err)
	}
	if len(res.Downloaded) == 0 {
		t.Fatal("a hash mismatch must trigger a reinstall, but nothing was reinstalled")
	}
	repaired, err := ReadMarker(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.SHA256 == amended.SHA256 {
		t.Fatal("the marker was not refreshed after the reinstall")
	}
	if st := CheckSidecar(binPath); !st.OK {
		t.Fatalf("reinstalled binary still invalid: %+v", st)
	}
}

func TestMarkerStubBinaryIsDetectedAsInvalid(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(binDir, llamaServerName(runtime.GOOS))
	if err := os.WriteFile(binPath, []byte("fake exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(binPath, Marker{Asset: "llama-test.zip", Size: 8, SHA256: shaOfBytes([]byte("fake exe"))}); err != nil {
		t.Fatal(err)
	}

	// Even with a self-consistent marker, the header check rejects it.
	st := CheckSidecar(binPath)
	if st.OK {
		t.Fatalf("an 8-byte stub was reported healthy: %+v", st)
	}
	if !strings.Contains(st.Problem, "not a recognised executable") {
		t.Errorf("problem should indicate unrecognised executable: %q", st.Problem)
	}
	if !st.NeedsReinstall {
		t.Error("a stub must require a reinstall")
	}
}

func TestRealSizeLauncherPassesAndStubFails(t *testing.T) {
	dir := t.TempDir()

	// 1. An 8-byte fake binary ("fake exe") fails.
	fakePath := filepath.Join(dir, "stub-launcher")
	if err := os.WriteFile(fakePath, []byte("fake exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InspectBinary(fakePath); err == nil {
		t.Fatal("an 8-byte fake executable must be rejected")
	}
	if st := CheckSidecar(fakePath); st.OK || !st.NeedsReinstall {
		t.Fatalf("CheckSidecar on 8-byte fake must fail with needs_reinstall: %+v", st)
	}

	// 2. Real-size 9,728-byte binary (valid PE/ELF/Mach-O header for this platform).
	realDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realBin := filepath.Join(realDir, llamaServerName(runtime.GOOS))
	realBytes := fakeExecutable(t, 9728)
	if len(realBytes) != 9728 {
		t.Fatalf("expected exactly 9728 bytes, got %d", len(realBytes))
	}
	if err := os.WriteFile(realBin, realBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without companion library, InspectBinary and CheckSidecar must reject it.
	if err := InspectBinary(realBin); err == nil {
		t.Fatal("9,728-byte binary without companion libraries must fail InspectBinary")
	}
	if st := CheckSidecar(realBin); st.OK {
		t.Fatal("9,728-byte binary without companion libraries must fail CheckSidecar")
	}

	// Add companion library next to it.
	libPath := filepath.Join(realDir, fakeLibName(runtime.GOOS))
	if err := os.WriteFile(libPath, []byte("companion library bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	// With companion library, InspectBinary must pass.
	if err := InspectBinary(realBin); err != nil {
		t.Fatalf("9,728-byte binary with companion library must pass InspectBinary: %v", err)
	}

	// CheckSidecar with no marker (hand-installed) must report OK.
	stNoMarker := CheckSidecar(realBin)
	if !stNoMarker.OK {
		t.Fatalf("CheckSidecar on hand-installed 9,728-byte binary must report OK: %+v", stNoMarker)
	}

	// CheckSidecar with matching marker SHA256 must report OK and MarkerOK.
	validMarker := Marker{
		Asset:  "llama-release.zip",
		Size:   int64(len(realBytes)),
		SHA256: shaOfBytes(realBytes),
	}
	if err := WriteMarker(realBin, validMarker); err != nil {
		t.Fatal(err)
	}
	stWithMarker := CheckSidecar(realBin)
	if !stWithMarker.OK || !stWithMarker.MarkerOK {
		t.Fatalf("CheckSidecar with matching marker must report OK and MarkerOK: %+v", stWithMarker)
	}

	// CheckSidecar with mismatched marker SHA256 must report broken and require reinstall.
	tamperedMarker := validMarker
	tamperedMarker.SHA256 = strings.Repeat("a", 64)
	if err := WriteMarker(realBin, tamperedMarker); err != nil {
		t.Fatal(err)
	}
	stTampered := CheckSidecar(realBin)
	if stTampered.OK || !stTampered.NeedsReinstall {
		t.Fatalf("CheckSidecar with mismatched marker must fail and require reinstall: %+v", stTampered)
	}
}

func TestForceReinstallsValidFiles(t *testing.T) {
	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, nil)
	if _, err := Run(opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// A normal re-run skips everything.
	normal := opts
	normal.Out = &discard{}
	normal.Err = &discard{}
	res, err := Run(normal)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(res.Downloaded) != 0 || len(res.Skipped) == 0 {
		t.Fatalf("idempotent re-run = %+v, want everything skipped", res)
	}

	// --force reinstalls both the sidecar and the model.
	forced := opts
	forced.Force = true
	forced.Out = &discard{}
	forced.Err = &discard{}
	res, err = Run(forced)
	if err != nil {
		t.Fatalf("forced Run: %v", err)
	}
	if len(res.Downloaded) < 2 {
		t.Fatalf("--force reinstalled only %v", res.Downloaded)
	}
}

func TestCheckModelVerifiesPinnedFiles(t *testing.T) {
	dir := t.TempDir()
	model := Model{File: "m.gguf", Size: 5, SHA256: shaOfBytes([]byte("hello"))}

	if st := CheckModel(filepath.Join(dir, "absent.gguf"), model); st.OK || st.Present {
		t.Errorf("absent model = %+v, want present=false ok=false", st)
	}

	if err := os.WriteFile(filepath.Join(dir, "m.gguf"), []byte("hellO"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := CheckModel(filepath.Join(dir, "m.gguf"), model)
	if st.OK || !st.Present {
		t.Errorf("corrupt model = %+v, want present=true ok=false", st)
	}
	if !strings.Contains(st.Problem, "sha256") {
		t.Errorf("problem should mention the hash: %q", st.Problem)
	}

	if err := os.WriteFile(filepath.Join(dir, "m.gguf"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := CheckModel(filepath.Join(dir, "m.gguf"), model); !st.OK {
		t.Errorf("valid model rejected: %+v", st)
	}
}

// TestInitWithEmptyDirsStaysUnderIsolatedHome proves the TestMain isolation is
// effective: Run with empty dirs resolves the defaults, which must land inside
// the temporary home rather than the developer's real one.
func TestInitWithEmptyDirsStaysUnderIsolatedHome(t *testing.T) {
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		t.Skip("no home is set in this environment")
	}
	if !strings.Contains(strings.ToLower(home), "mem-test-home-") {
		t.Fatalf("test home is not isolated (%q); TestMain did not run", home)
	}
	cfgPath := os.Getenv("MEM_CONFIG")
	if !strings.Contains(strings.ToLower(cfgPath), "mem-test-home-") {
		t.Fatalf("MEM_CONFIG is not isolated: %q", cfgPath)
	}
	// Both dirs must share the same per-binary isolation root.
	root := filepath.Dir(filepath.Dir(home)) // <root>/home -> <root>
	if !strings.HasPrefix(strings.ToLower(cfgPath), strings.ToLower(root)) {
		t.Fatalf("MEM_CONFIG %q is outside the isolation root %q", cfgPath, root)
	}

	f := newOfflineFixture(t, runtime.GOOS, runtime.GOARCH)
	opts := f.initOptions(t, true, func(o *Options) {
		// Empty dirs are exactly what leaked into the real home before.
		o.DataDir = ""
		o.ModelsDir = ""
		o.ConfigPath = ""
	})
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, dir := range []string{res.DataDir, res.ModelsDir, res.BinDir, res.ConfigPath} {
		if !strings.HasPrefix(strings.ToLower(dir), strings.ToLower(root)) {
			t.Errorf("%q escaped the isolated test root %q", dir, root)
		}
	}
}

// discard is an io.Writer that drops everything, for quiet repeated Runs.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// asBinaryProblem is errors.As specialized for BinaryProblem, avoiding an import
// in the test body.
func asBinaryProblem(err error, target *BinaryProblem) bool {
	for err != nil {
		if bp, ok := err.(BinaryProblem); ok {
			*target = bp
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
