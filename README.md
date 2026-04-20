# ccsync

Sync your [Claude Code](https://claude.com/claude-code) settings across machines, through a git repo you own.

Think VSCode Settings Sync, but the backend is just a git repository — on GitHub, your self-hosted Gitea, wherever. You keep full history, you can roll back, and nothing lives in a proprietary cloud.

The UX is a Bubble Tea TUI that hides git entirely: three-way merges, per-key JSON conflict resolution, selective sync, profiles, snapshots, rollback — without ever showing you a `git push` error.

## Install

One-liner (macOS, Linux):

```sh
curl -fsSL https://raw.githubusercontent.com/colinc86/ccsync/main/scripts/install.sh | bash
```

Pin a specific version:

```sh
VERSION=v0.2.0 curl -fsSL https://raw.githubusercontent.com/colinc86/ccsync/main/scripts/install.sh | bash
```

Homebrew:

```sh
brew install colinc86/tap/ccsync
```

From source (Go 1.22+):

```sh
go install github.com/colinc86/ccsync/cmd/ccsync@latest
```

## Quick start

```sh
# one-time bootstrap — point ccsync at a git repo you own
ccsync bootstrap --repo git@github.com:you/claude-settings.git

# or let ccsync create a new private repo for you (requires gh CLI auth)
ccsync bootstrap --gh-create claude-settings

# then sync whenever you want
ccsync           # launch the TUI
ccsync sync      # headless
```

On a second machine: run the same `bootstrap` against the same repo URL, then launch `ccsync` — the RedactionReview screen will surface any secrets (API keys, MCP env vars) it needs you to paste back.

## What gets synced

- `~/.claude/` (the whole tree)
- `~/.claude.json`

Machine-local cruft — caches, sessions, per-project state, status-line shell scripts, plugins, telemetry — is filtered out by a default `.syncignore`. The defaults are editable in the TUI Settings screen or by hand.

## Secrets are redacted, not omitted

API keys, OAuth tokens, and MCP environment variables are detected by JSON-path rules in `ccsync.yaml`, stripped from what ccsync commits, and stored in your OS keychain. The repo keeps a placeholder so the JSON shape stays intact:

```json
{ "mcpServers": { "gemini": { "env": { "GEMINI_API_KEY": "<<REDACTED:ccsync:default:$.mcpServers.gemini.env.GEMINI_API_KEY>>" } } } }
```

When you pull on a new machine, ccsync prompts you for any missing values in **RedactionReview** and writes them to that machine's keychain. Nothing sensitive ever hits git.

If a secret is missing, ccsync refuses to write the JSON file rather than silently persisting the placeholder — Claude Code would break at runtime, and we want the failure loud and local.

## Profiles

Multiple named configurations can live in the same repo under `profiles/<name>/`. Use them for work vs personal, or machine-specific setups.

```sh
ccsync profile create work "work machine config"
ccsync profile use work       # pre-switch backup taken automatically
```

## Safety

The single biggest risk of a sync tool is silent data loss. ccsync defends against it in layers:

- **Local snapshots** — before any on-disk write, ccsync copies the affected files to `~/.ccsync/snapshots/`. Kept 30 deep or 14 days, whichever is larger.
- **Append-only git history** — one commit per sync, never a force push. `ccsync rollback --commit <sha>` creates a new forward commit that matches the target tree.
- **Dry-run preview** — the TUI shows the full plan (per-file add/modify/delete, per-key JSON conflicts) before you press Apply.
- **First-push gate** — if you bootstrap against a non-empty repo, ccsync refuses to push until it has pulled and merged.
- **Config edit safety** — `ccsync.yaml` edits go through temp-file → validate → atomic rename; the previous version is copied to `~/.ccsync/config.bak`.
- **Redaction guardrail** — a dangling redaction placeholder on pull triggers RedactionReview instead of being written to disk.
- **`ccsync doctor`** — integrity checks: manifest consistency, dangling placeholders, profile materialization, snapshot health, repo health.

### Recovery

| Situation | Path |
|---|---|
| Pulled the wrong thing, local files are broken | `ccsync snapshot ls` → `ccsync snapshot restore <id>` |
| Pushed bad content to the repo | `ccsync rollback --commit <prev-sha>` |
| Edit to `ccsync.yaml` broke things | Restore `~/.ccsync/config.bak` |
| Secret fell out of the keychain | Run `ccsync`; RedactionReview surfaces the gap |

## Commands

```
ccsync                              launch TUI
ccsync sync [--dry-run] [--yes]     headless sync
ccsync bootstrap --repo URL         clone and initialize from an existing repo
ccsync bootstrap --gh-create NAME   create a new private repo via the gh CLI
ccsync profile ls|use|create|rm     manage profiles
ccsync snapshot ls                  list local pre-sync snapshots
ccsync snapshot restore <id>        restore local files from a snapshot
ccsync rollback                     restore local files from latest snapshot
ccsync rollback --commit SHA        revert repo+local to a specific commit
ccsync doctor                       run integrity checks
ccsync update [--check] [--force]   install the latest release in place
ccsync --version
```

Every destructive operation has a dry-run or preview by default. `--yes` is available for CI.

## Updating

```sh
ccsync update            # download the latest release, replace this binary
ccsync update --check    # print whether an update is available
ccsync update --force    # reinstall even if already on the latest
```

If ccsync was installed via Homebrew, `update` detects that and tells you to run `brew upgrade ccsync` (so it doesn't replace the file out from under brew's state).

## How it works

- **Pure-Go git** (`go-git/go-git`) — no `git` binary required on the host.
- **State machine** — every tracked path runs through a 15-case decision table comparing `(local_sha, base_sha, remote_sha)` and dispatches to no-op, pull, push, or three-way merge.
- **Merge engines** — JSON files get a structural deep-merge with per-key conflicts; text files (`CLAUDE.md`, agents, skills, commands) use `sergi/go-diff` three-way; binary/opaque files last-write-wins by mtime.
- **Base-of-truth** — per-host `~/.ccsync/state.json` tracks the last-synced commit SHA per profile; base blobs are pulled from that commit, not a shared manifest, so cross-machine bases stay coherent.

For the full design, see [CLAUDE.md](./CLAUDE.md).

## Configuration

Two files in the sync repo, both editable:

- **`.syncignore`** — gitignore syntax, determines what ccsync mirrors.
- **`ccsync.yaml`** — per-file JSON-path `include` / `exclude` / `redact` rules, plus profile definitions.

Both can be edited from the TUI Settings screen (with validation + rollback) or by hand.

## Non-goals (v1)

- Project-scoped `.claude/` sync
- Realtime / watch mode
- Team profiles with permissions

## License

MIT
