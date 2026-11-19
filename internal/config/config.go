// Package config loads and saves the mem configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// AppName is the single place the application name lives.
const AppName = "mem"

// DefaultPort is the port used by the dashboard server.
const DefaultPort = 7777

// DefaultTempExpiryDays is the default lifetime of a temp context.
const DefaultTempExpiryDays = 7

// DefaultIdleTimeoutSeconds is how long a model sidecar stays resident without
// use before it is killed.
const DefaultIdleTimeoutSeconds = 300

// DefaultEmbedModel is the embedding model file inside models_dir.
const DefaultEmbedModel = "bge-small-en-v1.5-q8_0.gguf"

// DefaultRerankModel is the reranker file inside models_dir. Reranking is off
// unless Rerank is set; the tiny reranker measured worse than plain fusion.
const DefaultRerankModel = "jina-reranker-v1-tiny-en-Q8_0.gguf"

// Config is the on-disk configuration.
type Config struct {
	DataDir               string `toml:"data_dir"`
	ModelsDir             string `toml:"models_dir"`
	Port                  int    `toml:"port"`
	DefaultTempExpiryDays int    `toml:"default_temp_expiry_days"`
	Rerank                bool   `toml:"rerank"`
	RerankModel           string `toml:"rerank_model"`
	IdleTimeoutSeconds    int    `toml:"idle_timeout_seconds"`
	SidecarPath           string   `toml:"sidecar_path"`
	EmbedModel            string   `toml:"embed_model"`
	AllowedHosts          []string `toml:"allowed_hosts"`
}

// BinDir is where `mem init` installs the llama-server binary and its shared
// libraries: a "bin" directory next to models_dir.
func (c Config) BinDir() string {
	if c.ModelsDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(c.ModelsDir), "bin")
}

// Path returns the config file path: $MEM_CONFIG or os.UserConfigDir()/mem/config.toml.
func Path() (string, error) {
	if p := os.Getenv("MEM_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, AppName, "config.toml"), nil
}

// Defaults returns the config used when no file exists.
func Defaults() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("locate home dir: %w", err)
	}
	base := filepath.Join(home, "."+AppName)
	return Config{
		DataDir:               filepath.Join(base, "data"),
		ModelsDir:             filepath.Join(base, "models"),
		Port:                  DefaultPort,
		DefaultTempExpiryDays: DefaultTempExpiryDays,
		IdleTimeoutSeconds:    DefaultIdleTimeoutSeconds,
		EmbedModel:            DefaultEmbedModel,
		RerankModel:           DefaultRerankModel,
	}, nil
}

// Load reads the config file, returning defaults for a missing file. The
// MEM_DATA_DIR environment variable overrides data_dir.
func Load() (Config, error) {
	def, err := Defaults()
	if err != nil {
		return Config{}, err
	}
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return loadFrom(path, def)
}

func loadFrom(path string, def Config) (Config, error) {
	cfg := def
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// keep defaults
	case err != nil:
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	default:
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
		// toml leaves absent keys at their existing value, but zero values
		// present in the file must not silently break the app.
		if cfg.DataDir == "" {
			cfg.DataDir = def.DataDir
		}
		if cfg.ModelsDir == "" {
			cfg.ModelsDir = def.ModelsDir
		}
		if cfg.Port == 0 {
			cfg.Port = def.Port
		}
		if cfg.DefaultTempExpiryDays == 0 {
			cfg.DefaultTempExpiryDays = def.DefaultTempExpiryDays
		}
		if cfg.IdleTimeoutSeconds == 0 {
			cfg.IdleTimeoutSeconds = def.IdleTimeoutSeconds
		}
		if cfg.EmbedModel == "" {
			cfg.EmbedModel = def.EmbedModel
		}
		if cfg.RerankModel == "" {
			cfg.RerankModel = def.RerankModel
		}
	}
	if v := os.Getenv("MEM_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	return cfg, nil
}

// LoadFile reads the config at an explicit path, returning defaults when the
// file is missing.
func LoadFile(path string) (Config, error) {
	def, err := Defaults()
	if err != nil {
		return Config{}, err
	}
	return loadFrom(path, def)
}

