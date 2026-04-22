---
name: academic-graph-query
description: Query patterns for the academic citation graph — Semantic Scholar, OpenAlex, Crossref, and DBLP. Reference for research-academic-graph and citation-graph-builder subagents. Covers endpoints, rate limits, response shapes, and jq snippets for filtering.
user-invocable: false
---

# Academic Graph Query Reference

Four complementary sources. Use them together: each has gaps the others fill.

| Source | Strength | Weakness |
|---|---|---|
| Semantic Scholar | Citations + references graph, recommendations | Coverage skews ML/CS; some old papers missing |
| OpenAlex | 250M+ works, broad fields, concept hierarchy | Citation counts lag; metadata sometimes stale |
| Crossref | Authoritative DOI metadata, citation counts | No abstracts; no full-text |
| DBLP | CS-curated bibliography, venue info | No abstracts, no citations |

Pattern: search → dedup by DOI → enrich each from each source → write findings.

---

## Semantic Scholar

Base: `https://api.semanticscholar.org/graph/v1`

Auth: optional API key via header `x-api-key:` (free tier 100 req/min). Without key: rough cap of ~1 req/sec shared globally; expect occasional 429 — back off 5s and retry up to 3 times.

### Search papers
```
GET /paper/search?query=<topic>&limit=25&fields=title,abstract,year,authors,citationCount,externalIds,url
```

Response:
```json
{
  "total": 1234,
  "offset": 0,
  "data": [
    {
      "paperId": "8c2e4d...",
      "title": "...",
      "abstract": "...",
      "year": 2024,
      "authors": [{"authorId":"...","name":"..."}],
      "citationCount": 42,
      "externalIds": {"DOI":"10.xxxx/...","ArXiv":"2401.12345"},
      "url": "https://www.semanticscholar.org/paper/..."
    }
  ]
}
```

### Get one paper
```
GET /paper/{paperId}?fields=title,abstract,authors,year,venue,citationCount,references,citations
GET /paper/DOI:{doi}?fields=...
GET /paper/ARXIV:{arxiv-id}?fields=...
GET /paper/URL:{percent-encoded-url}?fields=...
```

### Citation traversal (for citation-graph-builder)
```
GET /paper/{paperId}/references?limit=100&fields=title,year,externalIds
GET /paper/{paperId}/citations?limit=100&fields=title,year,externalIds
```

Both paginate via `offset`. Hard cap at 1000 per call. For multi-hop traversal, dedup IDs as you go and stop at depth limit.

### Bulk lookup (efficient — avoid rate limits)
```
POST /paper/batch
Content-Type: application/json
{"ids": ["DOI:10.xxxx/...", "ARXIV:2401.12345", ...]}
```
Up to 500 IDs per call.

---

## OpenAlex

Base: `https://api.openalex.org`

Auth: none required. **Polite pool**: add `?mailto=colinc86@gmail.com` to all queries — gets you a higher rate limit (10k/day) and faster service. No key needed.

### Search works
```
GET /works?search=<topic>&per-page=50&filter=concepts.id:C41008148,from_publication_date:2024-01-01
```

Concept IDs (subset for CS):
- `C41008148` — Computer Science (top-level)
- `C154945302` — AI
- `C2522767166` — Data structures
- `C107457646` — Distributed computing
- `C201995342` — Systems
- `C188441871` — PL theory
- `C151730666` — Cryptography
- `C107826830` — Embedded systems

Search the concept directly: `GET /concepts?search=software+engineering` to find the right ID.

Response work shape (selected fields):
```json
{
  "id": "https://openalex.org/W12345",
  "doi": "https://doi.org/10.xxxx/...",
  "title": "...",
  "publication_year": 2024,
  "cited_by_count": 87,
  "authorships": [{"author":{"display_name":"..."}, "institutions":[...]}],
  "concepts": [{"display_name":"Machine Learning","score":0.84}],
  "primary_location": {"source": {"display_name": "NeurIPS"}},
  "abstract_inverted_index": {...}  // see below
}
```

**Abstracts are inverted-indexed** to dodge copyright. Reconstruct:
```python
def reconstruct(idx):
    pos = {}
    for word, positions in idx.items():
        for p in positions: pos[p] = word
    return " ".join(pos[i] for i in sorted(pos))
```
Or as a `jq` one-liner (rough):
```
jq '.abstract_inverted_index | to_entries | map(.value[] as $p | {$p, w:.key}) | sort_by(.p) | map(.w) | join(" ")'
```

