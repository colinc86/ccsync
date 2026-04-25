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
  category/         classify paths (agents / skills / commands / hooks / output_styles / mcp_servers / claude_md / memory)
  config/           ccsync.yaml load/save with .bak rollback; profile resolution
  crypto/           chacha20-poly1305 + scrypt; optional repo encryption
  discover/         walk the six ~/.claude content roots honoring .syncignore
  doctor/           integrity checks + auto-fix for repo/snapshot/encryption state
  gitx/             clone/pull/commit/push wrappers that translate errors to plain English
  harness/          cross-machine sync-scenario fixtures (test-only)
  humanize/         plural / path-tilde / relative-time formatters
  ignore/           .syncignore matcher (gitignore-syntax wrapper)
  jsonfilter/       JSONPath-lite include/exclude/redact engine (per-profile rules only)
  manifest/         per-commit file ledger (SHA / size / mtime / author)
  mcpextract/       extract / build / inject the JSON-slice managed files
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

## Scope (v0.9.0+)

User-global only. No project-scoped `.claude/` yet — deferred.

ccsync syncs *content* — not whole config files. Settings stay
machine-local; only the things that meaningfully move between machines
ride the repo.

Tracked content directories:
- `~/.claude/agents/` · `~/.claude/skills/` · `~/.claude/commands/`
- `~/.claude/hooks/` · `~/.claude/output-styles/`
- `~/.claude/memory/` (on/off only — no drill-down)

Tracked top-level file:
- `~/.claude/CLAUDE.md`

JSON slices, extracted on push and injected on pull via `mcpextract`:
- `~/.claude.json:$.mcpServers` ↔ `profiles/<n>/.ccsync.mcp.json`
- `~/.claude/settings.json:$.mcpServers` ↔ `profiles/<n>/ccsync.mcp.json`
- `~/.claude/settings.json:$.hooks` ↔ `profiles/<n>/ccsync.hooks.json`

Everything else under `~/.claude.json` and `~/.claude/settings.json` is
*not* synced. ccsync reads those files only to splice the named
subtrees out (push) or back in (pull); every other key in the source
file (sessionId, theme, oauthAccount, permissions.allow, …) stays
exactly where the user put it.

Content toggles in `state.ContentToggles` (Settings → content) gate
each chunk on/off independently. Default-on for fresh installs.

`.syncignore` uses gitignore syntax. The v0.9.0 default is intentionally
tiny — `.DS_Store`, `*.bak`, `*.swp`, `Thumbs.db`. The long v0.8.x
exclude list (caches, sessions, telemetry, projects, plugins, …) is
gone because none of those paths are even in the discover walk now.

Sync modes (persisted in `state.SyncMode`, see `internal/state/state.go`):
- `auto` (default, or empty) — file watcher runs; clean syncs auto-apply
- `approve` — auto-apply modifications and deletes, but route add-new actions into the review screen for per-item allow/deny. Gate against accidentally propagating half-finished content across the fleet.
- `manual` — preview every sync; nothing auto-applies

## Merge strategy

Per-file three-way compare:
- `base` = the repo blob at `state.LastSyncedSHA[profile]` (this
  machine's last-known consensus commit). First sync for a profile
  has no base and takes-remote on any conflict.
- `local` = current bytes on disk (or absent).
- `remote` = current bytes at origin/master (post-fetch + reset).

| File kind                                                          | Strategy                                                                                                                                          |
|--------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------|
| Managed JSON slice (`.ccsync.mcp.json`, `ccsync.mcp.json`, `ccsync.hooks.json`) | Per-key three-way deep-merge via `internal/merge.JSON`. Concurrent edits to *different* MCP servers / hook events clean-merge; same-key edits surface as conflicts. The pull side is then re-injected via `internal/mcpextract` so non-synced keys in the source file (`~/.claude.json`, `~/.claude/settings.json`) survive untouched. |
| Markdown (agents / skills / commands / CLAUDE.md / memory / output-styles)      | Text three-way merge via go-diff-match-patch. Overlapping edits surface as a file-level conflict.                                                 |
| Opaque / binary (hook scripts, etc.)                               | Last-write-wins by mtime. No merge attempted.                                                                                                     |

Rule: **never silently lose data**. Anything uncertain goes to the TUI conflict picker, not an automatic resolution.

Conflict policy (persisted in `state.ConflictPolicy`, helpers in `internal/sync/conflict_policy.go`):
- `ask` (default, or empty) — show the picker
- `local` — this machine's bytes win; `ResolutionsFromPolicy` hands `LocalData` back to `ApplyResolutions`
- `cloud` — repo bytes win; `RemoteData` is written to local

`AnyDeleteVsModify` is the hard escape. When any conflict in the batch is delete-vs-modify (one side absent, or a `merge.ConflictJSONDeleteMod` sub-conflict), the automation bails and the picker opens — regardless of policy. Silently winning a fight where "winning" means erasing the other side's unrelated edit is too destructive to automate.

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
- Multi-user / team profiles with permissions
- A web UI

## Conventions for contributors (and Claude)

- Prefer `go-git` over shelling out to `git`
- All user-facing strings route through `internal/theme` for consistent styling
- Never print raw git errors — wrap in `gitx` and translate
- Machine-local paths should never end up committed; if in doubt, add to the default `.syncignore`
- Redact, don't omit, when we detect a secret — preserves JSON shape across machines (current target: `mcpServers.*.env.*` inside the managed MCP files)
- The discover walk is narrow by design — `internal/discover.ContentDirs` is the single source of truth for which subtrees ccsync sees. Don't widen it without a corresponding content toggle + `state.ContentChunk*` constant + Settings row.
