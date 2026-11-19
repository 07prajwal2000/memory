import urllib.request
import ssl
import json

ctx = ssl._create_unverified_context()

candidate_files = [
    # Baseline
    ('BAAI/bge-small-en-v1.5', 'already_local', 'bge-small-en-v1.5-q8_0.gguf'),
    ('BAAI/bge-small-en-v1.5-f16', 'already_local', 'bge-small-en-v1.5-f16.gguf'),
    # 384-d small
    ('ggml-org/gte-small-Q8_0-GGUF', 'gte-small-q8_0.gguf'),
    ('ggml-org/e5-small-v2-Q8_0-GGUF', 'e5-small-v2-q8_0.gguf'),
    ('prithivida/all-MiniLM-L6-v2-gguf', 'all-MiniLM-L6-v2-q8_0.gguf'),
    ('ChristianAzinn/snowflake-arctic-embed-xs-gguf', 'snowflake-arctic-embed-xs-Q8_0.GGUF'),
    ('ChristianAzinn/snowflake-arctic-embed-s-gguf', 'snowflake-arctic-embed-s-Q8_0.GGUF'),
    ('mradermacher/mxbai-embed-xsmall-v1-GGUF', 'mxbai-embed-xsmall-v1.Q8_0.gguf'),
    # Base <= 100MB
    ('CompendiumLabs/bge-base-en-v1.5-gguf', 'bge-base-en-v1.5-q4_k_m.gguf'),
    ('ChristianAzinn/gte-base-gguf', 'gte-base.Q4_K_M.gguf'),
    ('ChristianAzinn/e5-base-v2-gguf', 'e5-base-v2.Q4_K_M.gguf'),
    ('ChristianAzinn/snowflake-arctic-embed-m-gguf', 'snowflake-arctic-embed-m--Q4_K_M.GGUF'),
    ('nomic-ai/nomic-embed-text-v1.5-GGUF', 'nomic-embed-text-v1.5.Q4_K_M.gguf'),
    ('bartowski/granite-embedding-30m-english-GGUF', 'granite-embedding-30m-english-Q8_0.gguf'),
    ('cstr/gte-modernbert-base-GGUF', 'gte-modernbert-base-Q4_K_M.gguf'),
    # Ceiling
    ('CompendiumLabs/bge-large-en-v1.5-gguf', 'bge-large-en-v1.5-q4_k_m.gguf'),
]

for item in candidate_files:
    if len(item) == 3:
        print(f"LOCAL: {item[0]} -> {item[2]}")
        continue
    repo, fn = item
    url = f"https://huggingface.co/{repo}/resolve/main/{fn}"
    req = urllib.request.Request(url, method='HEAD', headers={'User-Agent': 'Mozilla/5.0'})
    try:
        with urllib.request.urlopen(req, timeout=10, context=ctx) as r:
            size = int(r.headers.get('Content-Length', 0))
            print(f"FOUND: {repo}/{fn} -> {size/(1024*1024):.1f} MB (HTTP {r.status})")
    except Exception as e:
        print(f"FAILED: {repo}/{fn} -> {e}")
