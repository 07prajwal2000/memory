import os
import sys
import time
import subprocess
import json
import urllib.request
import math

SERVER_BIN = os.path.abspath("spike/bin/llama-server.exe")
MODELS_DIR = os.path.abspath("spike/models")
PORT = 8089

def get_rss(pid):
    try:
        out = subprocess.check_output(
            ["powershell", "-NoProfile", "-Command", f"(Get-Process -Id {pid}).WorkingSet64"],
            stderr=subprocess.DEVNULL
        ).decode().strip()
        return int(out) / (1024 * 1024) # MB
    except Exception:
        return 0.0

def make_text_300():
    words = [
        "cross", "encoder", "transformer", "attention", "scoring", "retrieval", "document",
        "passage", "query", "relevance", "ranking", "benchmark", "accuracy", "tokens",
        "semantic", "context", "search", "precision", "recall", "performance", "speed"
    ]
    chosen = [words[i % len(words)] for i in range(250)]
    return " ".join(chosen)

def start_rerank_server(model_filename):
    model_path = os.path.join(MODELS_DIR, model_filename)
    cmd = [
        SERVER_BIN,
        "-m", model_path,
        "--rerank",
        "-c", "8192",
        "-b", "2048",
        "-ub", "512",
        "--port", str(PORT)
    ]
    t0 = time.time()
    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL
    )
    first_resp_time = None
    rerank_url = f"http://127.0.0.1:{PORT}/v1/rerank"
    
    for _ in range(120):
        try:
            req = urllib.request.Request(
                rerank_url,
                data=json.dumps({"query": "test", "documents": ["test doc"]}).encode("utf-8"),
                headers={"Content-Type": "application/json"}
            )
            resp = json.loads(urllib.request.urlopen(req, timeout=1).read())
            first_resp_time = time.time() - t0
            break
        except Exception:
            time.sleep(0.05)
    idle_rss = get_rss(proc.pid)
    return proc, first_resp_time, idle_rss

def call_rerank(query, documents):
    rerank_url = f"http://127.0.0.1:{PORT}/v1/rerank"
    req = urllib.request.Request(
        rerank_url,
        data=json.dumps({"query": query, "documents": documents}).encode("utf-8"),
        headers={"Content-Type": "application/json"}
    )
    t0 = time.time()
    resp = json.loads(urllib.request.urlopen(req, timeout=120).read())
    elapsed = time.time() - t0
    results = resp["results"]
    return results, elapsed

def start_embed_server(model_filename="bge-small-en-v1.5-q8_0.gguf"):
    model_path = os.path.join(MODELS_DIR, model_filename)
    cmd = [
        SERVER_BIN,
        "-m", model_path,
        "--embedding",
        "-c", "2048",
        "-b", "2048",
        "-ub", "2048",
        "--port", str(PORT)
    ]
    proc = subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    emb_url = f"http://127.0.0.1:{PORT}/v1/embeddings"
    for _ in range(100):
        try:
            req = urllib.request.Request(
                emb_url,
                data=json.dumps({"input": "test"}).encode("utf-8"),
                headers={"Content-Type": "application/json"}
            )
            urllib.request.urlopen(req, timeout=1)
            break
        except Exception:
            time.sleep(0.05)
    return proc

def get_embeddings(texts):
    emb_url = f"http://127.0.0.1:{PORT}/v1/embeddings"
    req = urllib.request.Request(
        emb_url,
        data=json.dumps({"input": texts}).encode("utf-8"),
        headers={"Content-Type": "application/json"}
    )
    resp = json.loads(urllib.request.urlopen(req, timeout=120).read())
    return [d["embedding"] for d in resp["data"]]

