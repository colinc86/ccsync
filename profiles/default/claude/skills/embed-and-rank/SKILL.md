---
name: embed-and-rank
description: Workflow for using mcp__gemini-embedding tools (embed_text, semantic_search) to dedup, rank, and cluster paper findings without spending LLM tokens. Reference for embedding-indexer and contradiction-finder subagents.
user-invocable: false
---

# Embedding & Ranking Workflow

`mcp__gemini-embedding__embed_text` and `mcp__gemini-embedding__semantic_search` provide cheap, fast vector operations. One embedding call costs ~0.1% of an Opus inference. Use them aggressively to short-circuit LLM calls for:

- **Dedup**: same paper appearing in multiple sources (cosine > 0.92 = same)
- **Ranking**: papers/findings by relevance to the original query
- **Clustering**: grouping atomic claims for contradiction detection
- **Off-topic detection**: stage-3 validation of researcher output

## Tool reference

### `embed_text`

Inputs:
- `text`: a single string OR `texts`: array of strings
- `dimensions`: 768 | 1536 | 3072 (default 3072)
- `task_type`: `RETRIEVAL_QUERY` | `RETRIEVAL_DOCUMENT` | `SEMANTIC_SIMILARITY` | `CLUSTERING` | `CLASSIFICATION` | `QUESTION_ANSWERING` | `FACT_VERIFICATION`

Returns: vector(s) as float arrays.

**Dimension picking**:
- **768**: use for dedup, off-topic checks, near-duplicate finding. Plenty for cosine > 0.85 thresholds.
- **1536**: use for ranking when fine ordering matters (top-20 selection from 100 candidates).
- **3072**: rarely needed for this system. Reserve for cases where 1536 produced visibly bad results.

**Task type picking**:
- **`RETRIEVAL_QUERY`** for the original user query (and `query_used` strings during validation)
- **`RETRIEVAL_DOCUMENT`** for paper titles+abstracts and finding bodies
- **`SEMANTIC_SIMILARITY`** for symmetric pair comparison (e.g., dedup)
- **`CLUSTERING`** for atomic claims being clustered by contradiction-finder
- Asymmetric tasks (query vs document) MUST use the matching pair — don't embed both with the same task type

### `semantic_search`

Inputs:
- `query`: a string
- `candidates`: array of strings to rank
- `dimensions`: same as embed_text (use 768 for ranking dedup, 1536 for fine ranking)
- `top_k`: how many results

Returns: ranked list with cosine similarity scores.

This is a one-shot convenience: it embeds the query + all candidates and returns sorted matches in one MCP call. Use when the candidates aren't already embedded.

## Persistent storage: `embeddings.json`

Schema:
```json
{
  "version": 1,
  "rows": {
    "<canonical_id>": {
      "kind": "paper" | "finding" | "claim",
      "source_path": "papers/2307.08691.md",
      "text_hash": "sha1:abcd...",
      "dim": 768,
      "task_type": "RETRIEVAL_DOCUMENT",
      "vector": [0.012, -0.034, ...],
      "embedded_at": "2026-04-17T14:32:00Z"
    }
  }
}
```

- `canonical_id` is the dedup key — `arxiv:2307.08691`, `doi:10.xxxx/...`, `finding:<source>-<sq>-<hash>`, `claim:<paper_id>:<claim_idx>`.
- `text_hash` lets you skip re-embedding when source content hasn't changed.
- One file per project, locked via `.locks/embeddings.json.lock` for concurrent writers.

## Batching

`embed_text` accepts arrays. Always batch:
- Up to **100 strings per call** (Gemini 2.0 limit).
- Group by `task_type` and `dimensions` (a batch can only have one of each).
- For paper corpus of 50 papers: 1 call. For 200: 2 calls.

Never call `embed_text` for one string in a loop — that's wasted overhead.

## Standard recipes

### Recipe 1: Dedup paper corpus (embedding-indexer in stage 3)

```
1. Read all papers from {papers/, findings/} that have a canonical_id but no row in embeddings.json.
2. Build texts[] = [title + ". " + abstract.truncate(1500) for each]
3. Call embed_text(texts, dimensions=768, task_type=RETRIEVAL_DOCUMENT) → vectors
4. Upsert into embeddings.json keyed by canonical_id.
5. Pairwise cosine sim within the new set + against existing — pairs with sim > 0.92 → mark as duplicates in a separate `duplicates.json` (canonical_id pairs).
```

