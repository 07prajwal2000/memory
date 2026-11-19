package skills_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memory/internal/cli"
	"memory/skills"
)

func runCLI(t *testing.T, args []string, dir string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	in := strings.NewReader("")

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir %s: %v", dir, err)
		}
		defer func() { _ = os.Chdir(origWd) }()
	}

	io := cli.IO{
		In:  in,
		Out: &stdout,
		Err: &stderr,
	}

	code := cli.Run(args, io, cli.DefaultEnv())
	return code, stdout.String(), stderr.String()
}

func TestSkillCLI_StdoutExactBytes(t *testing.T) {
	code, stdout, stderr := runCLI(t, []string{"skill"}, "")
	if code != cli.ExitOK {
		t.Fatalf("exit code = %d (stderr=%s), want %d", code, stderr, cli.ExitOK)
	}

	if !bytes.Equal([]byte(stdout), skills.SkillMD) {
		t.Fatalf("stdout mismatch: got %d bytes, want %d bytes", len(stdout), len(skills.SkillMD))
	}
}

func TestSkillCLI_JSONOutput(t *testing.T) {
	code, stdout, stderr := runCLI(t, []string{"skill", "--json"}, "")
	if code != cli.ExitOK {
		t.Fatalf("exit code = %d (stderr=%s), want %d", code, stderr, cli.ExitOK)
	}

	var data struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Bytes       int    `json:"bytes"`
		SHA256      string `json:"sha256"`
	}

	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		t.Fatalf("unmarshal json error: %v\noutput: %s", err, stdout)
	}

	if data.Name != "mem" {
		t.Errorf("name = %q, want 'mem'", data.Name)
	}
	if data.Version != "0.1.0" {
		t.Errorf("version = %q, want '0.1.0'", data.Version)
	}
	if data.Bytes != len(skills.SkillMD) {
		t.Errorf("bytes = %d, want %d", data.Bytes, len(skills.SkillMD))
	}
	if data.SHA256 != skills.SHA256() {
		t.Errorf("sha256 = %q, want %q", data.SHA256, skills.SHA256())
	}
}

func TestSkillCLI_InstallToAgent(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Initial install with --agent claude-code
	code, stdout, stderr := runCLI(t, []string{"skill", "--install", "--agent", "claude-code"}, tmpDir)
	if code != cli.ExitOK {
		t.Fatalf("initial install exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "installed") {
		t.Errorf("stdout expected to contain 'installed', got: %s", stdout)
	}

	targetFile := filepath.Join(tmpDir, ".claude", "skills", "mem", "SKILL.md")
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("reading installed skill file: %v", err)
	}
	if !bytes.Equal(content, skills.SkillMD) {
		t.Fatalf("installed content does not match SkillMD")
	}

	// 2. Re-run: identical file => already up to date (no-op)
	code, stdout, stderr = runCLI(t, []string{"skill", "--install", "--agent", "claude-code"}, tmpDir)
	if code != cli.ExitOK {
		t.Fatalf("second install exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "already up to date") {
		t.Errorf("stdout expected to contain 'already up to date', got: %s", stdout)
	}

	// 3. Conflict detection: modify the file
	differingText := "# Customized locally\n"
	if err := os.WriteFile(targetFile, []byte(differingText), 0o644); err != nil {
		t.Fatalf("writing differing file: %v", err)
	}

	code, stdout, _ = runCLI(t, []string{"skill", "--install", "--agent", "claude-code"}, tmpDir)
	if code != cli.ExitConflict {
		t.Fatalf("expected conflict exit code %d, got %d", cli.ExitConflict, code)
	}
	if !strings.Contains(stdout, "differs from embedded skill") || !strings.Contains(stdout, "--force") {
		t.Errorf("expected diff summary and force hint, got: %s", stdout)
	}

	// 4. Force overwrite
	code, stdout, stderr = runCLI(t, []string{"skill", "--install", "--agent", "claude-code", "--force"}, tmpDir)
	if code != cli.ExitOK {
		t.Fatalf("force install exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "updated") {
		t.Errorf("expected 'updated', got: %s", stdout)
	}

	contentAfterForce, _ := os.ReadFile(targetFile)
	if !bytes.Equal(contentAfterForce, skills.SkillMD) {
		t.Fatalf("force overwrite did not restore exact SkillMD bytes")
	}
}

func TestSkillCLI_AutoDetectAgents(t *testing.T) {
	tmpDir := t.TempDir()

	// With no agent directories, should report no agents detected
	code, stdout, stderr := runCLI(t, []string{"skill", "--install"}, tmpDir)
	if code != cli.ExitOK {
		t.Fatalf("exit code = %d (stderr=%s), want ExitOK", code, stderr)
	}
	if !strings.Contains(stdout, "no supported agents detected") {
		t.Errorf("expected 'no supported agents detected', got: %s", stdout)
	}

	// Create .agents directory to simulate universal agent
	if err := os.Mkdir(filepath.Join(tmpDir, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir .agents: %v", err)
	}

	code, stdout, stderr = runCLI(t, []string{"skill", "--install"}, tmpDir)
	if code != cli.ExitOK {
		t.Fatalf("exit code = %d (stderr=%s), want ExitOK", code, stderr)
	}
	if !strings.Contains(stdout, "installed") {
		t.Errorf("expected install output, got: %s", stdout)
	}

	universalFile := filepath.Join(tmpDir, ".agents", "skills", "mem", "SKILL.md")
	if _, err := os.Stat(universalFile); err != nil {
		t.Errorf("expected %s to exist: %v", universalFile, err)
	}
}
