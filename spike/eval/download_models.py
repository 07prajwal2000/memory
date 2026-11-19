import urllib.request
import ssl
import hashlib
import os
import json

ctx = ssl._create_unverified_context()

MODELS = [
    # 384-d small models
    {
        "id": "gte-small-q8_0",
        "name": "gte-small",
        "quant": "Q8_0",
        "dims": 384,
        "repo": "ggml-org/gte-small-Q8_0-GGUF",
        "file": "gte-small-q8_0.gguf",
        "url": "https://huggingface.co/ggml-org/gte-small-Q8_0-GGUF/resolve/main/gte-small-q8_0.gguf",
        "prefix_query": "",
        "prefix_doc": "",
        "pooling": "mean"
    },
    {
        "id": "e5-small-v2-q8_0",
        "name": "e5-small-v2",
        "quant": "Q8_0",
        "dims": 384,
        "repo": "ggml-org/e5-small-v2-Q8_0-GGUF",
        "file": "e5-small-v2-q8_0.gguf",
        "url": "https://huggingface.co/ggml-org/e5-small-v2-Q8_0-GGUF/resolve/main/e5-small-v2-q8_0.gguf",
        "prefix_query": "query: ",
        "prefix_doc": "passage: ",
        "pooling": "mean"
    },
    {
        "id": "all-MiniLM-L6-v2-q8_0",
        "name": "all-MiniLM-L6-v2",
        "quant": "Q8_0",
        "dims": 384,
        "repo": "prithivida/all-MiniLM-L6-v2-gguf",
        "file": "all-MiniLM-L6-v2-q8_0.gguf",
        "url": "https://huggingface.co/prithivida/all-MiniLM-L6-v2-gguf/resolve/main/all-MiniLM-L6-v2-q8_0.gguf",
        "prefix_query": "",
        "prefix_doc": "",
        "pooling": "mean"
    },
    {
        "id": "snowflake-arctic-embed-xs-q8_0",
        "name": "snowflake-arctic-embed-xs",
        "quant": "Q8_0",
        "dims": 384,
        "repo": "ChristianAzinn/snowflake-arctic-embed-xs-gguf",
        "file": "snowflake-arctic-embed-xs-Q8_0.GGUF",
        "url": "https://huggingface.co/ChristianAzinn/snowflake-arctic-embed-xs-gguf/resolve/main/snowflake-arctic-embed-xs-Q8_0.GGUF",
        "prefix_query": "Represent this sentence for searching relevant passages: ",
        "prefix_doc": "",
        "pooling": "cls"
    },
    {
        "id": "snowflake-arctic-embed-s-q8_0",
        "name": "snowflake-arctic-embed-s",
        "quant": "Q8_0",
        "dims": 384,
        "repo": "ChristianAzinn/snowflake-arctic-embed-s-gguf",
        "file": "snowflake-arctic-embed-s-Q8_0.GGUF",
        "url": "https://huggingface.co/ChristianAzinn/snowflake-arctic-embed-s-gguf/resolve/main/snowflake-arctic-embed-s-Q8_0.GGUF",
        "prefix_query": "Represent this sentence for searching relevant passages: ",
        "prefix_doc": "",
        "pooling": "cls"
    },
    {
        "id": "mxbai-embed-xsmall-v1-q8_0",
        "name": "mxbai-embed-xsmall-v1",
        "quant": "Q8_0",
        "dims": 384,
        "repo": "mradermacher/mxbai-embed-xsmall-v1-GGUF",
        "file": "mxbai-embed-xsmall-v1.Q8_0.gguf",
        "url": "https://huggingface.co/mradermacher/mxbai-embed-xsmall-v1-GGUF/resolve/main/mxbai-embed-xsmall-v1.Q8_0.gguf",
        "prefix_query": "Represent this sentence for searching relevant passages: ",
        "prefix_doc": "",
        "pooling": "cls"
    },
    # Base models <= 100 MB
    {
        "id": "bge-base-en-v1.5-q4_k_m",
        "name": "bge-base-en-v1.5",
        "quant": "Q4_K_M",
        "dims": 768,
        "repo": "CompendiumLabs/bge-base-en-v1.5-gguf",
        "file": "bge-base-en-v1.5-q4_k_m.gguf",
        "url": "https://huggingface.co/CompendiumLabs/bge-base-en-v1.5-gguf/resolve/main/bge-base-en-v1.5-q4_k_m.gguf",
        "prefix_query": "Represent this sentence for searching relevant passages: ",
        "prefix_doc": "",
        "pooling": "cls"
    },
    {
        "id": "gte-base-q4_k_m",
        "name": "gte-base",
        "quant": "Q4_K_M",
        "dims": 768,
        "repo": "ChristianAzinn/gte-base-gguf",
        "file": "gte-base.Q4_K_M.gguf",
        "url": "https://huggingface.co/ChristianAzinn/gte-base-gguf/resolve/main/gte-base.Q4_K_M.gguf",
        "prefix_query": "",
        "prefix_doc": "",
        "pooling": "mean"
    },
    {
        "id": "e5-base-v2-q4_k_m",
        "name": "e5-base-v2",
        "quant": "Q4_K_M",
        "dims": 768,
        "repo": "ChristianAzinn/e5-base-v2-gguf",
        "file": "e5-base-v2.Q4_K_M.gguf",
        "url": "https://huggingface.co/ChristianAzinn/e5-base-v2-gguf/resolve/main/e5-base-v2.Q4_K_M.gguf",
        "prefix_query": "query: ",
        "prefix_doc": "passage: ",
        "pooling": "mean"
    },
    {
        "id": "snowflake-arctic-embed-m-q4_k_m",
        "name": "snowflake-arctic-embed-m",
        "quant": "Q4_K_M",
        "dims": 768,
        "repo": "ChristianAzinn/snowflake-arctic-embed-m-gguf",
        "file": "snowflake-arctic-embed-m--Q4_K_M.GGUF",
        "url": "https://huggingface.co/ChristianAzinn/snowflake-arctic-embed-m-gguf/resolve/main/snowflake-arctic-embed-m--Q4_K_M.GGUF",
        "prefix_query": "Represent this sentence for searching relevant passages: ",
        "prefix_doc": "",
        "pooling": "cls"
    },
    {
        "id": "nomic-embed-text-v1.5-q4_k_m",
        "name": "nomic-embed-text-v1.5",
        "quant": "Q4_K_M",
        "dims": 768,
        "repo": "nomic-ai/nomic-embed-text-v1.5-GGUF",
        "file": "nomic-embed-text-v1.5.Q4_K_M.gguf",
        "url": "https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.Q4_K_M.gguf",
        "prefix_query": "search_query: ",
        "prefix_doc": "search_document: ",
        "pooling": "mean"
    },
    {
        "id": "granite-embedding-30m-english-q8_0",
        "name": "granite-embedding-30m-english",
        "quant": "Q8_0",
        "dims": 384,
        "repo": "bartowski/granite-embedding-30m-english-GGUF",
        "file": "granite-embedding-30m-english-Q8_0.gguf",
        "url": "https://huggingface.co/bartowski/granite-embedding-30m-english-GGUF/resolve/main/granite-embedding-30m-english-Q8_0.gguf",
        "prefix_query": "",
        "prefix_doc": "",
        "pooling": "mean"
    },
    {
        "id": "gte-modernbert-base-iq4_xs",
        "name": "gte-modernbert-base",
        "quant": "IQ4_XS",
        "dims": 768,
        "repo": "cstr/gte-modernbert-base-GGUF",
        "file": "gte-modernbert-base-iq4_xs.gguf",
        "url": "https://huggingface.co/cstr/gte-modernbert-base-GGUF/resolve/main/gte-modernbert-base-iq4_xs.gguf",
        "prefix_query": "",
        "prefix_doc": "",
        "pooling": "mean"
    },
    # Ceiling reference
    {
        "id": "bge-large-en-v1.5-q4_k_m",
        "name": "bge-large-en-v1.5",
        "quant": "Q4_K_M",
        "dims": 1024,
        "repo": "CompendiumLabs/bge-large-en-v1.5-gguf",
        "file": "bge-large-en-v1.5-q4_k_m.gguf",
        "url": "https://huggingface.co/CompendiumLabs/bge-large-en-v1.5-gguf/resolve/main/bge-large-en-v1.5-q4_k_m.gguf",
        "prefix_query": "Represent this sentence for searching relevant passages: ",
        "prefix_doc": "",
        "pooling": "cls"
    }
]