When stage 4 picks deep-read candidates, it skips one of each duplicate pair (preferring the one with more metadata).

### Recipe 2: Rank papers by relevance (stage 4 candidate selection)

```
1. Build query_text = original user topic from ResearchGoal
2. Call embed_text(query_text, dimensions=1536, task_type=RETRIEVAL_QUERY) → q_vec
3. Look up paper vectors from embeddings.json (re-embed at 1536 if cached at 768 — don't mix dimensions)
4. Compute cosine(q_vec, paper_vec) for each
5. Combine with centrality:
   final_score = 0.6 * cosine_sim + 0.4 * normalized_degree_centrality
6. Top-N by final_score = deep-read candidates
```

### Recipe 3: Off-topic check (stage 3 validation)

For each `findings/*.md`:
```
1. q_vec = embed_text(finding.query_used, dim=768, task_type=RETRIEVAL_QUERY)
2. doc_vecs = embed_text([f"{p.title}" for p in finding.papers[:5]], dim=768, task_type=RETRIEVAL_DOCUMENT)
3. mean_sim = mean(cosine(q_vec, dv) for dv in doc_vecs)
4. if mean_sim < 0.55: flag finding as off_topic, drop from corpus, log
```

### Recipe 4: Cluster atomic claims (contradiction-finder, stage 5)

```
1. Read all `## Claims` sections from papers/*.md and parse into {paper_id, claim_text, claim_idx}
2. claim_vecs = embed_text([c.claim_text for c in claims], dim=768, task_type=CLUSTERING)
3. Pairwise cosine matrix; agglomerative cluster with cutoff 0.78 (cosine sim) → cluster labels
4. Each cluster of size ≥2 = "topic of contention" candidate
5. Within each cluster, pairwise: do these claims contradict (same metric? same conditions?) → ask the LLM ONLY for the cluster pairs that survived this filter
```

This avoids the cartesian product of all claim pairs and only spends LLM tokens on plausibly contradictory pairs.

### Recipe 5: Find supporting/dissenting papers for a contradiction (stage 5)

```
1. Given a contradiction "Claim X: <text>", embed Claim X (dim=1536, task_type=SEMANTIC_SIMILARITY)
2. semantic_search(claim_text, candidates=[all paper abstracts], top_k=10)
3. Return top 10 papers most semantically aligned with the claim — manual review confirms
```

## Cosine similarity thresholds (calibrated for these task types)

| Operation | Threshold | Effect |
|---|---|---|
| Same paper (dedup) | cosine > 0.92 | Very high confidence; merges arXiv preprint with conference version |
| Near-duplicate finding | cosine > 0.85 | Same source-hit returned by two researchers |
| On-topic | cosine > 0.55 | Loose; mostly catches obvious off-topic noise |
| Same topic of contention (cluster) | cosine > 0.78 | Intermediate; good for grouping atomic claims |
| Hallucination spot-check (URL title vs returned content) | cosine > 0.6 | Loose; only flags egregious mismatches |

These are calibrated for `RETRIEVAL_DOCUMENT` task type at 768 dim. If you switch dim/task type, expect thresholds to shift; recalibrate by spot-checking 20 known pairs first.

## Failure mode: MCP unavailable

If `mcp__gemini-embedding__*` returns errors:
1. Embedding-indexer logs `{event:"embedding_unavailable"}` and exits with degraded status.
2. Conductor falls back to title+author normalized-string equality for dedup (catches the obvious cases — same arXiv ID across sources).
3. Stage 4 candidate ranking falls back to `0.4 * normalized_centrality + 0.6 * normalized_citation_count`.
4. Stage 5 contradiction-finder falls back to LLM-only clustering (more expensive — only feasible for ≤30 claims).
5. Synthesis methodology section notes the embedding fallback.

## Cost estimate

Per `/research` standard run, embedding cost: <$0.01.
- ~30-80 paper embeddings × 1 call (batched) at 768 dim
- ~20-40 finding embeddings × 1 call at 768 dim
- ~50-200 atomic claims × 1 call at 768 dim for clustering
- ~5-10 ad-hoc semantic_search calls

Compare to LLM-based dedup (Opus reading all paper pairs): >$5. Embeddings are the highest-leverage optimization in this system.
