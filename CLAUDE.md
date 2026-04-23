# ccsync — Claude Code settings sync

Sync a user's Claude Code configuration across machines via a git repo, with VSCode-Settings-Sync-style three-way merge and a Bubble Tea TUI. Git work is transparent — the user never sees raw git UX.

## Stack

- **Language:** Go (single static binary, cross-platform)
- **TUI:** `charmbracelet/bubbletea` + `lipgloss` + `bubbles`
- **Git:** `go-git/go-git` (pure Go, no shell-out, works without a local git install)
- **Diff/merge:** `sergi/go-diff` for text; custom JSON deep-merge for structured config

## Repo layout

```
cmd/ccsync/         entry point; subcommand dispatch + TUI bootstrap
internal/
  bootstrap/        first-run clone/init + state seeding
  category/         classify paths (agents / skills / commands / claude_md / mcp_servers / …)
  config/           ccsync.yaml load/save with .bak rollback; profile resolution
  crypto/           chacha20-poly1305 + scrypt; optional repo encryption
  discover/         walk ~/.claude honoring .syncignore
  doctor/           integrity checks + auto-fix for repo/snapshot/encryption state
  gitx/             clone/pull/commit/push wrappers that translate errors to plain English
  harness/          cross-machine sync-scenario fixtures (test-only)
  humanize/         plural / path-tilde / relative-time formatters
  ignore/           .syncignore matcher (gitignore-syntax wrapper)
  jsonfilter/       JSONPath-lite include/exclude/redact engine
  manifest/         per-commit file ledger (SHA / size / mtime / author)
  merge/            three-way merge (text + JSON + binary-LWW)
  profile/          profile CRUD with pre-switch snapshot safety
  secrets/          OS keychain or file-backend secret storage
  snapshot/         pre-sync local backups + rollback
  state/            ~/.ccsync/state.json schema + atomic save
  suggest/          rule-change proposals derived from the current plan
  sync/             the orchestrator — Run, resolve, rollback, encryption migration
  theme/            lipgloss styles + palette
  tui/              Bubble Tea screens + AppContext
  updater/          GitHub self-update (public + authenticated fallback)
  watch/            fsnotify auto-sync loop
  why/              rule-tracer for `ccsync why <path>`
```

## Scope (v1)

User-global only. No project-scoped `.claude/` yet — deferred.

Tracked paths:
- `~/.claude/` (entire tree, minus ignores)
- `~/.claude.json` (if present)

MCP servers live inside `~/.claude.json` under `$.mcpServers` and are
synced through that file's JSON merge rules. Project-scoped `.mcp.json`
is out of scope per the v1 boundary (no project-scoped `.claude/`).

`.syncignore` uses gitignore syntax. Default excludes:
- `settings.local.json` (machine-local by definition)
- Any file matching a secrets heuristic (keys ending in `_key`, `_token`, `apiKey`, etc. are redacted, not omitted — redaction leaves a placeholder so the shape is preserved across machines)

## Merge strategy

Per-file three-way compare:
- `base` = the repo blob at `state.LastSyncedSHA[profile]` (this
  machine's last-known consensus commit). First sync for a profile
  has no base and takes-remote on any conflict.
- `local` = current bytes on disk (or absent).
- `remote` = current bytes at origin/master (post-fetch + reset).

| File kind                              | Strategy                                                   |
|----------------------------------------|------------------------------------------------------------|
| JSON (`settings.json`, `.claude.json`) | Deep merge. On structural conflict, TUI lets user pick per-key or hand-edit. |
| Markdown (agents / skills / commands / CLAUDE.md / memory) | Text three-way merge via go-diff-match-patch. Overlapping edits surface as a file-level conflict. |
| Opaque / binary                        | Last-write-wins by mtime. No merge attempted.              |

Rule: **never silently lose data**. Anything uncertain goes to the TUI conflict picker, not an automatic resolution.

## Profiles

Baked in from v1 to avoid a later refactor.

- Each profile lives under `profiles/<name>/` in the sync repo with its own manifest
- Switching profile swaps files on disk (after a local snapshot, in case of accident)
- One profile is "active" per machine; state tracked in `~/.ccsync/state.json`

## Commit conventions

Every sync is one commit, authored as the current host. Message shape:

```
sync(<profile>): <host> +<added> ~<modified> -<removed>

<per-file bullet list, truncated at N>
```

## Palette (Anthropic-inspired)

Truecolor with 256-color fallbacks via `lipgloss.AdaptiveColor`.

| Role       | Hex       | Usage                            |
|------------|-----------|----------------------------------|
| Accent     | `#D97757` | Clay — focus, selection, cursor  |
| Accent 2   | `#CC785C` | Deeper clay — hover, active tab  |
| Cream      | `#F5F0E8` | Row bg (dark theme), card bg     |
| Ink        | `#2C2926` | Primary text                     |
| Muted      | `#8B857A` | Hints, secondary labels          |
| Success    | `#6B8E4E` | "in sync" / clean merge          |
| Warning    | `#D4A24C` | local newer / remote newer       |
| Conflict   | `#C84A4A` | needs manual resolution          |

Keep it warm and restrained — conflict red is the only loud color. Do not introduce new colors without a strong reason; expand roles by varying weight/style instead.

## Non-goals (for now)

- Project-scoped `.claude/` sync
- Realtime / watch mode (sync is on-demand only)
- Multi-user / team profiles with permissions
- A web UI

## Conventions for contributors (and Claude)

- Prefer `go-git` over shelling out to `git`
- All user-facing strings route through `internal/theme` for consistent styling
- Never print raw git errors — wrap in `gitx` and translate
- Machine-local paths should never end up committed; if in doubt, add to the default `.syncignore`
- Redact, don't omit, when we detect a secret — preserves JSON shape across machines