def sha256_file(filepath):
    h = hashlib.sha256()
    with open(filepath, 'rb') as f:
        while chunk := f.read(65536):
            h.update(chunk)
    return h.hexdigest()

manifest = []

# First check existing local baselines
local_models = [
    {
        "id": "bge-small-en-v1.5-q8_0",
        "name": "bge-small-en-v1.5",
        "quant": "Q8_0",
        "dims": 384,
        "file": "bge-small-en-v1.5-q8_0.gguf",
        "path": "spike/models/bge-small-en-v1.5-q8_0.gguf",
        "url": "local/already-shipped",
        "prefix_query": "Represent this sentence for searching relevant passages: ",
        "prefix_doc": "",
        "pooling": "cls"
    },
    {
        "id": "bge-small-en-v1.5-f16",
        "name": "bge-small-en-v1.5",
        "quant": "F16",
        "dims": 384,
        "file": "bge-small-en-v1.5-f16.gguf",
        "path": "spike/models/bge-small-en-v1.5-f16.gguf",
        "url": "local/already-shipped",
        "prefix_query": "Represent this sentence for searching relevant passages: ",
        "prefix_doc": "",
        "pooling": "cls"
    }
]

for m in local_models:
    p = m["path"]
    size = os.path.getsize(p)
    h = sha256_file(p)
    m["size_bytes"] = size
    m["size_mb"] = round(size / (1024*1024), 2)
    m["sha256"] = h
    manifest.append(m)
    print(f"Verified local model: {m['id']} ({m['size_mb']} MB, sha256: {h[:12]}...)")

