---
name: paper-synthesis
description: Schema and rules for the final synthesis report. Reference for the synthesizer subagent. Covers report structure, citation format, contradiction-handling rule, evidence weighting, and bibliography format.
user-invocable: false
---

# Synthesis Report Specification

The `synthesizer` subagent writes `synthesis.md`. This is the artifact the user actually reads. It must be tight, evidence-bound, and never silently pick a winner in a contested area.

## Hard Rules

1. **Every claim cites at least one paper or finding** by ID. No naked assertions.
2. **Every `## C{n}` from `analysis/contradictions.md` MUST appear in the Contradictions section** — either resolved (with justification), open, or sided (with explicit weighing). Conductor will reject synthesis that omits a contradiction ID.
3. **Confidence is explicit and standardized**: `high` (≥3 independent sources, methodology rated strong) | `medium` (2 sources OR methodology mixed) | `low` (1 source OR methodology weak).
4. **No emojis. No marketing language.** Plain technical prose.
5. **Bibliography is BibTeX-flavored**, not numbered footnotes. Citation format inline: `[paper-id]` (the canonical id like `arxiv:2307.08691` or `doi:10.xxxx/...`).
6. **Length discipline**: 1500-4000 words for standard depth. Deep mode can go to ~6000 if the corpus warrants. Quick mode skips synthesis entirely.

## Report Skeleton

```markdown
# <Topic>

**Question**: <user's original query, restated as a clear question>
**Date range covered**: <year_min>-<month> to <year_max>-<month>
**Corpus size**: <N> papers, <M> community/practitioner sources
**Iterations**: <discovery_rounds> discovery, <replan_rounds> replan
**Generated**: <ISO timestamp>

---

## Executive Summary

2-5 sentences. State the answer. If the question doesn't have a single answer, state the shape of the disagreement.

## Background

1-3 paragraphs. What is the problem space? What terminology does the reader need? Cite the seminal/foundational papers.

## Key Findings

### Finding 1: <one-sentence statement>
- **Evidence**: [paper-id-1], [paper-id-2], [hn-id-3]
- **Confidence**: high
- **Detail**: 1-3 paragraphs explaining the finding, what the evidence specifically shows, and any nuances.

### Finding 2: ...

(Aim for 4-8 findings. If you have more, group; if fewer, recheck whether you've actually answered the question.)

## Methodology Notes

Brief discussion of how strong the evidence base is. Cite from `analysis/methodology-review.md`. Flag:
- Papers with weak benchmarks or single-seed experiments
- Industry blog posts with no public methodology
- Sources with potential conflicts of interest
- Coverage gaps (sub-questions where evidence is thin or one-sided)

## Contradictions

For each `## C{n}` in `analysis/contradictions.md`:

### C1: <topic of contention>
- **Claim A** ([paper-id], confidence: high): "..."
- **Claim B** ([paper-id], confidence: medium): "..."
- **Resolution**: One of:
  - **Reconciled**: explain how both can be true (often: scope distinction — claim A holds under conditions X, claim B under conditions Y)
  - **Sided**: pick a side AND justify with methodology weight (e.g., "Claim A is more credible because it ran 5-seed experiments and replicated in [paper-id-2]")
  - **Open**: state plainly that current evidence cannot resolve and add to Open Questions

### C2: ...

## Open Questions

Bulleted list of things the corpus could not settle. Each item: 1-2 sentences + which sub-question raised it + which papers came closest.

## Reading List (Recommended Order)

1. **[paper-id]** — <why read first> (e.g., "best high-level overview of the area")
2. **[paper-id]** — <why read next>
3. ...

Aim for 3-7 papers. This is "if the user only reads N things, what?"

## Bibliography

BibTeX entries for every cited paper. Order alphabetically by first author's surname. Use:

```bibtex
@article{lastname2024key,
  author    = {Last, First and Last2, First2},
  title     = {Full Paper Title},
  year      = {2024},
  journal   = {Conference or Journal},
  doi       = {10.xxxx/...},
  url       = {https://doi.org/10.xxxx/...},
}

@misc{lastname2023arxivkey,
  author        = {Last, First},
  title         = {Title},
  year          = {2023},
  eprint        = {2307.08691},
  archivePrefix = {arXiv},
  primaryClass  = {cs.LG},
  url           = {https://arxiv.org/abs/2307.08691},
}
```

For non-paper sources (HN threads, Reddit posts, blog articles, GH repos):

```bibtex
@misc{hn2025-12345678,
  author = {Hacker News discussion},
  title  = {<thread title>},
  year   = {2025},
  url    = {https://news.ycombinator.com/item?id=12345678},
  note   = {Accessed 2026-04-17},
}

@misc{gh2024-owner-repo,
  author = {<owner>},
  title  = {<repo>: <description>},
  year   = {2024},
  url    = {https://github.com/owner/repo},
  note   = {<stars>★, last commit <date>},
}
```

```

## Evidence Weighting Heuristics

When weighing evidence:

| Strong signal | Weak signal |
|---|---|
| Peer-reviewed, top-tier venue (NeurIPS, ICLR, OSDI, SIGCOMM, POPL, ...) | arXiv preprint with no version 2 |
| Multiple independent reproductions | Single-team result |
| Public benchmark results with rerun-able code | Vendor-supplied numbers |
| Methodology critic rated `≥4/5` on rigor | Critic rated `≤2/5` on rigor |
| Community consensus across multiple subreddits/HN | One viral blog post |
| GitHub repo with active maintenance + production users | Abandoned repo with stars |

When evidence conflicts in strength, **say so explicitly in the Detail field of the relevant Finding**. Do not silently average them.

## Anti-patterns to Avoid

- ❌ "Many researchers believe..." — name them with citations
- ❌ "It is well known that..." — cite the source where it became well-known
- ❌ "Recent work suggests..." — be specific (`[paper-id]` shows X)
- ❌ Dropping a contradiction without addressing it
- ❌ Writing the synthesis as a chronological list of papers ("Smith et al. 2023 did X, then Jones et al. 2024 did Y") instead of organizing by claim
- ❌ Ending with a generic "more research is needed" — be specific in Open Questions
- ❌ Omitting confidence labels on findings

## Validation (the conductor will check)

After writing `synthesis.md`, the conductor verifies:
1. File exists and is ≥2k chars
2. All section headers present in order: Executive Summary, Background, Key Findings, Methodology Notes, Contradictions, Open Questions, Reading List, Bibliography
3. Every `## C{n}` from `analysis/contradictions.md` appears in the Contradictions section
4. Bibliography has at least one entry per uniquely cited ID in the body
5. No bare `[paper-id]` references that don't appear in Bibliography

If validation fails, the conductor will retry the synthesizer ONCE with the specific failure noted.
