// Package testenv makes a test binary hermetic with respect to the user's real
// home directory and configuration.
//
// Every package's TestMain calls Main. It points the home, config and cache
// locations at a fresh temporary directory, so no test can write to the real
// ~/.mem, ~/.config/mem, %APPDATA%\mem or the platform cache directory, even
// when a test passes empty paths and the product falls back to its defaults.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// homeEnvVars are the environment variables that decide where a program looks
// for the user's home, config and cache directories on the supported platforms.
var homeEnvVars = []string{
	"USERPROFILE",  // Windows home
	"HOMEDRIVE",    // Windows home (drive part)
	"HOMEPATH",     // Windows home (path part)
	"HOME",         // unix home
	"APPDATA",      // Windows roaming config
	"LOCALAPPDATA", // Windows local data
	"XDG_CONFIG_HOME",
	"XDG_CACHE_HOME",
	"XDG_DATA_HOME",
	"MEM_CONFIG", // mem's own config override
	"MEM_DATA_DIR",
}

// Main isolates the process and runs the package's tests. It returns the exit
// code for the TestMain wrapper.
//
// If the temporary directory cannot be created the tests fail loudly rather
// than silently running against the real home.
func Main(m *testing.M) int {
	root, err := os.MkdirTemp("", "mem-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testenv: cannot create isolated home: %v\n", err)
		return 1
	}
	defer os.RemoveAll(root)

	home := filepath.Join(root, "home")
	configDir := filepath.Join(root, "config")
	cacheDir := filepath.Join(root, "cache")
	dataDir := filepath.Join(root, "data")
	for _, dir := range []string{home, configDir, cacheDir, dataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "testenv: cannot create %s: %v\n", dir, err)
			return 1
		}
	}

	values := map[string]string{
		"USERPROFILE":     home,
		"HOMEDRIVE":       filepath.VolumeName(home),
		"HOMEPATH":        trimVolume(home),
		"HOME":            home,
		"APPDATA":         configDir,
		"LOCALAPPDATA":    cacheDir,
		"XDG_CONFIG_HOME": configDir,
		"XDG_CACHE_HOME":  cacheDir,
		"XDG_DATA_HOME":   dataDir,
		"MEM_CONFIG":      filepath.Join(configDir, "mem", "config.toml"),
	}
	for key, value := range values {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "testenv: cannot set %s: %v\n", key, err)
			return 1
		}
	}
	// A stale override would defeat the isolated config path.
	_ = os.Unsetenv("MEM_DATA_DIR")

	return m.Run()
}

// trimVolume returns a path without its volume name, for HOMEPATH on Windows.
func trimVolume(p string) string {
	if v := filepath.VolumeName(p); v != "" {
		return p[len(v):]
	}
	return p
}

// HomeEnvVars returns the variables Main overrides. Tests can assert that the
// isolation is in place.
func HomeEnvVars() []string {
	out := make([]string, len(homeEnvVars))
	copy(out, homeEnvVars)
	return out
}
