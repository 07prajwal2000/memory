// Package setup implements `mem init`: platform detection, the download of the
// llama-server release and the GGUF models, and the interactive wizard.
package setup

import (
	"fmt"
	"runtime"
	"strings"
)

// Release is the pinned llama.cpp build. Asset names were checked against the
// GitHub release API for this tag; every asset carries a pinned SHA-256 below.
const Release = "b12503"

const releaseBase = "https://github.com/ggml-org/llama.cpp/releases/download/" + Release + "/"

// releaseBaseURL and modelBaseURL are variables so tests can point the
// downloader at a local server. Production code never changes them.
var (
	releaseBaseURL = releaseBase
	embedModelURL  = EmbeddingModel.URL
	rerankModelURL = RerankModel.URL
)

// Asset is one downloadable llama-server archive.
type Asset struct {
	// Name is the release asset file name.
	Name string
	// Size is the byte length of the asset.
	Size int64
	// SHA256 is the hex digest of the asset, measured from this release.
	SHA256 string
	// Format is "zip" or "tar.gz".
	Format string
}

// Key identifies a target platform.
type Key struct {
	GOOS   string
	GOARCH string
}

func (k Key) String() string { return k.GOOS + "/" + k.GOARCH }

// Assets maps every supported platform to its CPU build.
//
// Linux uses the "ubuntu" builds (the official CPU artifacts), macOS the
// universal CPU build per architecture, Windows the CPU build per architecture.
//
// The SHA-256 values were computed locally from the downloads performed while
// writing this code; they are not upstream-published checksums.
var Assets = map[Key]Asset{
	{"windows", "amd64"}: {
		Name:   "llama-" + Release + "-bin-win-cpu-x64.zip",
		Size:   25521102,
		SHA256: "64bee9b6fece8e66c665c40290cbb84706af23c670041d024b56a71a39859d77",
		Format: "zip",
	},
	{"windows", "arm64"}: {
		Name:   "llama-" + Release + "-bin-win-cpu-arm64.zip",
		Size:   13838966,
		SHA256: "56df5e64f4763ff9a8ca5e1a5b54a5839b4becb99e0e2f1ba8ad80dbf50faf05",
		Format: "zip",
	},
	{"linux", "amd64"}: {
		Name:   "llama-" + Release + "-bin-ubuntu-x64.tar.gz",
		Size:   23784463,
		SHA256: "0d585f8a78ffdfdc4f6303a089aaa1379c15323b33483b6548c85c774a376a9d",
		Format: "tar.gz",
	},
	{"linux", "arm64"}: {
		Name:   "llama-" + Release + "-bin-ubuntu-arm64.tar.gz",
		Size:   19377874,
		SHA256: "e2b566c5368e2e781b73e5c6222f9512c9e6bc563cf4863a42b675356fb6df6a",
		Format: "tar.gz",
	},
	{"darwin", "amd64"}: {
		Name:   "llama-" + Release + "-bin-macos-x64.tar.gz",
		Size:   13055174,
		SHA256: "4c42388726490b1ed7864dc621dd29526f0ced3d617a96c5ac111287a7a68ba5",
		Format: "tar.gz",
	},
	{"darwin", "arm64"}: {
		Name:   "llama-" + Release + "-bin-macos-arm64.tar.gz",
		Size:   17282183,
		SHA256: "e88d7e91198d0de6ceb3d33711ac01ee0d5b519cd9ba4e81854e3bba84f22214",
		Format: "tar.gz",
	},
}

// URL returns the download URL for the given asset.
func URL(a Asset) string { return releaseBaseURL + a.Name }

// Model is one downloadable GGUF file.
type Model struct {
	// File is the name written into models_dir.
	File string
	// URL is the exact file inside its HuggingFace repository.
	URL string
	// Size is the byte length.
	Size int64
	// SHA256 is the hex digest, measured from this project's own download.
	SHA256 string
}

// EmbeddingModel is the bge-small-en-v1.5 Q8_0 GGUF used for vectors.
var EmbeddingModel = Model{
	File:   "bge-small-en-v1.5-q8_0.gguf",
	URL:    "https://huggingface.co/CompendiumLabs/bge-small-en-v1.5-gguf/resolve/main/bge-small-en-v1.5-q8_0.gguf",
	Size:   36806944,
	SHA256: "ec38e8da142596baa913124ae50550de284b6916bf59577ef2f0cb9660c2f514",
}

// RerankModel is the jina-reranker-v1-tiny-en Q8_0 GGUF. It is off by default
// because the 02a evaluation measured it hurting technical recall.
var RerankModel = Model{
	File:   "jina-reranker-v1-tiny-en-Q8_0.gguf",
	URL:    "https://huggingface.co/gpustack/jina-reranker-v1-tiny-en-GGUF/resolve/main/jina-reranker-v1-tiny-en-Q8_0.gguf",
	Size:   36676960,
	SHA256: "0defc1f8a1f4dd22183124a2a25a97765603e5a9e42258046c9b2c8a26d1f553",
}

// AssetFor returns the llama-server asset for a platform. An unsupported
// platform returns a clear error listing the supported ones.
func AssetFor(goos, goarch string) (Asset, error) {
	if a, ok := Assets[Key{goos, goarch}]; ok {
		return a, nil
	}
	return Asset{}, fmt.Errorf("no llama-server build for %s/%s; supported: %s", goos, goarch, Supported())
}

// CurrentAsset returns the asset for the running platform.
func CurrentAsset() (Asset, error) { return AssetFor(runtime.GOOS, runtime.GOARCH) }

// Supported lists the supported platforms.
func Supported() string {
	keys := make([]string, 0, len(Assets))
	for k := range Assets {
		keys = append(keys, k.String())
	}
	sortStrings(keys)
	return strings.Join(keys, ", ")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