### Cited-by traversal
```
GET /works?filter=cites:W12345&per-page=200
```
This is OpenAlex's equivalent of Semantic Scholar's `/citations`.

### Forward citations / references via DOI
```
GET /works/https://doi.org/10.xxxx/...
```

---

## Crossref

Base: `https://api.crossref.org`

Auth: none. **Polite pool**: add `?mailto=colinc86@gmail.com`. Gets you ~50 req/sec consistently.

### Lookup by DOI
```
GET /works/{doi}?mailto=colinc86@gmail.com
```

Response:
```json
{
  "status": "ok",
  "message": {
    "DOI": "10.xxxx/...",
    "title": ["..."],
    "author": [{"given":"...","family":"..."}],
    "published": {"date-parts":[[2024,3,15]]},
    "container-title": ["NeurIPS"],
    "is-referenced-by-count": 87,
    "reference": [{"DOI":"10.xxxx/..."}, ...],  // backward citations
    "publisher": "...",
    "type": "proceedings-article"
  }
}
```

### Search
```
GET /works?query=<topic>&filter=type:journal-article,from-pub-date:2024&rows=25&mailto=...
```

Backward citations are inline in the `reference` array. **Forward citations require Semantic Scholar or OpenAlex** — Crossref doesn't provide them.

---

## DBLP

Base: `https://dblp.org/search/publ/api`

Auth: none. Generous limits. Returns XML by default; pass `format=json` for JSON.

### Search publications
```
GET /search/publ/api?q=<topic>&format=json&h=50
```

Response (heavily nested):
```json
{
  "result": {
    "hits": {
      "@total": "234",
      "hit": [
        {
          "info": {
            "title": "...",
            "venue": "NeurIPS",
            "year": "2024",
            "type": "Conference and Workshop Papers",
            "authors": {"author":[{"@pid":"...","text":"..."}]},
            "doi": "10.xxxx/...",
            "ee": "https://...",
            "url": "https://dblp.org/rec/..."
          }
        }
      ]
    }
  }
}
```

DBLP excels at venue-level filtering ("conference papers from POPL 2023"):
```
GET /search/publ/api?q=stream:conf/popl:&format=json&h=100
```

Stream prefixes: `conf/<acronym>:` for conferences, `journals/<abbr>:` for journals.

---

## jq snippets

```bash
# Semantic Scholar: extract title + DOI from search results
jq '.data[] | {title, doi: .externalIds.DOI, arxiv: .externalIds.ArXiv, citations: .citationCount}'

# OpenAlex: title + DOI + year + cited_by from search results
jq '.results[] | {title, doi: (.doi // null | sub("https://doi.org/"; "")), year: .publication_year, cited: .cited_by_count}'

# Crossref: extract title + year + author count
jq '.message | {title: .title[0], year: .published."date-parts"[0][0], authors: (.author | length), citations: ."is-referenced-by-count"}'

# DBLP: flatten hits to paper rows
jq '.result.hits.hit[].info | {title, venue, year, doi, url}'
```

---

## Cross-source dedup recipe

1. Run search on Semantic Scholar (richest metadata).
2. For each result with a DOI, also fetch Crossref for canonical citation count.
3. For each result with an arXiv ID, also fetch arXiv for the latest abstract version.
4. Search OpenAlex with the same query and merge by DOI; OpenAlex catches papers Semantic Scholar misses.
5. Search DBLP and merge by DOI for venue confirmation.
6. Dedup canonical key priority: DOI > arXiv ID > Semantic Scholar paperId > title-author hash.

---

## Rate limit summary

| Source | Auth | Limit |
|---|---|---|
| Semantic Scholar | None | ~1 req/sec shared (often 429) |
| Semantic Scholar | API key | 100 req/min |
| OpenAlex | None + `mailto=` | 10 req/sec, 100k/day |
| Crossref | None + `mailto=` | ~50 req/sec polite pool |
| DBLP | None | Generous; conservative ~5 req/sec |

When in doubt, sleep 200ms between calls and you're fine across all four.
