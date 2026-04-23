---
name: ccsync-primitives
description: Reference map of non-obvious internal APIs in ccsync. Covers sync.Run inputs/outputs, manifest.Action semantics, three-way merge, first-sync (baseCommit=="") policy, PreserveLocalExcludes, PromotePath, profile extends, gitx init-vs-clone, secrets keying, and state policies. Load when editing internal/sync, internal/manifest, internal/merge, internal/profile, or internal/state.
user-invocable: true
---

# ccsync: internal primitives map

A map, not a tutorial. Every entry points at the file + line where
the real definition lives — read the code, not a stale paraphrase
here.

## `sync.Run` — the heart of everything

File: `internal/sync/run.go:53`

```go
func Run(ctx, in Inputs, events chan<- Event) (Result, error)
```

`Inputs` (`run.go:28`): profile, claude paths, repo path, state dir,
host ident, auth, dry-run flag, optional `OnlyPaths` (selective
sync).

What it does, in order:

1. `gitx.Open(RepoPath)` → `repo.Fetch` → `repo.SyncToRemote` (hard
   reset local HEAD to origin/master so push stays fast-forward).
2. Discover files in `ClaudeDir` + `ClaudeJSON`. (MCP server
   definitions live inside claude.json under `$.mcpServers`; there's
   no separate `mcp.json` path — that's a project-scoped concept
   outside the v1 user-global boundary.)
3. Compute manifest actions per path (`manifest.Decide`).
4. Apply actions:
   - `ActionAddLocal` / `ActionPull` → write remote bytes to local
   - `ActionAddRemote` / `ActionPush` → queue local bytes for repo
   - `ActionMerge` → three-way merge (`mergeFile`)
   - `ActionConflict` → first-sync take-remote or surface to user
5. If changes, commit + push.

Important wrapper: `RunWithRetry` (`retry.go:21`) — handles
`non-fast-forward` by re-fetching + re-running once.

## Merging

`mergeFile(path, base, local, remote) (result, cleanResult)` —
`internal/sync/run.go:563`. Dispatches to:

- `merge.TextThreeWay` — for text (markdown, source-like)
- `merge.JSONMerge` — deep merge for JSON (`.claude.json`,
  `settings.json`). See `internal/merge/json.go`.
- `merge.BinaryMergeLastWriteWins` — by mtime for opaque/binary.

Returns **two** results: the merged bytes, and a "clean" diagnostic
that tells you whether there were unresolved conflicts even after
strategy application.

## The `baseCommit == ""` first-sync policy (v0.6.4)

File: `internal/sync/run.go`, inside the `ActionMerge` and
`ActionConflict` switch cases.

When `state.LastSyncedSHA[profile]` is empty (machine has never
synced this profile), three-way merge has no base. The policy:
**take remote.** The rationale: "joining an existing profile =
adopting what's there". Without this policy, you get the v0.6.3 bug
(settings.json conflicts on first sync for every second machine).

If you are editing sync behavior and you find yourself writing
another branch for `baseCommit == ""`, re-read the existing branches
first — the policy may already handle your case.

## `manifest.Action*` enum

File: `internal/manifest/decide.go:4-16`.

| Constant              | Meaning                                                      |
|-----------------------|--------------------------------------------------------------|
| `ActionNoOp`          | Nothing to do.                                               |
| `ActionAddLocal`      | New on remote; pull into `~/.claude`.                        |
| `ActionAddRemote`     | New on local; push to repo.                                  |
| `ActionPull`          | Remote changed; fast-forward pull.                           |
| `ActionPush`          | Local changed; fast-forward push.                            |
| `ActionDeleteLocal`   | Remote deleted; delete locally.                              |
| `ActionDeleteRemote`  | Local deleted; delete on remote.                             |
| `ActionMerge`         | Both sides changed; three-way merge.                         |
| `ActionConflict`      | Can't auto-resolve (e.g. delete vs. modify).                 |

`Decide(local, base, remote)` at `decide.go:32` is the decision
table. If you need a new action, you almost certainly don't — think
twice.

## `PreserveLocalExcludes` — machine-local key splicer

File: `internal/jsonfilter/filter.go:121`.

