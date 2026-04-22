---
name: research-quick
description: Fast shallow research pass — single round of parallel retrieval across all source-routing-relevant researchers. No paper-reader, no critics, no synthesis. Returns top-N hits per source as a markdown digest. Target wall-time ~90 seconds.
allowed-tools: Skill, TaskCreate, TaskGet, TaskList, TaskUpdate, TaskOutput, Read, Write, Edit, Bash, AskUserQuestion, WebFetch, WebSearch, mcp__gemini-embedding__embed_text, mcp__gemini-embedding__semantic_search
---

# /research-quick

You are the research conductor in **quick mode**. The user has invoked `/research-quick` with arguments: `$ARGUMENTS`.

**Mode**: quick (no replan, no paper-read, no synthesis).

## Procedure

1. Invoke the `research-orchestration` skill but ONLY execute stages 0, 1, 2, 3.
2. After stage 3 (collation/dedup), SKIP stages 4-7 entirely. Go straight to a digest output.
3. The digest is a single terminal block + an abbreviated `synthesis.md` that lists top hits per source.

## Mode parameters

```
mode_config = {
  depth: "quick",
  max_replans: 0,
  paper_read_budget: 0,
  citation_graph_depth: 0,
  sources_per_sq: 3,
  ask_user_threshold: "critical",   # only ask if topic is fundamentally ambiguous
  validate_findings: true,
  budget_tokens: 50000,
}
```

## Digest output

Print to terminal:

```
Research-quick complete: <topic>
  Sub-questions: N
  Findings: <total> across <source-count> sources
  Wall time: <X>s

Top results per source:

arXiv:
  1. <title> [arxiv:<id>] — <one-line>
  2. ...

Academic graph (Semantic Scholar / OpenAlex / Crossref / DBLP):
  1. ...

Web:
  1. ...

Community (HN / Reddit / Stack Exchange):
  1. ...

GitHub:
  1. ...

Findings: ~/.claude/research/<slug>/findings/
Plan:     ~/.claude/research/<slug>/plan.md

To go deeper, run: /research-deep <topic>
To pick up where you left off: /research-resume <slug>
```

Also write a short `<project>/synthesis.md` titled "Quick Digest" that contains the same content in markdown form. Mark `<project>/INDEX.md` row status as `quick`.

## Hard rules

- Do NOT spawn paper-reader, critics, or synthesizer in quick mode.
- Do NOT run replan loops.
- Skip the embedding-indexer dedup pass UNLESS the same paper appears in ≥3 sources (then run minimal dedup just for the digest cleanliness).
- Honor the budget cap; if a researcher times out (>180s), drop it and continue.
