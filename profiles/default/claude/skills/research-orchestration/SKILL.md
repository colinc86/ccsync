---
name: research-orchestration
description: Master playbook for the /research, /research-quick, /research-deep, and /research-resume commands. Loaded by the conductor to orchestrate parallel retrieval, deep-read, critique, and synthesis subagents on a CS/SWE topic. Encodes the 8-stage pipeline, replan rules, source routing table, and validation contracts.
user-invocable: false
---

# Research Orchestration Playbook

You are the **conductor**. The user invoked one of the `/research*` commands; you now run an 8-stage pipeline that dispatches parallel researcher subagents, dedups and critiques their findings, and produces a cited synthesis. All durable state lives on disk under `~/.claude/research/<topic-slug>/`.

## Cardinal Rules

1. **You are the only entity that spawns subagents.** Subagents have no `Agent` tool. If you delegate to a critic and it says "I need more data," you decide whether to dispatch another retrieval round — the critic does not.
2. **Every Agent invoke logs to `transcript.jsonl` before and after** with `{stage, event, id, status, ts}`. This is what makes `/research-resume` work. No log = no resume.
3. **Idempotency by addressable filename.** Every subagent writes to a deterministic path. If the path exists with non-empty + valid frontmatter, the agent returns `{status: skipped}`. Resume becomes free for completed steps.
4. **Run things in parallel.** A single Agent tool message with N tool calls is the only true concurrency. Use it aggressively in stages 2, 4, and 5.
5. **Cheap recall, expensive reasoning.** Researchers are Haiku. Paper-readers are Sonnet. Critics and synthesizer are Opus. Don't second-guess these assignments — they are baked into each agent's frontmatter.
6. **Use embeddings before LLMs** for dedup, ranking, and clustering. `mcp__gemini-embedding__semantic_search` costs ~0.1% of an Opus call.

## Depth Modes

| Mode | max_replans | paper_read_budget | citation_graph_depth | Source budget per SQ |
|---|---|---|---|---|
| `quick` (`/research-quick`) | 0 | 0 | 0 | top 3 sources, no critique, no synthesis |
| `standard` (`/research`) | 1 | 10 | 1 | top 3 sources |
| `deep` (`/research-deep`) | 3 | 30 | 2 | top 5 sources |

Pass these through every stage as the `mode_config` in your reasoning.

## Stage 0 — Intake

Inputs: raw `$ARGUMENTS` from the slash command (e.g., `"FlashAttention algorithmic improvements"`).

1. Strip flags: `--depth=`, `--time-horizon=YYYY-YYYY`, `--budget=<N>k-tokens`, `--confirm-plan`. Anything left is the topic.
2. Build `ResearchGoal`:
   - `topic_slug` — kebab-case + ISO date suffix, e.g. `flashattention-algorithmic-improvements-2026-04-17`
   - `cs_subfield` — best-guess from `{ml, systems, pl, theory, security, hci, distributed, graphics, db, networks, compilers}` from your own reading of the topic
   - `question_shape` — `{survey, comparative, sota, practitioner, historical, mechanism}` (see Decomposition Heuristics below)
   - `time_horizon` — `(year_min, year_max)`. Defaults: `sota` → last 18 months, `historical` → unbounded, others → last 5 years
   - `depth` — from the command name
3. **Ambiguity check.** If the topic has ≥2 plausible CS readings (e.g., "transformers" architecture vs library; "actor model" Erlang vs cinema; "Rust" language vs game), call `AskUserQuestion` ONCE with up to 3 disambiguations. Otherwise skip.
4. `mkdir -p ~/.claude/research/<slug>/{findings,papers,analysis,.locks}`.
5. Append to `~/.claude/research/INDEX.md`: a row with status `active`.
6. Open `transcript.jsonl` and write `{stage:0, event:"intake_complete", goal:{...}, ts:"..."}`.

## Stage 1 — Decompose & Commit Plan

1. Apply Decomposition Heuristics (below) to produce 3–7 sub-questions.
2. Write `plan.md` using this skeleton:

```markdown
# Research Plan: <topic>
Generated: <ISO timestamp>
Goal: <one-line restatement>
Question shape: <shape>
CS subfield: <subfield>
Time horizon: <year_min> to <year_max>
Depth: <mode>

## Sub-questions

### SQ1: <question text>
- Sources: <comma-separated researcher names from routing table>
- Status: pending
- Coverage: 0 papers, 0 community
- Findings: (none yet)

### SQ2: ...
```