```go
func PreserveLocalExcludes(incoming, localOriginal []byte, excludes []string) ([]byte, error)
```

JSONPath expressions in `excludes` name keys that are machine-local.
The function takes whatever is about to be written to disk
(`incoming`, e.g. merged from remote), splices the local machine's
values in at those paths, and returns the spliced bytes. Used so
`oauthAccount`, `userID`, per-project arrays etc. never leave the
machine.

Call site: `run.go:447`.

Exclude patterns live in the effective profile config — see
`config.Config.Excludes` and `profile.ProfileSpec.Excludes`.

## `PromotePath` — move a file between profile subtrees

File: `internal/sync/promote.go:29`.

```go
func PromotePath(ctx, in, repoRelPath, from, to string) error
```

Used by the UI's "promote this to default so both machines get it"
action. repoRelPath is under `claude/` (e.g.
`claude/agents/foo.md`). Commits + pushes the move as one commit.

## Profile inheritance

File: `internal/profile/profile.go`.

`Create(cfg, cfgPath, name, description)` at `profile.go:19`. A
`ProfileSpec` has an `Extends` field. When a profile extends
another, effective config layering is: parent's rules first, child's
rules override. Implementation lives mostly in `config.Config`'s
effective-rules methods — read there for specifics.

Invariant: cycles in `extends` are caught at config load. If you
edit profile resolution, don't remove that check.

## gitx: `Init` vs. `Clone`

- `gitx.Clone(ctx, url, path, auth)` — `gitx.go:25`. Use when remote
  has commits.
- `gitx.Init(path, remoteURL)` — `gitx.go:37`. Use on an empty remote
  (first sync ever). Creates a local worktree with origin pointing at
  the remote URL.

The harness picks one based on `s.BareHead() != ""`. In application
code, the same decision is made inside bootstrap.

All gitx errors are wrapped with plain-English messages —
`internal/gitx/errors.go`. Don't print raw go-git errors to users.

## Secrets

File: `internal/secrets/`.

- `secrets.Store(key, value)` — write
- `secrets.Load(key)` — read
- `secrets.Key(profile, keyPath)` — compose a key namespaced per
  profile

Backend is the OS keychain on real runs; `secrets.MockInit()` swaps
in an in-memory map for tests.

## `state.State` and review policies

File: `internal/state/state.go`.

- `state.State` struct at `state.go:36` — `ActiveProfile`,
  `LastSyncedSHA` (per-profile), `OnboardingComplete`, `DenyPaths`,
  `DenyMCPServers`, `DirectionPolicies`.
- `Direction` type at `state.go:144` — enum over
  `"push"`, `"pull"` (and possibly `"both"` — check the const
  block).
- `DirectionPolicy` at `state.go:138` — per category (e.g.
  `"settings"`, `"agents"`), per direction, a policy name
  (`"auto"`, `"review"`, `"never"`).
- `Load(stateDir)` at `state.go:361`, `Save(stateDir, s)` at
  `state.go:380`.

Category names (the string keys) live in `internal/category/` — do
not hardcode them in new code, import the constants.

## Current version

File: `cmd/ccsync/main.go:33` — `const version = "0.6.4"` as of this
writing. The release skill (`ccsync-release`) bumps this.

## Non-obvious invariants you will break if you're not careful

1. **`state.LastSyncedSHA` is per profile, not per machine.** A
   machine running both `default` and `work` keeps independent
   base-commits for each.
2. **`in.OnlyPaths != nil` means selective sync.** Selective syncs
   do NOT advance `LastSyncedSHA`, so the un-selected paths remain
   pending next run.
3. **`gitx.Open` → `Fetch` → `SyncToRemote` is `reset --hard` on
   local HEAD.** Any uncommitted local changes to files under the
   worktree (not `~/.claude`) are gone. That's intentional — the
   worktree is a cache of remote, not a second place users edit.
4. **The manifest lives in the repo worktree, not `~/.ccsync`.**
   Per-repo, not per-machine. Keyed by host UUID inside. If you
   mistake its scope you will design bugs into cross-machine flow.
5. **Commit message format is load-bearing.** `sync(<profile>):
   <host> +<a> ~<m> -<r>` — tooling and history UI parse the first
   line. Don't reshape it casually.
