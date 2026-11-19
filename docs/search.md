# How search works

`mem` combines keyword search and meaning-based search, then merges the results. By default it also tolerates typos and partial words.

## Two searches, one list

When you write a memory, `mem` splits it into small chunks at headings and paragraphs, then stores two things for each chunk:

1. **Keywords.** A full-text index (SQLite FTS5 with BM25 ranking) records the words. This is why an exact error code, flag or function name is found reliably.
2. **Meaning.** A small local embedding model (`bge-small-en-v1.5`) turns the text into a 384-dimensional vector. This is why "how long do access tokens last" can find a note that says "tokens expire after 15 minutes" without sharing those words.

At query time both searches run, and their ranked lists are merged with a method called reciprocal rank fusion. The result is one list where a chunk can be strong on keywords, on meaning, or both. The `sources` field tells you which legs matched: `bm25` for keywords, `vec` for meaning, `name` for the memory's name, heading or tags, `fuzzy` for a typo or prefix expansion, and `rerank` when the optional reranker reordered the list.

Reranking is **off by default**. When enabled, a second small model scores the top candidates against the query. In our tests the tiny reranker did not improve ranking enough to justify its memory and latency, so it is opt-in. See [Configuration](configuration.md#rerank).

## Fuzzy matching is on by default

Fuzzy matching is what makes search feel forgiving while you type. It adds three behaviors:

- **Prefix expansion.** The last word is treated as a prefix, so `migrat` finds `migrations`.
- **Typo tolerance.** A word close to a known word is expanded: `refrsh` finds `refresh`, `drivn` finds `driven`.
- **Morphology.** The index uses Porter stemming, so `refresh`, `refreshing` and `refreshed` match each other.

Names, path segments, headings and tags are searched too, which is why `deploy/staging.md` is easy to find by name.

Exact matches always outrank fuzzy-only matches. A fuzzy hit is a suggestion, not a replacement. When a result matched only through fuzzy expansion, human output marks it with `~`.

Real examples:

```bash
mem search refrsh --context work --limit 3
```

```text
1. readme.md (global/eebfzjok4nhm) score=0.0933 [vec,fuzzy]
   matched: refresh
   ...
2. auth/token.md (work/t4epatooy5gv) score=0.0723 [vec,fuzzy,name]
   matched: toke, token
   Access tokens expire after 15 minutes; refresh tokens last 30 days.
```

```bash
mem search "table drivn tests" --context work --limit 3
```

```text
2. prefs/code-style.md (work/y5maamtampm2) score=0.0812 [vec,bm25,fuzzy]
   matched: table, tests, tabl, driven, test
   Prefer small pure functions and table-driven Go tests. No comments unless they explain why.
```

## Turning fuzzy matching off

Every surface has a switch. All of them default to on.

| Where | How to turn it off |
|---|---|
| CLI, one query | `mem search ... --no-fuzzy` |
| CLI, force on for one query | `mem search ... --fuzzy` |
| MCP server default | `mem mcp --no-fuzzy` |
| MCP `search` tool, one call | `"fuzzy": false` |
| Saved config | `mem config search_fuzzy false` |
| REST `GET /api/search` | `fuzzy=0` |

For contrast, the same typo query with fuzzy off:

```bash
mem search "databse migratons" --context work --no-fuzzy
```

```text
3. db/migrations.md (work/wi67lnntnaq5) score=0.0667 [vec]
   Schema changes go through goose migrations in db/migrations; never edit the live schema by hand.
```

The note is still found (the vector leg and the name help), but the keyword leg no longer guesses. Use `--no-fuzzy` when you need exact-word precision or when fuzzy results are noisy.

## Writing queries that work

- **Ask like a colleague.** "how do we deploy the staging API" works better than `deploy`.
- **Add exact identifiers when you have them.** Error codes, flags, function names, ticket ids and configuration keys are matched literally and are the strongest signal you can give.
- **Start narrow, then widen.** Search one context first. If you get nothing, drop `--context` or raise `--limit`.
- **Prefer the words the note would contain.** The embedding model is small; a paraphrase is fine, but a completely different vocabulary may miss.
- **Name your notes.** A good name (`auth/token-refresh.md`) is indexed and often decides the match.
- **Keep one topic per note.** A short note has a clear vector; a long mixed note has an average of everything.

## Why a note may not be found

- **It is not indexed yet.** If the embedder was down when you wrote it, keyword search still works and `mem status` shows pending vectors. Run `mem sync`.
- **You searched the wrong scope.** Search covers `global` plus the context you pass. A note in `work` is invisible when you search only `global`; a note in `global` is hidden by `--no-global`.
- **The context expired.** A temp context disappears with its notes. `mem context ls` shows what still exists.
- **Fuzzy is off and the query has a typo.** Either fix the spelling or drop `--no-fuzzy`.
- **The words are too generic.** `the`, `fix`, `error` match many chunks; add a distinctive term.
- **The result is below the limit.** Raise `--limit` (max 50) or use `--per-doc` to see more chunks of the same document.
- **You edited the markdown by hand.** The files are the source of truth, but the index is derived. Run `mem sync` to pick up external edits.

## Speed

Search runs locally. On the corpus used for benchmarking (real Go source, 39,000 and 200,000 chunks), the search pipeline measured **10 ms p50 / 26 ms p95** at 39k chunks and **51 ms p50 / 150 ms p95** at 200k, including embedding the query. The first query after the model server has stopped pays a one-time model start of a few hundred milliseconds.

<div class="next-steps">

**Next steps:** [Dashboard](dashboard.md) → [Recipes](recipes.md)

</div>
