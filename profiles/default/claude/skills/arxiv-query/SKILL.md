---
name: arxiv-query
description: How to query the arXiv API effectively for CS topics. Reference for the research-arxiv subagent. Covers category codes, date filters, sort orders, Atom XML parsing, ID extraction, and the HTML view URL pattern used by paper-reader.
user-invocable: false
---

# arXiv Query Reference

## Endpoint

`http://export.arxiv.org/api/query`

GET parameters:
- `search_query=` — see syntax below
- `start=` — pagination offset (default 0)
- `max_results=` — page size (max 2000, but be polite — use 25-50)
- `sortBy=` — `relevance | lastUpdatedDate | submittedDate`
- `sortOrder=` — `ascending | descending`

Returns Atom XML.

## Rate limits

3 req/sec sustainable, no auth. Insert 350ms sleep between consecutive calls if running tight loops. Heavy parallel use (>10 concurrent fetches) risks soft-blocking — keep stage 2 dispatches under 5 arXiv-bound researchers.

## Query syntax

```
search_query=cat:cs.LG+AND+abs:%22flash+attention%22+AND+submittedDate:[202301010000+TO+202612312359]
```

Operators: `AND`, `OR`, `ANDNOT`. Group with parentheses. Field prefixes:
- `ti:` title
- `au:` author
- `abs:` abstract
- `cat:` category (see below)
- `all:` all fields
- `submittedDate:` `[YYYYMMDDhhmm TO YYYYMMDDhhmm]`

Quote multi-word phrases with `%22...%22` (URL-encoded `"..."`).

## CS category codes

| Code | Field |
|---|---|
| cs.AI | Artificial Intelligence |
| cs.AR | Hardware Architecture |
| cs.CC | Computational Complexity |
| cs.CE | Computational Engineering |
| cs.CG | Computational Geometry |
| cs.CL | Computation and Language (NLP) |
| cs.CR | Cryptography and Security |
| cs.CV | Computer Vision |
| cs.CY | Computers and Society |
| cs.DB | Databases |
| cs.DC | Distributed/Parallel/Cluster Computing |
| cs.DM | Discrete Mathematics |
| cs.DS | Data Structures and Algorithms |
| cs.ET | Emerging Technologies |
| cs.FL | Formal Languages and Automata |
| cs.GL | General Literature |
| cs.GR | Graphics |
| cs.GT | Game Theory |
| cs.HC | Human-Computer Interaction |
| cs.IR | Information Retrieval |
| cs.IT | Information Theory |
| cs.LG | Machine Learning |
| cs.LO | Logic in CS |
| cs.MA | Multiagent Systems |
| cs.MM | Multimedia |
| cs.MS | Mathematical Software |
| cs.NA | Numerical Analysis |
| cs.NE | Neural / Evolutionary Computing |
| cs.NI | Networking and Internet Architecture |
| cs.OH | Other |
| cs.OS | Operating Systems |
| cs.PF | Performance |
| cs.PL | Programming Languages |
| cs.RO | Robotics |
| cs.SC | Symbolic Computation |
| cs.SD | Sound |
| cs.SE | Software Engineering |
| cs.SI | Social and Information Networks |
| cs.SY | Systems and Control |

For broad topics, use multiple categories with `OR`: `cat:cs.LG+OR+cat:cs.CL+OR+cat:cs.AI`.

## Sort guidance

- `lastUpdatedDate desc` — best for "state of the art" queries (catches v2/v3 revisions)
- `submittedDate desc` — best for "what's new this month"
- `relevance` — default but quirky; pair with date filter to avoid stale top hits

## Response parsing (Atom XML)

Each `<entry>` has:
- `<id>http://arxiv.org/abs/2307.08691v2</id>` — strip `v\d+$` for canonical ID
- `<title>...</title>` — has line breaks; collapse whitespace
- `<summary>...</summary>` — abstract
- `<author><name>...</name></author>` — multiple
- `<published>2023-07-17T...</published>` — first version date
- `<updated>2023-09-12T...</updated>` — latest version date
- `<arxiv:primary_category term="cs.LG"/>`
- `<link rel="alternate" href="http://arxiv.org/abs/2307.08691v2"/>` — abstract page
- `<link title="pdf" href="http://arxiv.org/pdf/2307.08691v2"/>` — PDF
- DOI (if assigned): `<arxiv:doi>10.xxxx/...</arxiv:doi>`

Extract canonical ID with regex `arxiv\.org/abs/(\d{4}\.\d{4,5})(v\d+)?` → group 1.

## Companion URLs (for paper-reader)

For arXiv ID `2307.08691`:
- Abstract page: `https://arxiv.org/abs/2307.08691`
- HTML view (much easier than PDF): `https://arxiv.org/html/2307.08691v2` (omit `v2` for latest)
- PDF: `https://arxiv.org/pdf/2307.08691.pdf`
- Source (TeX): `https://arxiv.org/e-print/2307.08691` (rarely useful for LLM)

**Prefer HTML view** when available — paper-reader gets clean text without PDF parsing pain. Fall back to PDF only if HTML returns 404.

## Common queries (templates)

```
# Recent ML on a topic
search_query=cat:cs.LG+AND+all:%22<topic>%22&sortBy=lastUpdatedDate&sortOrder=descending&max_results=50&start=0

# All-CS broad sweep
search_query=(cat:cs.*)+AND+all:%22<topic>%22&sortBy=relevance&max_results=50

# Author + topic
search_query=au:%22<lastname>%22+AND+abs:%22<topic>%22&sortBy=submittedDate&sortOrder=descending

# Date-bounded SOTA
search_query=cat:cs.LG+AND+abs:%22<topic>%22+AND+submittedDate:[202507010000+TO+202612312359]&sortBy=relevance
```

Note: `cat:cs.*` is NOT supported — enumerate explicitly with `OR` if you need cross-category.

## Gotchas

- Empty `<summary>` happens for withdrawn papers — drop these.
- Author-disambiguation is poor; same name can be different people.
- Cross-listed papers appear once per category — dedup by canonical ID.
- arXiv tags don't always reflect content (papers are self-categorized).