# Hand-made test corpus of 100 passages and 15 queries with known ground truth
CORPUS = [
    # 0: Python GIL
    "In CPython, the global interpreter lock, or GIL, is a mutex that prevents multiple native threads from executing Python bytecodes simultaneously.",
    # 1: SQLite WAL
    "SQLite write-ahead logging (WAL) mode allows concurrent readers to continue reading while a writer is modifying the database without locking conflicts.",
    # 2: Photosynthesis
    "Photosynthesis is a biological process used by plants and other organisms to convert light energy into chemical energy stored in carbohydrates.",
    # 3: DNA double helix
    "Deoxyribonucleic acid is a molecule composed of two polynucleotide chains that coil around each other to form a double helix carrying genetic instructions.",
    # 4: Rust borrow checker
    "Rust uses a compile-time borrow checker to enforce memory safety and eliminate data races without needing an automated garbage collector.",
    # 5: TCP three-way handshake
    "The transmission control protocol establishes a network connection between client and server using a SYN, SYN-ACK, and ACK three-way handshake.",
    # 6: Black holes
    "A black hole is a region of spacetime where gravity is so strong that nothing, including light and other electromagnetic waves, can escape.",
    # 7: Sourdough fermentation
    "Sourdough bread is made by the fermentation of dough using naturally occurring lactobacilli and wild yeasts rather than cultivated baker's yeast.",
    # 8: Quantum computing qubits
    "Unlike classical bits which represent 0 or 1, quantum computers utilize qubits that can exist in quantum superpositions and entangled states.",
    # 9: Go channels and goroutines
    "Go achieves concurrent execution using lightweight goroutines and communicates between them using typed channels following CSP principles.",
    # 10: Mitochondria ATP
    "Mitochondria are double-membrane-bound organelles often referred to as the powerhouses of the cell because they generate most adenosine triphosphate.",
    # 11: French Revolution
    "The French Revolution was a period of fundamental political and societal change in France that began with the Estates General in 1789.",
    # 12: Transformer architecture
    "The transformer neural network architecture relies entirely on self-attention mechanisms to compute representations of input and output without recurrence.",
    # 13: Kepler's laws of planetary motion
    "Kepler's first law states that the orbit of a planet is an ellipse with the Sun located at one of the two orbital focal points.",
    # 14: HTTP status code 404
    "The HTTP 404 Not Found client error response code indicates that the server cannot find the requested resource or webpage URL."
]

# Add 85 distractors
DISTRACTORS = [
    f"Distractor passage {i}: This is an auxiliary text discussing various general aspects of computing, natural history, science, geography, and general knowledge paragraph {i}."
    for i in range(15, 100)
]
ALL_PASSAGES = CORPUS + DISTRACTORS

QUERIES = [
    ("What prevents simultaneous multi-threaded bytecode execution in Python?", 0),
    ("How does WAL mode in SQLite handle concurrent read and write operations?", 1),
    ("How do plants convert sunlight into chemical energy?", 2),
    ("What structural shape carries genetic instructions in DNA molecules?", 3),
    ("How does Rust achieve memory safety without a garbage collector?", 4),
    ("What sequence of packets establishes a TCP connection?", 5),
    ("Why cannot light or matter escape from a black hole?", 6),
    ("What wild microorganisms are responsible for fermenting sourdough dough?", 7),
    ("What allows qubits to represent more states than classical binary bits?", 8),
    ("How do goroutines communicate concurrently in Go?", 9),
    ("Which organelle produces the majority of ATP in biological cells?", 10),
    ("When did the French political revolution begin with the Estates General?", 11),
    ("What mechanism replaces recurrence in transformer neural network models?", 12),
    ("What shape is a planetary orbit according to Kepler's first law?", 13),
    ("What does the HTTP 404 response status code signify to a web client?", 14),
]