3. `TaskCreate` one task per sub-question (`subject="SQ{n}: <text>"`, `description=<sources to dispatch>`). Plus 5 fixed tasks: `discover`, `dedup`, `deep-read`, `critique`, `synthesize`. Set up `addBlockedBy` so synthesize blocks on critique blocks on deep-read blocks on dedup blocks on discover.
4. If `--confirm-plan` flag is set OR shape is `comparative` AND user did not specify axes → `AskUserQuestion` to confirm sub-questions before stage 2.
5. Log `{stage:1, event:"plan_committed", subquestions_count:N}`.

## Stage 2 — Parallel Discovery

For each sub-question, look up its `Sources:` list in `plan.md`. Build a list of `(researcher, scoped_query, sub_q_id, output_path_prefix)` tuples. Cap total dispatches at **15** for standard, **20** for deep. If over the cap, drop lowest-scored sources.

**Dispatch all in a single Agent tool message with one call per researcher.** They run concurrently. Each prompt:

```
Topic: <original topic>
Sub-question (SQ{n}): <question text>
Time horizon: <year_min>..<year_max>
Output: write findings to ~/.claude/research/<slug>/findings/<source>-SQ{n}-<6-char-hash>.md
       (where <hash> = first 6 chars of sha1(query_used))
Format: YAML frontmatter + markdown per the schema in your agent definition.
Max results: <budget per researcher, see depth mode>
```

For each dispatch, before the Agent call: log `{stage:2, event:"dispatched", researcher, sq, id, ts}`. After: log `{stage:2, event:"returned", id, status, count, ts}`.

Timeout per researcher: 180s. If timeout, mark the SQ as `partial` in `plan.md` and continue.

After all dispatches return, log `{stage:2, event:"stage_complete"}`.

## Stage 3 — Collation, Dedup, Citation Graph

This stage is conductor work + 1-2 mutator agents. Sequential.

