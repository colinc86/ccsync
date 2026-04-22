---
name: research-list-papers
description: Print the deduplicated paper bibliography for a research project — DOI, title, year, venue, citation count, source. Reads papers/*.md frontmatter. Read-only.
allowed-tools: Read, Bash
---

# /research-list-papers

Print the bibliography for a research project. The user has invoked `/research-list-papers` with arguments: `$ARGUMENTS` (project slug).

## Procedure

1. **Locate the project**: `~/.claude/research/$ARGUMENTS/`. If missing, list available projects from `INDEX.md` and ask the user to pick one.

2. **Read paper frontmatter**: walk `<project>/papers/*.md`. For each, extract from YAML frontmatter:
   - `paper_id` (canonical)
   - `title`
   - `authors` (first author + et al if multiple)
   - `year`
   - `venue`
   - `doi` (if present)
   - `arxiv_id` (if present)
   - `url`
   - `claims_count` (signals deep-read quality)

3. **Also read finding frontmatter** to surface papers that were discovered but not deep-read (so the user can see the full retrieval surface): walk `<project>/findings/*.md` and aggregate paper IDs from the `papers[]` arrays. Mark those not in `<project>/papers/` as `[discovered, not deep-read]`.

4. **Sort options**: by year desc (default). Optionally let the user pass `--sort=year-asc | citations-desc | venue-asc | title` if `$ARGUMENTS` contains a flag.

5. **Print as a table**:

```
Bibliography for: <slug>
Project status: <status>  |  Papers deep-read: <X>  |  Papers discovered: <Y>

Year  Venue        Authors           Title                                                  ID
────  ───────────  ─────────────────  ─────────────────────────────────────────────────────  ──────────────────────
2024  ICLR         Dao et al.         FlashAttention-3: Faster Attention with Async         arxiv:2407.08608
2023  arXiv        Dao                FlashAttention-2                                       arxiv:2307.08691  [deep-read, 5 claims]
2022  NeurIPS      Dao et al.         FlashAttention                                         arxiv:2205.14135  [deep-read, 7 claims]
...
2018  ICML         Vaswani et al.     Attention Is All You Need                              doi:10.../...     [discovered, not deep-read]

Quick links:
  Synthesis:  cat ~/.claude/research/<slug>/synthesis.md
  Plan:       cat ~/.claude/research/<slug>/plan.md
  All papers: ls ~/.claude/research/<slug>/papers/
```

6. **Optional: BibTeX export** if `$ARGUMENTS` contains `--bibtex`:
   - Read `<project>/synthesis.md` if present and extract its Bibliography section.
   - Otherwise, generate BibTeX entries from paper frontmatter using the format in the `paper-synthesis` skill.
   - Print the BibTeX block at the end.

## Hard rules

- Read-only.
- Do NOT spawn subagents.
- Do NOT fetch external sources.
- If `<project>/papers/` is empty (e.g., quick-mode project), say so clearly and offer to print the discovered (finding-only) paper list.