def evaluate_reranker(name, filename, bge_passages_embs, query_embs):
    print(f"\n==================================================")
    print(f"Testing Reranker: {name} ({filename})")
    print(f"==================================================")
    proc, cold_start, idle_rss = start_rerank_server(filename)
    if cold_start is None:
        print("FAILED to start rerank server!")
        proc.kill()
        return None
    print(f"Cold start time: {cold_start*1000:.1f} ms")
    print(f"Idle RSS: {idle_rss:.1f} MB")

    # 1. Measure latency to rerank 30 passages of ~300 tokens against 1 query
    print("Benchmarking 30 passages of ~300 tokens...")
    passages_30 = [make_text_300() for _ in range(30)]
    query = "benchmark query for measuring cross encoder latency against thirty candidate passages"
    # warmup
    call_rerank(query, passages_30[:5])

    latencies = []
    for _ in range(3):
        _, elapsed = call_rerank(query, passages_30)
        latencies.append(elapsed)
    mean_lat_30 = sum(latencies) / len(latencies)
    print(f"Latency to rerank 30 passages (~300 tokens each): {mean_lat_30*1000:.1f} ms")

    load_rss = get_rss(proc.pid)
    print(f"Resident memory after load: {load_rss:.1f} MB")

    # 2. Quality evaluation: Embedding + Rerank MRR@10
    reciprocal_ranks = []
    ranks = []
    for q_idx, (q_text, target_idx) in enumerate(QUERIES):
        q_vec = query_embs[q_idx]
        # Candidate generation: top 30 from embedding cosine similarity
        sims = []
        for p_idx, p_vec in enumerate(bge_passages_embs):
            sim = sum(x*y for x, y in zip(q_vec, p_vec))
            sims.append((sim, p_idx))
        sims.sort(key=lambda x: x[0], reverse=True)
        top30_indices = [idx for _, idx in sims[:30]]

        # Call reranker on top 30
        candidate_docs = [ALL_PASSAGES[idx] for idx in top30_indices]
        rerank_res, _ = call_rerank(q_text, candidate_docs)
        # Sort rerank_res by relevance_score descending
        rerank_res.sort(key=lambda x: x["relevance_score"], reverse=True)

        # Find where target_idx ended up
        target_rank = None
        for rank_pos, r in enumerate(rerank_res):
            orig_doc_idx = top30_indices[r["index"]]
            if orig_doc_idx == target_idx:
                target_rank = rank_pos + 1
                break
        
        ranks.append(target_rank)
        if target_rank is not None and target_rank <= 10:
            reciprocal_ranks.append(1.0 / target_rank)
        else:
            reciprocal_ranks.append(0.0)

    mrr = sum(reciprocal_ranks) / len(reciprocal_ranks)
    print(f"MRR@10 (Embedding + {name}): {mrr:.4f} (Ranks: {ranks})")

    proc.kill()
    proc.wait()
    time.sleep(0.5)

    return {
        "name": name,
        "filename": filename,
        "cold_start_ms": cold_start * 1000,
        "idle_rss_mb": idle_rss,
        "load_rss_mb": load_rss,
        "latency_30_passages_ms": mean_lat_30 * 1000,
        "mrr_at_10": mrr,
        "ranks": ranks
    }

def main():
    # Step 1: Start embedding server to compute embeddings for corpus & queries
    print("Starting BGE embedding server to generate baseline embeddings...")
    emb_proc = start_embed_server()
    bge_passages_embs = get_embeddings(ALL_PASSAGES)
    bge_queries_embs = get_embeddings([
        "Represent this sentence for searching relevant passages: " + q[0]
        for q in QUERIES
    ])
    emb_proc.kill()
    emb_proc.wait()

    # Calculate embedding-only MRR@10 baseline
    emb_mrr_list = []
    emb_ranks = []
    for q_idx, (_, target_idx) in enumerate(QUERIES):
        q_vec = bge_queries_embs[q_idx]
        sims = []
        for p_idx, p_vec in enumerate(bge_passages_embs):
            sim = sum(x*y for x, y in zip(q_vec, p_vec))
            sims.append((sim, p_idx))
        sims.sort(key=lambda x: x[0], reverse=True)
        top10 = [idx for _, idx in sims[:10]]
        if target_idx in top10:
            rank = top10.index(target_idx) + 1
            emb_mrr_list.append(1.0 / rank)
            emb_ranks.append(rank)
        else:
            emb_mrr_list.append(0.0)
            # find full rank
            full_rank = [idx for _, idx in sims].index(target_idx) + 1
            emb_ranks.append(full_rank)
    emb_only_mrr = sum(emb_mrr_list) / len(emb_mrr_list)
    print(f"\n==================================================")
    print(f"Baseline Embedding-Only MRR@10: {emb_only_mrr:.4f} (Ranks: {emb_ranks})")
    print(f"==================================================")

    # Step 2: Evaluate the rerankers
    r1 = evaluate_reranker("ms-marco-MiniLM-L-6-v2 Q8_0", "ms-marco-minilm-l6-v2-q8_0.gguf", bge_passages_embs, bge_queries_embs)
    r2 = evaluate_reranker("jina-reranker-v1-tiny-en Q8_0", "jina-reranker-v1-tiny-en-Q8_0.gguf", bge_passages_embs, bge_queries_embs)
    r3 = evaluate_reranker("bge-reranker-v2-m3 Q4_K_M", "bge-reranker-v2-m3-Q4_K_M.gguf", bge_passages_embs, bge_queries_embs)

    results = {
        "embedding_only_mrr": emb_only_mrr,
        "embedding_ranks": emb_ranks,
        "rerankers": [r1, r2, r3]
    }
    with open("spike/exp3_results.json", "w") as f:
        json.dump(results, f, indent=2)
    print("\nExperiment 3 completed! Results written to spike/exp3_results.json")

if __name__ == "__main__":
    main()
