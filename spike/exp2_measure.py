import os
import sys
import time
import subprocess
import json
import urllib.request
import math
import random

SERVER_BIN = os.path.abspath("spike/bin/llama-server.exe")
MODELS_DIR = os.path.abspath("spike/models")
PORT = 8089

def get_rss(pid):
    # Use powershell to query working set
    try:
        out = subprocess.check_output(
            ["powershell", "-NoProfile", "-Command", f"(Get-Process -Id {pid}).WorkingSet64"],
            stderr=subprocess.DEVNULL
        ).decode().strip()
        return int(out) / (1024 * 1024) # MB
    except Exception as e:
        return 0.0

def make_text_300():
    words = [
        "memory", "system", "search", "vector", "database", "embedding", "context",
        "agent", "storage", "retrieval", "performance", "latency", "benchmark", "index",
        "sqlite", "rerank", "fusion", "relevance", "document", "passage", "scoring",
        "precision", "recall", "quantization", "dimension"
    ]
    # 250 words is ~300 tokens
    chosen = [random.choice(words) for _ in range(250)]
    return " ".join(chosen)

def start_server(model_filename, extra_flags=[]):
    model_path = os.path.join(MODELS_DIR, model_filename)
    cmd = [
        SERVER_BIN,
        "-m", model_path,
        "--embedding",
        "-c", "2048",
        "-b", "2048",
        "-ub", "2048",
        "--port", str(PORT)
    ] + extra_flags
    t0 = time.time()
    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL
    )
    # Poll until ready
    first_resp_time = None
    url = f"http://127.0.0.1:{PORT}/health"
    emb_url = f"http://127.0.0.1:{PORT}/v1/embeddings"
    
    for _ in range(100):
        try:
            req = urllib.request.Request(
                emb_url,
                data=json.dumps({"input": "test"}).encode("utf-8"),
                headers={"Content-Type": "application/json"}
            )
            resp = json.loads(urllib.request.urlopen(req, timeout=1).read())
            first_resp_time = time.time() - t0
            break
        except Exception:
            time.sleep(0.05)
    idle_rss = get_rss(proc.pid)
    return proc, first_resp_time, idle_rss

def call_embeddings(texts):
    emb_url = f"http://127.0.0.1:{PORT}/v1/embeddings"
    req = urllib.request.Request(
        emb_url,
        data=json.dumps({"input": texts}).encode("utf-8"),
        headers={"Content-Type": "application/json"}
    )
    t0 = time.time()
    resp = json.loads(urllib.request.urlopen(req, timeout=120).read())
    elapsed = time.time() - t0
    embs = [d["embedding"] for d in resp["data"]]
    return embs, elapsed

def measure_model(name, filename):
    print(f"\n==================================================")
    print(f"Testing model: {name} ({filename})")
    print(f"==================================================")
    proc, cold_start, idle_rss = start_server(filename)
    if cold_start is None:
        print("FAILED to start server!")
        proc.kill()
        return None
    print(f"Cold start time: {cold_start*1000:.1f} ms")
    print(f"Idle RSS: {idle_rss:.1f} MB")

    # Check dim and L2 norm on single test
    test_emb, _ = call_embeddings(["Testing embedding output properties"])
    dim = len(test_emb[0])
    l2_norm = math.sqrt(sum(x*x for x in test_emb[0]))
    print(f"Embedding dimension: {dim}")
    print(f"L2 norm: {l2_norm:.6f} (Normalized: {abs(l2_norm - 1.0) < 1e-4})")

    # Warmup
    call_embeddings([make_text_300()])

    results = {}
    for batch_size in [1, 16, 64]:
        texts = [make_text_300() for _ in range(batch_size)]
        # Run 3 times, take mean
        latencies = []
        for _ in range(3):
            _, elapsed = call_embeddings(texts)
            latencies.append(elapsed)
        mean_lat = sum(latencies) / len(latencies)
        throughput = batch_size / mean_lat
        print(f"Batch {batch_size:2d}: {mean_lat*1000:7.1f} ms ({throughput:5.1f} texts/sec)")
        results[f"batch_{batch_size}_ms"] = mean_lat * 1000
        results[f"batch_{batch_size}_tps"] = throughput

    load_rss = get_rss(proc.pid)
    print(f"Resident memory after load: {load_rss:.1f} MB")

    proc.kill()
    proc.wait()
    time.sleep(0.5)

    return {
        "name": name,
        "filename": filename,
        "cold_start_ms": cold_start * 1000,
        "idle_rss_mb": idle_rss,
        "load_rss_mb": load_rss,
        "dim": dim,
        "l2_norm": l2_norm,
        "batches": results
    }

