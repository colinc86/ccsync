# ccsync

Sync your Claude Code config across machines through a git repo you own — no third-party cloud, no data leaving your hands, secrets stay in your keychain.

---

If you use Claude Code on more than one computer, you know the drill: skill you wrote on the work laptop isn't on the personal one, the MCP server you wired up at home is missing on the desktop, the agent you iterated on last week exists in three slightly-different versions across three `~/.claude/` directories. Every few weeks you `scp` a folder around, give up halfway, and tell yourself you'll set up a "real" sync someday.

ccsync is that someday. Point it at a private git repo you control (yours, your team's, `gh repo create --private`, whatever), and it keeps the *content* of your Claude Code installs in sync across every machine — agents, skills, commands, hooks, output styles, memory, CLAUDE.md, and the MCP server slices from `~/.claude.json` and `~/.claude/settings.json`. Settings stay machine-local; only content rides the repo. Three-way merges so concurrent edits don't clobber each other. MCP env vars redact into the keychain so `GEMINI_API_KEY` never lands in git. Pre-sync snapshots so a bad pull is a `ccsync rollback` away. Optional at-rest encryption so even the git host can't read your config. A Bubble Tea TUI that hides git behind keystrokes like `enter` and `space`.

## install

Pick one.

**The usual curl one-liner:**

```sh
curl -fsSL https://raw.githubusercontent.com/colinc86/ccsync/main/scripts/install.sh | bash
```

**Download from releases.** Tarballs for darwin + linux × amd64 + arm64 are published on every tag: [github.com/colinc86/ccsync/releases/latest](https://github.com/colinc86/ccsync/releases/latest). Extract, drop `ccsync` somewhere on your `PATH`, done.

**Build from source.** Go 1.22 or newer:

```sh
git clone https://github.com/colinc86/ccsync.git
cd ccsync
go build -o ccsync ./cmd/ccsync
```

## 30-second first run

```sh
ccsync
```

1. **Onboarding wizard.** Pick a sync policy. `auto` (default) is "install, sync, forget." If that scares you, pick `approve` or `manual` — you can change it later in Settings.
2. **Bootstrap.** Either paste the URL of an existing git repo, or let ccsync run `gh repo create --private` for you. If the repo already has ccsync content on it (other machines pushed first), ccsync fetches it; if the repo's empty, this machine is the first.
3. **Profile picker.** On a fresh repo, there's just one profile (`default`). On an existing repo, you'll see a list of what's already there — pick one to join, or create a new profile for this machine that inherits from `default`.
4. **First sync.** ccsync walks the six content directories under `~/.claude/`, picks up the top-level `CLAUDE.md`, and extracts the MCP-server slices (`$.mcpServers` from `~/.claude.json` and `~/.claude/settings.json`) plus the hook wiring (`$.hooks` from `settings.json`) into per-profile managed files. Secrets in MCP env blocks are redacted into the OS keychain on the way up.

Every subsequent machine is the same four steps.

## what gets synced (and what doesn't)

ccsync v0.9.0 syncs *content*, not whole config files. Settings stay machine-local.

**In scope:**
- `~/.claude/agents/` · `~/.claude/skills/` · `~/.claude/commands/`
- `~/.claude/hooks/` · `~/.claude/output-styles/` · `~/.claude/memory/`
- `~/.claude/CLAUDE.md`
- `~/.claude.json:$.mcpServers` (extracted to `profiles/<n>/.ccsync.mcp.json`)
- `~/.claude/settings.json:$.mcpServers` (→ `ccsync.mcp.json`)
- `~/.claude/settings.json:$.hooks` (→ `ccsync.hooks.json`)

**Out of scope** (read on push, written on pull, never tracked as a whole):
- Everything else in `~/.claude.json` and `~/.claude/settings.json` — `theme`, `sessionId`, `oauthAccount`, `permissions.allow`, every cache/telemetry/feature-flag key, all of it. ccsync touches the source files only to splice the named subtrees in or out.
- Everything else under `~/.claude/` — `cache/`, `sessions/`, `projects/`, `plugins/`, `logs/`, `telemetry/`, `statusline-*`, etc.

Each chunk is independently toggleable in Settings → content. Drill-downs let you uncheck individual skills, MCP servers, or hook events per profile.

## sync modes — pick your vibe

Settings → **sync mode** cycles between three:

- **auto** · install, sync, forget. A file watcher runs while the TUI is open; clean merges apply without a keypress. Default for new installs.
- **approve** · auto-apply edits and deletes, but *pause on new files* before they push or pull. Lets the fleet keep itself in sync while giving you a gate on the half-finished skill you haven't decided to share yet.
- **manual** · nothing moves without a keypress. Every sync shows you a preview; you decide.

Orthogonal to the conflict policy below.

## conflict policy — when two machines disagree

Settings → **when versions diverge**:

- **ask me** · open the picker, you choose per-file. Default.
- **take this machine's** · the primary-workstation vibe. Local wins.
- **take the cloud's** · the mirror vibe. A secondary machine that should track the fleet; local edits lose.

**The escape clause.** Delete-vs-modify conflicts always open the picker regardless of policy. Silently "winning" a fight where your contribution is *a deletion* means erasing the other side's unrelated work, and that's too destructive to automate.

Compose the axes: `auto + take-this-machine's` is the fastest primary-workstation loop. `approve + take-the-cloud's` gates new files but silently accepts fleet edits. `manual + ask-me` is the safety-goggles combo.

## what's inside

**day-to-day**
- Auto-sync with an fsnotify watcher (debounced)
- Approve mode for new-file gating
- Conflict policy for simultaneous-edit defaults
- Command palette (Ctrl+K) for jumping to any screen or action
- Self-update with in-place restart — no manual relaunch after upgrade

**multi-machine**
- Profiles with `extends:` inheritance
- Profile picker on first run per machine
- Profile inspector ("what's in my profile?") — every skill, command, agent, hook, output style, MCP server, grouped, with sync status
- Per-machine denylist for "don't push this one, ever"
- Host-class labels for filtering per machine type

**safety**
- Pre-sync snapshots taken before any local write (including the source files behind managed JSON slices); restore by ID
- `rollback` by snapshot or by git commit SHA
- Three-way merge (text via go-diff-match-patch, per-key JSON deep-merge for managed slices, binary last-write-wins by mtime)
- Refuses to operate on a v0.8.x-shaped repo — old format is detected and the user gets a clean upgrade message
- `doctor` command with auto-fix for integrity drift

**privacy**
- MCP `env.*` redaction routes through the OS keychain; placeholders go to the repo
- OS-keychain-backed secret store, with file-backend fallback via `CCSYNC_SECRETS_BACKEND=file`
- Optional at-rest encryption: chacha20-poly1305 + scrypt-derived passphrase, applied per-blob to everything under `profiles/`

**content surface**
- Settings → content toggles each chunk (agents / skills / commands / hooks / output styles / memory / CLAUDE.md / each MCP slice / hook wiring) on or off
- Per-chunk drill-down lets you uncheck individual items (one specific skill, one MCP server, etc.) for the active profile
- `.syncignore` — gitignore syntax, applied inside the tracked content directories
- `ccsync why <path>` — trace every rule that applied to a file
- `ccsync blame <path>` — per-line git-blame-style attribution for a synced file

**for robots**
- `ccsync sync --dry-run --yes` — scriptable, no TTY needed
- `ccsync watch` — background debounced fsnotify loop
- All headless commands write to stdout/stderr, no TUI required

## command reference

```
ccsync                              launch the TUI
ccsync sync [--dry-run] [--yes]     headless sync
ccsync bootstrap --repo URL         initialize from an existing git repo
ccsync bootstrap --gh-create NAME   create a new private repo via gh CLI
ccsync profile ls|use|create|rm     manage profiles
ccsync snapshot ls                  list pre-sync snapshots
ccsync snapshot restore ID          restore local files from a snapshot
ccsync rollback                     restore local files from the latest snapshot
ccsync rollback --commit SHA        revert repo+local to a specific commit
ccsync doctor [--fix]               run integrity checks (optionally auto-fix)
ccsync why <path>                   trace which rules apply to a path
ccsync blame <path>                 per-line sync attribution for a repo path
ccsync watch [--debounce 10s]       auto-sync on local file changes
ccsync encrypt                      enable repo encryption (prompts for passphrase)
ccsync decrypt                      disable repo encryption
ccsync unlock                       store the passphrase for an already-encrypted repo
ccsync update [--check] [--force]   install the latest release in place
ccsync uninstall [--yes]            remove state, snapshots, secrets, and self
ccsync --version                    print version
```

Everything runs against the state at `~/.ccsync/`. The binary's the only install artifact; uninstall nukes the state dir and keychain entries cleanly and leaves `~/.claude/` alone.

## multi-machine setup in practice

**Machine 1** (fresh repo):

```sh
ccsync bootstrap --gh-create my-claude-settings   # or --repo git@github.com:you/foo.git
ccsync sync
```

The first sync pushes your entire `~/.claude/` (minus ignores, minus redacted secrets) into `profiles/default/` on the repo.

**Machine 2** (joining the fleet):

```sh
ccsync bootstrap --repo git@github.com:you/my-claude-settings.git
```

The TUI's profile picker asks whether to **join `default`** (same config as machine 1) or **create a new profile** like `work` that inherits from default. Most people create a new one — it keeps per-machine drift manageable.

```sh
ccsync profile create work "Work laptop"   # or do it through the picker
# ccsync.yaml now has: work: { extends: default }
ccsync sync
```

`work` starts as a thin overlay: anything not explicitly in `profiles/work/` falls through to `profiles/default/`. Add a `work`-specific skill and it lives only there; every machine on `default` keeps its own thing.

**Encrypt the repo** (optional; pick a passphrase and store it once per machine):

```sh
ccsync encrypt                     # first machine. passphrase → keychain
# on every other machine:
ccsync unlock                      # paste the same passphrase; stored in keychain
```

After `encrypt`, blobs under `profiles/` are ciphertext envelopes (CCSY magic header). Metadata files (`ccsync.yaml`, `.syncignore`, `manifest.json`, `README.md`) stay plaintext so you can still eyeball the repo on GitHub.

## when things go sideways

- **Unexpected conflicts at the resolver.** Run `ccsync sync --dry-run` first to see the plan. If the diff's wrong, `ccsync why <path>` explains which rule marked the file. If the diff's right and you just don't want to think about it, set a conflict policy in Settings.
- **Something looks wrong locally.** `ccsync snapshot ls` shows pre-sync backups; `ccsync snapshot restore <id>` puts you back. `ccsync rollback` with no args restores the most recent.
- **You pushed a bad commit.** `ccsync rollback --commit <sha>` reverts repo-side and local to that commit by creating a *forward* commit (no history rewrite).
- **State feels drifty.** `ccsync doctor` runs integrity checks across the repo, snapshots, encryption state, and manifest. `ccsync doctor --fix` applies the safe fixes automatically.
- **Nuke it.** `ccsync uninstall --yes` removes `~/.ccsync/`, the keychain entries, and the binary. Your `~/.claude/` is untouched.

## contributing

Architecture notes, stack, merge-strategy specifics, and design guardrails live in [CLAUDE.md](./CLAUDE.md). The verify pipeline before every release is:

```sh
go fmt ./...
go vet ./...
go test ./...
go build ./...
```

Repro-first bug workflow: write the failing test pinning the observable bug, *then* the fix, then revert the fix and confirm the test fails with the same message. Details in the `ccsync-repro-first` skill under `.claude/skills/`.

## links

- [Releases](https://github.com/colinc86/ccsync/releases) — tagged builds with tarballs
- [Issues](https://github.com/colinc86/ccsync/issues) — bug reports + feature requests
- [CLAUDE.md](./CLAUDE.md) — contributor docs
