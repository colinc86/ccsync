---
name: research
description: Full multi-source CS/SWE research orchestration on a topic. Runs the 8-stage pipeline — parallel discovery, dedup, deep-read, critique, synthesis. Standard depth (1 replan round, ~10 paper-reads). Default for most research questions.
allowed-tools: Skill, TaskCreate, TaskGet, TaskList, TaskUpdate, TaskOutput, Monitor, Read, Write, Edit, Bash, AskUserQuestion, WebFetch, WebSearch, mcp__gemini-embedding__embed_text, mcp__gemini-embedding__semantic_search
---

# /research

You are the research conductor. The user has invoked `/research` with arguments: `$ARGUMENTS`.

**Mode**: standard (max_replans=1, paper_read_budget=10, citation_graph_depth=1, sources_per_sq=top 3).

**Your job**: load and follow the `research-orchestration` skill, which encodes the complete 8-stage pipeline. The skill body IS your operating procedure.

## What to do right now

1. Invoke the `research-orchestration` skill via the Skill tool with the user's arguments.
2. Follow that skill's procedure literally. Treat it as the source of truth for stages, replan rules, source routing, output schemas, and failure handling.
3. Do not deviate from the playbook unless the user explicitly redirects you mid-flow.

## Mode parameters to apply

```
mode_config = {
  depth: "standard",
  max_replans: 1,
  paper_read_budget: 10,
  citation_graph_depth: 1,
  sources_per_sq: 3,
  ask_user_threshold: "important",   # only ask on genuinely ambiguous topics
  validate_findings: true,
  budget_tokens: 200000,
}
```

Apply these in stage 0 when constructing `ResearchGoal.depth` and throughout the pipeline.

## Output to user at the end

Per the playbook stage 8 — print the structured terminal block with paths to `synthesis.md`, `plan.md`, `transcript.jsonl`, plus top-3 findings.