def run_quantization_evaluation():
    print("\n==================================================")
    print("Evaluating int8 quantization overlap@10 on 2k sample")
    print("==================================================")
    proc, _, _ = start_server("bge-small-en-v1.5-q8_0.gguf")
    
    # Generate 2000 texts
    print("Generating 2,000 text samples...")
    sample_texts = [make_text_300() for _ in range(2000)]
    
    # Embed in batches of 64
    all_embs = []
    t0 = time.time()
    batch_size = 64
    for i in range(0, len(sample_texts), batch_size):
        chunk = sample_texts[i:i+batch_size]
        embs, _ = call_embeddings(chunk)
        all_embs.extend(embs)
        sys.stdout.write(f"\rEmbedded {len(all_embs)}/2000 texts in {time.time()-t0:.1f}s")
        sys.stdout.flush()
    print()

    # Generate 50 queries
    queries = [
        "Represent this sentence for searching relevant passages: " + make_text_300()[:100]
        for _ in range(50)
    ]
    query_embs, _ = call_embeddings(queries)

    proc.kill()
    proc.wait()

    # Pre-quantize all 2000 vectors to int8
    # float vector is L2 normalized. Scaling by 127 and rounding
    quantized_vectors = []
    for vec in all_embs:
        qvec = [max(-128, min(127, int(round(x * 127)))) for x in vec]
        quantized_vectors.append(qvec)

    overlaps = []
    for q_idx, q_vec in enumerate(query_embs):
        # Float similarity (dot product)
        f_scores = []
        for doc_idx, doc_vec in enumerate(all_embs):
            score = sum(x*y for x, y in zip(q_vec, doc_vec))
            f_scores.append((score, doc_idx))
        f_scores.sort(key=lambda x: x[0], reverse=True)
        top10_float = set(idx for _, idx in f_scores[:10])

        # Int8 similarity
        # Quantize query vector
        q_int8 = [max(-128, min(127, int(round(x * 127)))) for x in q_vec]
        i_scores = []
        for doc_idx, doc_qvec in enumerate(quantized_vectors):
            score = sum(x*y for x, y in zip(q_int8, doc_qvec))
            i_scores.append((score, doc_idx))
        i_scores.sort(key=lambda x: x[0], reverse=True)
        top10_int8 = set(idx for _, idx in i_scores[:10])

        overlap = len(top10_float.intersection(top10_int8)) / 10.0
        overlaps.append(overlap)

    avg_overlap = sum(overlaps) / len(overlaps)
    print(f"\nAverage overlap@10 across 50 queries on 2,000 texts: {avg_overlap*100:.2f}%")
    return avg_overlap

if __name__ == "__main__":
    results = {}
    r1 = measure_model("BGE-Small-EN-v1.5 Q8_0", "bge-small-en-v1.5-q8_0.gguf")
    r2 = measure_model("BGE-Small-EN-v1.5 F16", "bge-small-en-v1.5-f16.gguf")
    r3 = measure_model("Multilingual-E5-Small Q8_0", "multilingual-e5-small-Q8_0.gguf")
    overlap = run_quantization_evaluation()

    full_results = {
        "models": [r1, r2, r3],
        "overlap_at_10": overlap
    }
    with open("spike/exp2_results.json", "w") as f:
        json.dump(full_results, f, indent=2)
    print("\nExperiment 2 completed! Results written to spike/exp2_results.json")
