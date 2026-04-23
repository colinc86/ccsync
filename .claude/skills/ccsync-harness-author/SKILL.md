---
name: ccsync-harness-author
description: Template and conventions for authoring cross-machine sync scenarios in internal/harness/scenarios_test.go. Load when a bug or feature involves two or more machines, pushes/pulls, merge behavior, or profile state. Paired with ccsync-repro-first.
user-invocable: true
---

# ccsync: harness scenario authoring

Cross-machine sync bugs live in `internal/harness/scenarios_test.go`.
The harness runs **real** `sync.Run`, **real** `go-git`, **real**
`jsonfilter` — only `$HOME` and the keychain are faked. Every
user-reported cross-machine bug should be reproducible as a
deterministic subtest here.

## API cheat-sheet

Key types in `internal/harness/`:

- `Scenario` — `harness.go:28`. Owns the tmp root + bare repo + shared `config.Config`.
- `Machine` — `machine.go:21`. One simulated node. Has its own `$HOME`, `.claude/`, `.claude.json`, `.ccsync/`, and local clone.

Constructors:

- `NewScenario(t, opts...)` — `harness.go:63`. Creates an empty bare repo + tmp dirs.
- `Scenario.NewMachine(name)` — `machine.go:49`. First machine gets `gitx.Init` (fresh); later machines get `gitx.Clone`.

Seeding (all on `Machine`, all fluent, all in `seed.go`):

- `.WriteClaudeJSON(body)`
- `.WriteClaudeJSONKey(key, value)` — reads-modifies-writes one key
- `.WriteClaudeFile(relPath, content)` — under `~/.claude/`
- `.DeleteClaudeFile(relPath)`
- `.SetSecret(keyPath, value)` — into the shared mock keychain

Profile / policy:

- `.UseProfile(name)` — **must be called before the first Sync**
- `.DenyPath(repoRelPath)`
- `.DenyMCPServer(name)`
- `.SetPolicy(category, state.Direction, policy)`

Driving sync:

- `.Sync() sync.Result` — fails the test on error
- `.SyncExpectErr() (Result, error)` — doesn't fail on error
- `.DryRun() sync.Plan` — plan without apply
- `.SyncAndResolveAll(TakeLocal | TakeRemote) sync.Result` — resolves all conflicts one way
- `.Promote(repoRelPath, from, to)`

Assertions (on `Machine` unless noted, all in `assert.go`):

- `.AssertClaudeJSONKey("oauthAccount.userId", "abc")` — dotted-path deep check
- `.AssertClaudeJSONKeyAbsent("telemetry.apiKey")`
- `.AssertClaudeFile("agents/foo.md", "expected-content")`
- `.AssertNoClaudeFile("agents/foo.md")`
- `.AssertSyncClean()` — fresh DryRun produces no push/pull/conflict
- `Scenario.AssertBareHasPath("profiles/default/claude/agents/foo.md")`
- `Scenario.AssertBareNoPath(...)`

Introspection:

- `Scenario.BareHead() string` — origin/master SHA, or "" if empty
- `Scenario.BareFile(path) ([]byte, bool)` — read path from bare HEAD
- `Scenario.BareCommits() []string` — log on master, newest first, `<short-sha> <subject>` each

## Two-machine template

Starting point for most cross-machine bugs. Copy, rename, fill in.

```go
func TestSomeObservableBug(t *testing.T) {
    s := NewScenario(t)

    home := s.NewMachine("home")
    home.WriteClaudeJSONKey("theme", "dark")
    home.WriteClaudeFile("agents/helper.md", "# helper")
    home.Sync()

    // Now create the second machine. It clones the bare that home
    // just pushed to; first sync here is the interesting moment.
    work := s.NewMachine("work")
    work.WriteClaudeJSONKey("oauthAccount", map[string]any{
        "userId": "work-only-user",
    })
    work.Sync()

    // Assert what should have happened. Be specific — if the test
    // would also pass when some OTHER path showed up, tighten it.
    work.AssertClaudeJSONKey("theme", "dark")
    work.AssertClaudeFile("agents/helper.md", "# helper")
    work.AssertClaudeJSONKey("oauthAccount.userId", "work-only-user")
    s.AssertBareHasPath("profiles/default/claude/agents/helper.md")
}
```

## Different-profile template

For bugs that involve profile inheritance or cross-profile promotion.

```go
func TestCrossProfileBehavior(t *testing.T) {
    s := NewScenario(t, WithProfiles(map[string]config.ProfileSpec{
        "work": {Description: "work machine", Extends: "default"},
    }))

    home := s.NewMachine("home")
    home.UseProfile("default").WriteClaudeFile("CLAUDE.md", "shared").Sync()

    work := s.NewMachine("work")
    work.UseProfile("work").WriteClaudeFile("agents/work-only.md", "x").Sync()

    // work inherits CLAUDE.md from default...
    work.AssertClaudeFile("CLAUDE.md", "shared")
    // ...and its own agents/work-only.md lives under profiles/work
    s.AssertBareHasPath("profiles/work/claude/agents/work-only.md")
    s.AssertBareNoPath("profiles/default/claude/agents/work-only.md")
}
```

## Gotchas (things that burned me in v0.6.x)

1. **`strings.Contains`, not exact match, for `.claude.json`.**
   After write, JSON is re-pretty-printed, so byte-level equality
   against your seed string will fail for whitespace reasons. Use
   `AssertClaudeJSONKey` (structural) or
   `strings.Contains(string(m.ClaudeJSONRaw()), "autoUpdatesChannel")`.
2. **`.UseProfile("work")` must happen before the first `Sync()`.**
   After the first sync the machine's state records the active
   profile and switching mid-test does surprising things.
3. **`NewMachine("work")` only clones if bare has commits.** If you
   call `NewMachine` on a scenario where no prior machine has
   synced, you get a fresh-init worktree, not a clone. That's
   usually fine, but if you want the "machine #2 joins existing
   repo" shape, make sure a prior machine has synced.
4. **`secrets.MockInit()` is shared across the scenario.**
   `SetSecret` on one machine is visible to every machine. That's
   intentional — simulates a user's real multi-machine keychain —
   but test authors sometimes get surprised.
5. **`s.t.Fatalf` via `t.Helper()` fails the *test*, not the
   goroutine.** If you wrap a machine call in `go func(){}()` the
   fatal won't land where you expect; don't do that.
6. **"first sync take-remote" is the v0.6.4 policy.** When a
   machine's `state.LastSyncedSHA[profile]` is empty *and* both
   sides have content for the same path, remote wins. If your
   assertion expects "local wins on first sync", it's wrong — that
   would re-introduce the conflict the user reported. See
   `internal/sync/run.go` around the `baseCommit == ""` branches in
   `ActionMerge` and `ActionConflict`.
7. **Assertions fire inside helper methods.** That means a failed
   assert exits the whole test — you can't assert, recover, and
   continue. If you need soft checks, use plain `t.Errorf` against
   raw state (`m.ClaudeJSONMap()`, `s.BareFile(...)`).

## Two canonical examples

Read these first when adding a new scenario:

- `first_sync_takes_remote_on_settings_conflict` — replays the user's
  v0.6.3 report.
- `first_sync_still_pushes_work_only_content` — the complement
  (prove we didn't over-correct and eat work-unique content).

Both live in `internal/harness/scenarios_test.go`.

## Running one scenario

```
go test ./internal/harness/ -run TestNameOfYourScenario -v
```

The `-v` is worth it — these tests print meaningful context on
failure, and the harness's `t.Fatalf` messages are designed to read
well without further digging.
