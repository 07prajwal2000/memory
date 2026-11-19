import urllib.request
import ssl
import json

ctx = ssl._create_unverified_context()

slm_candidates = [
    ('bartowski/SmolLM2-135M-Instruct-GGUF', 'SmolLM2-135M-Instruct-Q4_K_M.gguf'),
    ('bartowski/SmolLM2-360M-Instruct-GGUF', 'SmolLM2-360M-Instruct-Q4_K_M.gguf'),
    ('Qwen/Qwen2.5-0.5B-Instruct-GGUF', 'qwen2.5-0.5b-instruct-q4_k_m.gguf'),
    ('Qwen/Qwen2.5-1.5B-Instruct-GGUF', 'qwen2.5-1.5b-instruct-q4_k_m.gguf'),
]

for repo, fn in slm_candidates:
    url = f"https://huggingface.co/{repo}/resolve/main/{fn}"
    req = urllib.request.Request(url, method='HEAD', headers={'User-Agent': 'Mozilla/5.0'})
    try:
        with urllib.request.urlopen(req, timeout=10, context=ctx) as r:
            size = int(r.headers.get('Content-Length', 0))
            print(f"FOUND: {repo}/{fn} -> {size/(1024*1024):.1f} MB (HTTP {r.status})")
    except Exception as e:
        print(f"FAILED: {repo}/{fn} -> {e}")
