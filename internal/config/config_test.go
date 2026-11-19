package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// withConfigFile points MEM_CONFIG at a temp file for the duration of a test.
func withConfigFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("MEM_CONFIG", path)
	t.Setenv("MEM_DATA_DIR", "")
	return path
}

func TestLoadDefaultsWhenMissing(t *testing.T) {
	withConfigFile(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Fatalf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.DefaultTempExpiryDays != DefaultTempExpiryDays {
		t.Fatalf("DefaultTempExpiryDays = %d, want %d", cfg.DefaultTempExpiryDays, DefaultTempExpiryDays)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	wantData := filepath.Join(home, ".mem", "data")
	if cfg.DataDir != wantData {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, wantData)
	}
	if cfg.ModelsDir != filepath.Join(home, ".mem", "models") {
		t.Fatalf("ModelsDir = %q", cfg.ModelsDir)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := withConfigFile(t)
	want := Config{
		DataDir:               filepath.Join(t.TempDir(), "data"),
		ModelsDir:             filepath.Join(t.TempDir(), "models"),
		Port:                  8080,
		DefaultTempExpiryDays: 3,
		IdleTimeoutSeconds:    90,
		EmbedModel:            "custom-embed.gguf",
		Rerank:                true,
		RerankModel:           "custom-rerank.gguf",
		SidecarPath:           "/opt/llama/llama-server",
		AllowedHosts:          []string{"tunnel.ngrok.io", "api.example.com"},
	}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not written to MEM_CONFIG path: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, key := range []string{"data_dir", "models_dir", "port", "default_temp_expiry_days", "idle_timeout_seconds", "embed_model", "rerank", "rerank_model", "sidecar_path", "allowed_hosts"} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("config file missing %q:\n%s", key, raw)
		}
	}
}

func TestSaveIsAtomic(t *testing.T) {
	path := withConfigFile(t)
	first := Config{DataDir: "/a", ModelsDir: "/b", Port: 1, DefaultTempExpiryDays: 2}
	if err := first.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No temp files may survive a successful save.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
}

func TestLoadPartialFileKeepsDefaults(t *testing.T) {
	path := withConfigFile(t)
	if err := os.WriteFile(path, []byte("port = 9000\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9000 {
		t.Fatalf("Port = %d, want 9000", cfg.Port)
	}
	if cfg.DataDir == "" || cfg.ModelsDir == "" {
		t.Fatalf("defaults were not preserved: %+v", cfg)
	}
	if cfg.DefaultTempExpiryDays != DefaultTempExpiryDays {
		t.Fatalf("DefaultTempExpiryDays = %d", cfg.DefaultTempExpiryDays)
	}
}

func TestEnvOverrides(t *testing.T) {
	path := withConfigFile(t)
	if err := os.WriteFile(path, []byte("data_dir = \"/from/file\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	override := filepath.Join(t.TempDir(), "from-env")
	t.Setenv("MEM_DATA_DIR", override)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != override {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, override)
	}
}

func TestLoadInvalidFile(t *testing.T) {
	path := withConfigFile(t)
	if err := os.WriteFile(path, []byte("this is = not toml [[[\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an invalid config file")
	}
}

func TestPathUsesUserConfigDir(t *testing.T) {
	t.Setenv("MEM_CONFIG", "")
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(dir, AppName, "config.toml")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}
