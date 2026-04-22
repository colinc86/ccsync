---
name: cite
description: Resolve a DOI, arXiv ID, or paper URL through Crossref + Semantic Scholar and print BibTeX + APA + a one-paragraph summary. Standalone — does not create a research project. Useful for one-off citation lookups.
allowed-tools: WebFetch, Bash, Read
---

# /cite

Look up a single paper by identifier and produce a citation block. The user has invoked `/cite` with arguments: `$ARGUMENTS`.

## Procedure

1. **Parse `$ARGUMENTS`** — should be one of:
   - DOI: `10.xxxx/...` (with or without `doi:` / `https://doi.org/` prefix)
   - arXiv ID: `2307.08691` (with or without `arxiv:` / `https://arxiv.org/abs/` prefix)
   - URL to a paper page

2. **Resolve via Crossref** (for DOI lookups; canonical metadata):
   - `GET https://api.crossref.org/works/<doi>?mailto=colinc86@gmail.com`
   - Extract: title, authors, year, venue, DOI, citation count, publisher.

3. **Augment via Semantic Scholar** (for abstract + recent citation count):
   - `GET https://api.semanticscholar.org/graph/v1/paper/DOI:<doi>?fields=title,abstract,year,authors,citationCount,venue,url`
   - Or for arXiv: `GET .../paper/ARXIV:<id>?fields=...`
   - Or for URL: `GET .../paper/URL:<percent-encoded-url>?fields=...`
   - If Semantic Scholar fails, continue with what Crossref returned.

4. **For arXiv-only papers** (no DOI yet), use arXiv API directly:
   - `GET https://export.arxiv.org/api/query?id_list=<arxiv-id>`
   - Parse title, abstract, authors, year, primary_category.

5. **Format output** to the terminal:

```
Citation lookup: <ID>

<Title>
<Authors (Last, First; ...)>
<Year> · <Venue> · <citation_count> citations

DOI: <doi or N/A>
arXiv: <arxiv_id or N/A>
URL: <link>

Abstract:
<full abstract, wrapped at ~100 cols>

BibTeX:
@article{<key>,
  author    = {Last, First and ...},
  title     = {Full Title},
  year      = {2024},
  journal   = {<venue>},
  doi       = {10.xxxx/...},
  url       = {https://doi.org/10.xxxx/...},
}

APA:
Last, F., Last2, F., & Last3, F. (2024). Full Title. <Venue>. https://doi.org/10.xxxx/...
```

For arXiv-only:
```
BibTeX:
@misc{<key>,
  author        = {Last, First},
  title         = {Title},
  year          = {2023},
  eprint        = {2307.08691},
  archivePrefix = {arXiv},
  primaryClass  = {cs.LG},
  url           = {https://arxiv.org/abs/2307.08691},
}
```

BibTeX key convention: `<lastname-of-first-author><year><first-significant-word-of-title>` — lowercase, hyphenated. E.g., `dao2023flashattention`.

## Failure handling

- DOI not found in Crossref: try Semantic Scholar with the DOI directly. Then arXiv if it looks like an arXiv preprint.
- All sources fail: print a clear error with the resolution attempts and suggest the user check the identifier format.
- Network errors: retry once with 2s sleep.

## Hard rules

- Standalone — do NOT create a `~/.claude/research/<slug>/` directory.
- Do NOT spawn subagents.
- Do NOT modify any files.
- Output goes to terminal only.
