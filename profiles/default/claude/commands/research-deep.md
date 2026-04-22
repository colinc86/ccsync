---
name: research-deep
description: Exhaustive multi-round research — citation graph traversal to depth 2, paper-reader on top 30 papers, full critique chain, multiple replan rounds, deeper bibliography. Use for thesis-grade or genuinely complex topics. Cost ~$3-5 per run.
allowed-tools: Skill, TaskCreate, TaskGet, TaskList, TaskUpdate, TaskOutput, Monitor, Read, Write, Edit, Bash, AskUserQuestion, WebFetch, WebSearch, mcp__gemini-embedding__embed_text, mcp__gemini-embedding__semantic_search
---

# /research-deep

You are the research conductor in **deep mode**. The user has invoked `/research-deep` with arguments: `$ARGUMENTS`.

**Mode**: deep (max_replans=3, paper_read_budget=30, citation_graph_depth=2, sources_per_sq=top 5).

## Procedure

1. Invoke the `research-orchestration` skill and follow the full 8-stage pipeline literally.
2. Apply the deep-mode parameters below. The pipeline supports up to 3 replan rounds, 2-hop citation graph traversal, and broader source coverage per sub-question.
3. Be aggressive with stage 6 replan decisions — if domain-expert flags a missing perspective, take it (you have budget).

## Mode parameters

```
mode_config = {
  depth: "deep",
  max_replans: 3,
  paper_read_budget: 30,
  citation_graph_depth: 2,
  sources_per_sq: 5,
  ask_user_threshold: "useful",     # ask user on close calls (more interactive)
  validate_findings: true,
  budget_tokens: 600000,
}
```

## Notes for deep mode

- Stage 4 paper-read runs in waves of 5 (so 30 papers = 6 waves, sequential).
- Stage 3 dedup is critical — 30 deep-reads of duplicates wastes the budget.
- Stage 5 contradiction-finder will surface more contradictions because the corpus is larger; the synthesizer must address them all.
- `synthesis.md` length budget: up to 6000 words.

## User-in-loop in deep mode

Be more willing to ask the user:
- At stage 6 if widen vs deepen scores are within 0.20 (vs 0.15 in standard).
- At stage 6 if budget >70% used and replan would push budget over 90%.
- After the first full round if the corpus reveals a major direction the user didn't specify (e.g., "do you want me to also cover the X angle?").

## Output to user at the end

Per the playbook stage 8 — full structured terminal block. Plus a one-line note recommending follow-up: e.g., "To explore X further, run: /research <X>".
