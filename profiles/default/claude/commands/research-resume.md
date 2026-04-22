---
name: research-resume
description: Resume a previously started research project. Reads plan.md and transcript.jsonl, finds the last completed stage, and re-dispatches incomplete tasks. Idempotent — already-completed work is skipped via file-existence checks.
allowed-tools: Skill, TaskCreate, TaskGet, TaskList, TaskUpdate, TaskOutput, Monitor, Read, Write, Edit, Bash, AskUserQuestion, WebFetch, WebSearch, mcp__gemini-embedding__embed_text, mcp__gemini-embedding__semantic_search
---

# /research-resume

You are the research conductor resuming a previously started project. The user has invoked `/research-resume` with arguments: `$ARGUMENTS` (which should be the `topic-slug` of an existing project, e.g., `flashattention-improvements-2026-04-17`).

## Procedure

1. **Locate the project directory**: `~/.claude/research/$ARGUMENTS/`. If it doesn't exist:
   - List existing projects from `~/.claude/research/INDEX.md`.
   - Use `AskUserQuestion` to ask which project to resume (offer up to 4 most recent).

2. **Reconstruct state from disk**:
   - Read `<project>/plan.md` to recover the original `ResearchGoal` (depth, time horizon, sub-questions).
   - Read `<project>/transcript.jsonl` end-to-end.
   - Find the last `{event: "stage_complete", stage: N}` entry. The next stage to run is N+1.
   - Within stage N+1, scan for `{event: "dispatched", id: X}` events without matching `{event: "returned", id: X}` — these are the incomplete tasks.

3. **Detect partial state by file inventory**:
   - Stage 2 partial: `<project>/findings/` has some files. Count vs. expected dispatches in transcript → know what to redo.
   - Stage 4 partial: `<project>/papers/` has some files. Same logic.
   - Stage 5 partial: missing one of `analysis/{domain-map,methodology-review,contradictions}.md`. Re-dispatch only the missing critic(s).
   - Stage 7 partial: `synthesis.md` missing. Re-run synthesizer.

4. **Re-dispatch incomplete tasks**:
   - Honor idempotency: every subagent checks if its target file exists with valid frontmatter and returns `{status: "skipped"}` if so. So you can safely re-dispatch the entire stage and only the missing work will execute.
   - For efficiency, prefer dispatching only the explicitly missing items based on transcript analysis.

5. **Continue the pipeline** from stage N+1 onward, applying the same `mode_config` recovered from `plan.md`. Honor the original `max_replans` and budget — if those were already exhausted in the previous run, don't restart them.

6. **Log resume event** at the start: `{stage: <next>, event: "resumed", from_stage: <last_complete>, ts: ...}`.

7. **Output** at the end: per the playbook stage 8, with an additional note about what was resumed: "Resumed from stage N+1; X tasks completed in this resume run."

## Edge cases

- **Project status is `complete`** in `INDEX.md`: tell the user the project is already done, point at `synthesis.md`. Don't re-run unless they explicitly ask.
- **Transcript is corrupt or missing**: fall back to file-inventory-only state reconstruction. If even that fails, ask the user whether to start fresh or abandon.
- **Plan was edited mid-run** (extra revision blocks): respect the latest revision; resume from the most recent stage_complete.
- **More than one project matches the slug**: this shouldn't happen (slugs are timestamped), but if it does, ask the user.

## Hard limits

- Do NOT redo work that has matching dispatched+returned events in the transcript — that wastes tokens.
- Do NOT modify `plan.md` Sub-questions beyond what the playbook normally allows (cs-domain-expert can append; conductor adds revision blocks).
- If you detect the previous run aborted because of a hard error (logged in transcript), surface that to the user before resuming and ask whether to retry or skip the failing step.
