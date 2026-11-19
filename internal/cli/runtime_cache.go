package cli

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/ncruces/go-sqlite3"
	"github.com/tetratelabs/wazero"

	"memory/internal/config"
)

var sqliteCacheOnce sync.Once

// enableSQLiteCache points the SQLite Wasm runtime at a persistent compilation
// cache. Compiling the 1.5 MB SQLite module costs ~750 ms; loading it from the
// cache costs ~40 ms, which is what keeps every command well under the startup
// budget. The first ever run on a machine still pays the compile.
//
// A cache that cannot be created is not an error: the process falls back to the
// default in-memory compilation.
func enableSQLiteCache() {
	sqliteCacheOnce.Do(func() {
		dir, err := os.UserCacheDir()
		if err != nil {
			return
		}
		cacheDir := filepath.Join(dir, config.AppName, "wazero")
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return
		}
		cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
		if err != nil {
			return
		}
		sqlite3.RuntimeConfig = wazero.NewRuntimeConfig().WithCompilationCache(cache)
		// The cache is flushed by wazero as modules compile; the handle itself
		// lives for the process lifetime, so it is not closed here.
		_ = context.Background()
	})
}
