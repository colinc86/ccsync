---
name: research-status
description: List all research projects under ~/.claude/research/ with status, paper count, sub-question count, last activity, and synthesis presence. Read-only — does not modify anything.
allowed-tools: Read, Bash
---

# /research-status

Show the user a table of all research projects.

## Procedure

1. Read `~/.claude/research/INDEX.md` to get the registered projects list.
2. For each project directory under `~/.claude/research/<slug>/`:
   - Stat the directory for last-modified time.
   - Count files: `papers/*.md`, `findings/*.md`.
   - Read `plan.md` (if exists) frontmatter/header for: depth, sub-question count, current revision count.
   - Check existence of `synthesis.md`.
   - Check existence and last entry of `transcript.jsonl` for status (active / complete / partial / failed).
3. If `INDEX.md` is missing or empty AND there are project directories, walk the directories directly.
4. Sort by last-activity desc.

## Output format

Print to terminal:

```
Research projects (~/.claude/research/):

Slug                                            Status     Papers  Findings  SQs  Synthesis  Last activity
─────────────────────────────────────────────  ─────────  ──────  ────────  ───  ─────────  ─────────────
flashattention-improvements-2026-04-17         complete       12        47    6  yes        2026-04-17 14:32
rust-async-runtimes-2026-04-15                 partial         3        18    5  no         2026-04-16 09:14
postgres-pool-tuning-2026-04-10                complete        8        29    4  yes        2026-04-10 11:05
transformer-kv-cache-quant-2026-04-08          quick           0        22    4  digest     2026-04-08 16:48

To resume:           /research-resume <slug>
To list papers:      /research-list-papers <slug>
To inspect:          cat ~/.claude/research/<slug>/synthesis.md
```

If no projects found:

```
No research projects yet.

Start one with:
  /research <topic>          (standard depth)
  /research-quick <topic>    (~90s shallow pass)
  /research-deep <topic>     (exhaustive)
```

## Hard rules

- Read-only. Do NOT modify any files.
- Do NOT spawn subagents.
- If a project directory looks corrupt (missing plan.md), still list it with status `corrupt`.
- Be fast — this should return in a couple seconds.