// Save writes the config atomically, creating parent directories.
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	return c.SaveFile(path)
}

// SaveFile writes the config to an explicit path atomically.
func (c Config) SaveFile(path string) error {
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+AppName+"-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// String renders the config for humans.
func (c Config) String() string {
	s := "data_dir=" + c.DataDir +
		" models_dir=" + c.ModelsDir +
		" port=" + strconv.Itoa(c.Port) +
		" default_temp_expiry_days=" + strconv.Itoa(c.DefaultTempExpiryDays) +
		" idle_timeout_seconds=" + strconv.Itoa(c.IdleTimeoutSeconds) +
		" embed_model=" + c.EmbedModel +
		" rerank=" + strconv.FormatBool(c.Rerank) +
		" rerank_model=" + c.RerankModel +
		" sidecar_path=" + c.SidecarPath
	if len(c.AllowedHosts) > 0 {
		s += " allowed_hosts=" + strings.Join(c.AllowedHosts, ",")
	}
	return s
}

// Keys returns the settable config keys in display order.
func Keys() []string {
	return []string{
		"data_dir", "models_dir", "port", "default_temp_expiry_days",
		"idle_timeout_seconds", "embed_model", "rerank", "rerank_model",
		"sidecar_path", "allowed_hosts",
	}
}

// Set validates and assigns one config key. An unknown key is reported together
// with the list of valid keys so `mem config` can print a fix.
func (c *Config) Set(key, value string) error {
	atoi := func(name string, min, max int) (int, error) {
		n, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("%s must be a whole number, got %q", name, value)
		}
		if n < min || (max > 0 && n > max) {
			if max > 0 {
				return 0, fmt.Errorf("%s must be between %d and %d, got %d", name, min, max, n)
			}
			return 0, fmt.Errorf("%s must be at least %d, got %d", name, min, n)
		}
		return n, nil
	}
	switch key {
	case "data_dir":
		c.DataDir = value
	case "models_dir":
		c.ModelsDir = value
	case "port":
		n, err := atoi("port", 1, 65535)
		if err != nil {
			return err
		}
		c.Port = n
	case "default_temp_expiry_days":
		n, err := atoi("default_temp_expiry_days", 1, 0)
		if err != nil {
			return err
		}
		c.DefaultTempExpiryDays = n
	case "idle_timeout_seconds":
		n, err := atoi("idle_timeout_seconds", 0, 0)
		if err != nil {
			return err
		}
		c.IdleTimeoutSeconds = n
	case "embed_model":
		c.EmbedModel = value
	case "rerank":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("rerank must be true or false, got %q", value)
		}
		c.Rerank = b
	case "rerank_model":
		c.RerankModel = value
	case "sidecar_path":
		c.SidecarPath = value
	case "allowed_hosts":
		parts := strings.Split(value, ",")
		var hosts []string
		for _, p := range parts {
			if h := strings.TrimSpace(p); h != "" {
				hosts = append(hosts, h)
			}
		}
		c.AllowedHosts = hosts
	default:
		return fmt.Errorf("unknown config key %q; valid keys: %s", key, joinKeys())
	}
	return nil
}

// Get returns the current value of one config key as text.
func (c Config) Get(key string) (string, error) {
	switch key {
	case "data_dir":
		return c.DataDir, nil
	case "models_dir":
		return c.ModelsDir, nil
	case "port":
		return strconv.Itoa(c.Port), nil
	case "default_temp_expiry_days":
		return strconv.Itoa(c.DefaultTempExpiryDays), nil
	case "idle_timeout_seconds":
		return strconv.Itoa(c.IdleTimeoutSeconds), nil
	case "embed_model":
		return c.EmbedModel, nil
	case "rerank":
		return strconv.FormatBool(c.Rerank), nil
	case "rerank_model":
		return c.RerankModel, nil
	case "sidecar_path":
		return c.SidecarPath, nil
	case "allowed_hosts":
		return strings.Join(c.AllowedHosts, ","), nil
	default:
		return "", fmt.Errorf("unknown config key %q; valid keys: %s", key, joinKeys())
	}
}

func joinKeys() string {
	keys := Keys()
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k
	}
	return out
}
