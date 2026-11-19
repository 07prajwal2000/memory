import urllib.request
import ssl
import hashlib
import os
import json

ctx = ssl._create_unverified_context()

SLM_MODELS = [
    {
        "id": "SmolLM2-135M-Instruct-Q4_K_M",
        "name": "SmolLM2-135M-Instruct",
        "quant": "Q4_K_M",
        "file": "SmolLM2-135M-Instruct-Q4_K_M.gguf",
        "url": "https://huggingface.co/bartowski/SmolLM2-135M-Instruct-GGUF/resolve/main/SmolLM2-135M-Instruct-Q4_K_M.gguf"
    },
    {
        "id": "SmolLM2-360M-Instruct-Q4_K_M",
        "name": "SmolLM2-360M-Instruct",
        "quant": "Q4_K_M",
        "file": "SmolLM2-360M-Instruct-Q4_K_M.gguf",
        "url": "https://huggingface.co/bartowski/SmolLM2-360M-Instruct-GGUF/resolve/main/SmolLM2-360M-Instruct-Q4_K_M.gguf"
    },
    {
        "id": "qwen2.5-0.5b-instruct-q4_k_m",
        "name": "Qwen2.5-0.5B-Instruct",
        "quant": "Q4_K_M",
        "file": "qwen2.5-0.5b-instruct-q4_k_m.gguf",
        "url": "https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf"
    },
    {
        "id": "qwen2.5-1.5b-instruct-q4_k_m",
        "name": "Qwen2.5-1.5B-Instruct",
        "quant": "Q4_K_M",
        "file": "qwen2.5-1.5b-instruct-q4_k_m.gguf",
        "url": "https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf"
    }
]

def sha256_file(filepath):
    h = hashlib.sha256()
    with open(filepath, 'rb') as f:
        while chunk := f.read(65536):
            h.update(chunk)
    return h.hexdigest()

manifest = []

for m in SLM_MODELS:
    dest = os.path.join("spike/models", m["file"])
    m["path"] = dest.replace("\\", "/")
    url = m["url"]
    
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
            if os.path.getsize(dest) > 10*1024*1024:
                download_needed = False

    if download_needed:
        print(f"Downloading {m['id']} from {url} to {dest}...")
        req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
        with urllib.request.urlopen(req, timeout=120, context=ctx) as resp, open(dest, 'wb') as out_f:
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

out_manifest = "spike/eval/slm_manifest.json"
with open(out_manifest, "w", encoding="utf-8") as f:
    json.dump(manifest, f, indent=2)

print(f"SLM Manifest written to {out_manifest}")