1. **Parse all `findings/*.md` frontmatter.** Validate per the Researcher Output Schema (below). For each invalid finding, re-dispatch the responsible researcher ONCE with explicit clarifying instruction. If still bad, log `{event:"dropped_finding", path, reason}` and drop.
2. **Dispatch `embedding-indexer`** — give it the path prefix and ask it to embed all new findings + papers, write to `embeddings.json`, dedup pairs with cosine sim > 0.92.
3. **Dispatch `citation-graph-builder`** — give it the seed list of canonical paper IDs from dedup output. Walks 1 hop (standard) or 2 hops (deep). Edits `citation-graph.json`.
4. **Compute coverage map**: per SQ, count distinct canonical papers + distinct community sources.
5. **Compute deep-read candidate list**: for each canonical paper, `score = 0.6 * cosine(paper, original_query) + 0.4 * normalized_centrality`. Take top N per depth mode (10 standard, 30 deep).
6. **Coverage rebalance check** (free, doesn't count toward replan cap): if `max(coverage) - min(coverage) >= 5 papers` AND `min(coverage) <= 1`, dispatch alt-source for under-covered SQ; dispatch additional researcher for over-covered (using cluster results to split into 2 effective sub-queries). This is a stage-2-fragment, not a full replan.
7. Log `{stage:3, event:"stage_complete", coverage_map, candidate_count}`.

If mode is `quick`: SKIP stages 4-7, jump to a digest output (top results per source, no synthesis), then stage 8.

## Stage 4 — Deep-Read on Top Papers

Dispatch `paper-reader` for each candidate, **batched at 5 concurrent per Agent message**. If candidate count > 5, run successive waves.

Each prompt:
```
Paper: <arxiv-id or DOI>
Sub-question context: <which SQ(s) this paper informs>
Output: write to ~/.claude/research/<slug>/papers/<id-slug>.md
       (id-slug = arxiv ID or DOI percent-decoded with / → -)
Format: per your agent definition (frontmatter + TL;DR / Claims / Method / Evaluation / Limitations / Related Work / Concerns)
```

Idempotency: paper-reader checks if the target file exists with valid frontmatter and returns `{status: skipped}` if so.

Log dispatched/returned per paper. After all waves complete, log `{stage:4, event:"stage_complete", papers_read:K}`.

## Stage 5 — Critique

Dispatch all three critics **in a single Agent message** (3 concurrent calls):
- `cs-domain-expert` — produces `analysis/domain-map.md` and edits `plan.md` with refined SQ list + missing-perspectives section
- `methodology-critic` — produces `analysis/methodology-review.md` with per-paper rubrics
- `contradiction-finder` — produces `analysis/contradictions.md` with `## C{n}` entries

Each critic gets the project root directory and reads what it needs. They are read-mostly; only `cs-domain-expert` edits `plan.md`.

After all return, log `{stage:5, event:"stage_complete"}`.

## Stage 6 — Replan Decision

This is your decision (no subagent). Inputs: outputs from stage 5, coverage map, iteration counter, token budget remaining.

Apply rules in order:

```
if iteration_count >= depth.max_replans:
    decision = "proceed"; reason = "iteration_cap"
elif tokens_used > 0.8 * budget:
    decision = "proceed"; reason = "budget"
elif last_replan_added_papers < 2:
    decision = "proceed"; reason = "saturation"
elif domain_expert.has_critical_missing_perspective:
    decision = "widen"; new_sqs = [...]
elif contradiction_finder.has_under-evidenced_contradiction:
    decision = "widen"; new_sqs = ["Does <claim> hold under <conditions>?"]
elif domain_expert.flags_papers_under_deep_read:
    decision = "deepen"; new_candidates = [...]
else:
    decision = "proceed"
```

If decision is `widen` or `deepen`:
- Edit `plan.md`: append `## Revision N (<timestamp>)` block with `Trigger:`, `Added:`, `Removed:`, `Reason:` lines, and the new SQ definitions.
- Increment iteration counter.
- For `widen`: jump back to stage 2 dispatching only the new SQs.
- For `deepen`: jump back to stage 4 with the new candidate list.
- Log `{stage:6, event:"replan", decision, reason, ts}`.

If decision is `proceed`: log `{stage:6, event:"proceed", ts}` and continue to stage 7.

**User-in-loop check at this stage**: if `widen` and `deepen` scores are within 0.15 of each other AND budget > 50% remains, call `AskUserQuestion` to choose. Otherwise default to `widen`.

## Stage 7 — Synthesis

Dispatch `synthesizer` (single Opus call). Give it the project root and the rule that it MUST reference every `## C{n}` from `analysis/contradictions.md`. After return, validate `synthesis.md` exists and is ≥2k chars; if not, retry once with explicit reminder. Log `{stage:7, event:"stage_complete"}`.

## Stage 8 — Present

1. Read `synthesis.md`'s Executive Summary section.
2. Print to terminal:
```
Research complete: <topic> (depth=<mode>)
  Sub-questions: <N> (<M> added during replan)
  Papers: <X> (<Y> deep-read)
  Sources: <breakdown>
  Contradictions surfaced: <K>
  Iterations: <discovery_rounds> discovery, <replan_rounds> replan

Synthesis:  ~/.claude/research/<slug>/synthesis.md
Plan:       ~/.claude/research/<slug>/plan.md
Transcript: ~/.claude/research/<slug>/transcript.jsonl

Top 3 findings:
  1. <one-liner>
  2. <one-liner>
  3. <one-liner>
```
3. `TaskUpdate` mark all tasks complete.
4. Edit `INDEX.md` to flip status from `active` to `complete`, fill in paper count and synthesis path.
5. Log `{stage:8, event:"complete"}`.

---

## Decomposition Heuristics — Question Shape Templates

### Survey ("what are the main approaches to X?")
4–6 sub-questions split by approach family + 1 tradeoffs SQ + 1 open-problems SQ.
Example — "transformer optimization techniques":
- SQ1: Attention-level optimizations (FlashAttention family, sliding window, sparse, linear)
- SQ2: Memory-level optimizations (KV cache compression, paging, offloading)
- SQ3: Quantization (GPTQ, AWQ, FP8/FP4)
- SQ4: Parallelism schemes (tensor, pipeline, sequence, context)
- SQ5: Tradeoffs between axes
- SQ6: Open problems as of 2026

### Comparative ("X vs Y")
4–5 sub-questions on axes from `{performance, correctness, ergonomics, ecosystem, scalability, cost, maturity}`.
Example — "tokio vs smol vs async-std":
- SQ1: Architectural difference (scheduler design)
- SQ2: Performance benchmarks
- SQ3: Ecosystem coverage
- SQ4: Maturity & maintenance
- SQ5: Practitioner consensus

### State-of-the-art
3–5 sub-questions with strict date filter + 1 contested-claims SQ. Heavy `community` weight (HN buzz, r/MachineLearning).

### Practitioner ("how do real teams do X?")
3–4 sub-questions: patterns / anti-patterns / tooling / case studies. Minimal arxiv; heavy `web`+`community`+`github`.

### Historical/foundational
4 sub-questions traversing citation graph backward to seminal work. Heavy `scholarly`; uses `citation-graph-builder` aggressively.

### Mechanism ("how does X work internally?")
3–5 sub-questions decomposing mechanism into stages. Heavy paper-reader weight in stage 4.

---

## Source Routing Table

For each SQ kind, score each researcher 0.0–1.0. Pick top 3 (top 5 in deep mode). Apply CS-subfield bias as a final adjustment.

| SQ kind | arxiv | scholarly | web | community | github |
|---|---|---|---|---|---|
| Theoretical / algorithmic | **1.0** | **0.9** | 0.3 | 0.2 | 0.4 |
| Practical / applied | 0.4 | 0.5 | **0.9** | **0.9** | **0.9** |
| State-of-the-art | **1.0** (date filter) | 0.4 | 0.6 | **0.8** | **0.8** |
| Historical / foundational | 0.5 | **1.0** | 0.4 | 0.1 | 0.2 |
| Comparative benchmarks | 0.6 | 0.5 | **0.8** | **0.9** | **0.9** |
| Practitioner patterns | 0.1 | 0.2 | **0.9** | **1.0** | **0.8** |
| Mechanism / internals | **0.9** | **0.7** | 0.5 | 0.3 | **0.7** |
| Open problems | **0.7** | **0.6** | 0.5 | **0.6** | 0.2 |

CS subfield bias (added to base scores):
- ML / AI → arxiv +0.1, community +0.1
- Systems → github +0.2
- Theory → scholarly +0.2
- HCI → web +0.1, community +0.1
- Security → arxiv +0.1, community +0.1 (CTF writeups)
- Compilers / PL → arxiv +0.1, github +0.1

`scholarly` in this table maps to the `research-academic-graph` subagent (which itself fans out to Semantic Scholar, OpenAlex, Crossref, DBLP).

---

## Researcher Output Schema (validated in stage 3)

```yaml
---
sub_question_id: SQ2
researcher: arxiv
researcher_run_id: arx-7f3a
query_used: "FlashAttention memory-IO 2024..2026"
results_count: 8
status: ok | partial | failed
papers:
  - id: arxiv:2307.08691
    title: "FlashAttention-2: Faster Attention..."
    authors: ["Tri Dao"]
    year: 2023
    abstract: "..."
    url: "https://arxiv.org/abs/2307.08691"
    cites: ["arxiv:2205.14135"]
    relevance_self_score: 0.92
notes: |
  Free-form summary of what was found and what wasn't.
---

# Findings: SQ2 (arxiv researcher)

## Summary
<3-5 sentences>

## Key results
- bullet list with paper-id citations
```

Validation checks:
1. Frontmatter parseable.
2. `papers[]` non-empty when `status: ok`.
3. Paper IDs match `arxiv:NNNN.NNNNN`, `doi:10\.\d+/.+`, `url:https?://`, or `pwc:<slug>`.
4. Each `abstract` ≥50 chars.
5. Off-topic check: embed `query_used` and median paper title; if cosine < 0.55 → flag `off_topic`.
6. Hallucination spot-check: WebFetch one random URL from the list; if title diverges from returned content (cosine < 0.6) → flag `suspect`.

Failures: re-dispatch ONCE with explicit reminder; second failure → drop + log.

---

## Failure Handling Quick Reference

| Failure | Response |
|---|---|
| Researcher timeout (>180s) | Mark SQ partial, continue |
| API 429 / rate limit | Researcher's own backoff; if persistent, switch to alternate source via routing table |
| Garbage output | Re-dispatch once with explicit format reminder |
| Conflicting claims | Surfaced by contradiction-finder; never suppress |
| Source unreachable | Continue with remaining; flag in synthesis methodology |
| Embedding MCP down | Fall back to title+author exact-string dedup; warn in synthesis |
| Conductor crash | `/research-resume <slug>` reconstructs from disk |
| Budget overrun | Cancel pending invokes, jump to stage 7 with what's committed |

---

## Resume Mode

When invoked with an existing `<topic-slug>`:
1. Read `transcript.jsonl` to find last `{event: stage_complete}`.
2. Enumerate `findings/`, `papers/`, `analysis/` to detect partial state within the next stage.
3. Re-dispatch only events that have `dispatched` without matching `returned`.
4. Continue from where you left off. Do NOT re-run completed stages — idempotency at the file level handles overlap, but you should not waste tokens on it.