# Download remote models
for m in MODELS:
    dest = os.path.join("spike/models", m["file"])
    m["path"] = dest.replace("\\", "/")
    url = m["url"]
    
    # Check if exists and valid
    download_needed = True
    if os.path.exists(dest):
        print(f"Checking existing {m['id']} at {dest}...")
        try:
            req = urllib.request.Request(url, method='HEAD', headers={'User-Agent': 'Mozilla/5.0'})
            with urllib.request.urlopen(req, timeout=10, context=ctx) as r:
                expected_size = int(r.headers.get('Content-Length', 0))
                if expected_size > 0 and os.path.getsize(dest) == expected_size:
                    print(f"Existing file matches remote size ({expected_size} bytes). Skipping download.")
                    download_needed = False
        except Exception as e:
            print(f"Could not check remote size for {m['id']}: {e}")
            if os.path.getsize(dest) > 1024*1024:
                download_needed = False

    if download_needed:
        print(f"Downloading {m['id']} from {url} to {dest}...")
        req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
        with urllib.request.urlopen(req, timeout=60, context=ctx) as resp, open(dest, 'wb') as out_f:
            total_read = 0
            while chunk := resp.read(65536):
                out_f.write(chunk)
                total_read += len(chunk)
        print(f"Downloaded {m['id']} ({total_read/(1024*1024):.1f} MB)")

    size = os.path.getsize(dest)
    h = sha256_file(dest)
    m["size_bytes"] = size
    m["size_mb"] = round(size / (1024*1024), 2)
    m["sha256"] = h
    manifest.append(m)
    print(f"Ready: {m['id']} ({m['size_mb']} MB, sha256: {h[:12]}...)")

out_manifest = "spike/eval/models_manifest.json"
with open(out_manifest, "w", encoding="utf-8") as f:
    json.dump(manifest, f, indent=2)

print(f"Manifest written to {out_manifest} ({len(manifest)} models registered)")
