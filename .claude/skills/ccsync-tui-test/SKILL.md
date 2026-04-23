---
name: ccsync-tui-test
description: Bubbletea unit-test playbook for internal/tui screens. Covers minimal AppContext scaffolding, driving tea.KeyMsg, extracting tea.Sequence sub-commands via reflection, asserting on switchScreenMsg, and the autoJoinMsg pattern for auto-advancing screens. Load when adding or fixing TUI behavior.
user-invocable: true
---

# ccsync: TUI testing playbook

`internal/tui/` had zero tests pre-v0.6.2 — that's how the profile
picker regression slipped through three releases. It now has two
canonical tests that cover the patterns this skill documents:

- `internal/tui/profilepickerscreen_test.go`
- `internal/tui/bootstrapwizard_test.go`

Read both before writing new ones.

## Core idea

Bubbletea screens are `tea.Model` implementations. Unit-testing them
doesn't need a running `tea.Program` — you construct the model,
drive `Update(tea.KeyMsg)` / other messages, and inspect the returned
`(tea.Model, tea.Cmd)`. Commands are opaque; execute them by calling
them — `msg := cmd()` — and then assert on the concrete `tea.Msg`
type.

## Minimal AppContext

Most screens take `*AppContext`. Build the smallest one that
satisfies the constructor + the code path under test. Use
`t.TempDir()` for any on-disk state so real `~/.ccsync` is never
touched.

```go
ctx := &AppContext{
    State: &state.State{
        LastSyncedSHA: map[string]string{}, // nil-map panics on write
    },
    Config: &config.Config{
        Profiles: map[string]config.ProfileSpec{
            "default": {},
        },
    },
    StateDir: t.TempDir(),
    RepoPath: t.TempDir(),
}
```

If the screen calls `state.Save`, `StateDir` must exist. If it
touches the worktree, `RepoPath` must exist (and you may need to
seed files under `profiles/<name>/claude/`).

The **picker test's `newTestPickerCtx(t, profiles, active, withContent)`**
helper is worth copying into new test files — it parameterizes the
three dimensions that matter (profile list, active profile, is the
subtree populated).

## Driving a keystroke

```go
newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
```

Common keys:

- `tea.KeyMsg{Type: tea.KeyEnter}`
- `tea.KeyMsg{Type: tea.KeyEsc}`
- `tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}` — for letters
- `tea.KeyMsg{Type: tea.KeyUp}` / `KeyDown`

To type a word into a `textinput` inside a screen, drive runes one
at a time:

```go
for _, r := range "work" {
    newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
    m = newModel.(*profilePickerModel)
}
```

## Executing and inspecting a Cmd

```go
cmd := someUpdate(...)
if cmd == nil { t.Fatal("expected a Cmd") }
msg := cmd()         // run the command, get the message it produces
if _, ok := msg.(autoJoinMsg); !ok {
    t.Errorf("expected autoJoinMsg; got %T", msg)
}
```

If the message you want is produced by a *chain* of Update calls,
feed the message back through Update:

```go
msg := cmd()
newModel, _ = m.Update(msg)
```

## The `tea.Sequence` reflection trick

`tea.Sequence(cmds...)` wraps several Cmds in a single Cmd whose
message is an unexported `sequenceMsg` type. You can't type-assert
it directly, but it's a named `[]tea.Cmd` slice — reflection pulls
the elements out cleanly. The canonical helper:

```go
func extractSequenceCmds(t *testing.T, msg tea.Msg) []tea.Cmd {
    t.Helper()
    v := reflect.ValueOf(msg)
    if v.Kind() != reflect.Slice {
        t.Fatalf("expected sequence msg to be a slice; got %s (%T)", v.Kind(), msg)
    }
    out := make([]tea.Cmd, v.Len())
    for i := 0; i < v.Len(); i++ {
        c, ok := v.Index(i).Interface().(tea.Cmd)
        if !ok {
            t.Fatalf("sequence element %d is not a tea.Cmd; got %T", i, v.Index(i).Interface())
        }
        out[i] = c
    }
    return out
}
```

Usage pattern (from `bootstrapwizard_test.go:47-65`):

```go
seqMsg := cmd()
subCmds := extractSequenceCmds(t, seqMsg)
lastMsg := subCmds[len(subCmds)-1]()
sw, ok := lastMsg.(switchScreenMsg)
if !ok {
    t.Fatalf("last seq cmd should produce switchScreenMsg; got %T", lastMsg)
}
if _, isPicker := sw.s.(*profilePickerModel); !isPicker {
    t.Errorf("should push profilePickerModel; got %T", sw.s)
}
```

## The `autoJoinMsg` pattern

Used when a screen may auto-advance (e.g., profile picker on a
freshly-bootstrapped repo). The pattern in
`internal/tui/profilepickerscreen.go`:

1. Constructor inspects state, sets `m.autoJoin = true` when
   conditions are met.
2. `Init()` returns a Cmd that emits `autoJoinMsg{}` when `autoJoin`.
3. `Update()` handles `autoJoinMsg` by finalizing + transitioning.

Tests distinguish the two paths:

```go
// auto-advance case
m := newProfilePickerScreen(ctx) // ctx with fresh empty subtree
if !m.autoJoin { t.Error("should auto-join") }
cmd := m.Init()
if cmd == nil { t.Fatal("auto-join should emit a Cmd from Init") }
msg := cmd()
if _, ok := msg.(autoJoinMsg); !ok {
    t.Errorf("got %T", msg)
}

// must-show case
m := newProfilePickerScreen(ctx) // ctx with populated subtree
if cmd := m.Init(); cmd != nil {
    t.Fatal("must-show Init should be nil; a non-nil Cmd means the user never sees the picker")
}
```

Putting **both** cases in the test suite is load-bearing — v0.6.1
passed the auto-join test because auto-join was too eager; the
must-show test is what pinned it down.

## Asserting on a View

`m.View() string` renders the screen. Use `strings.Contains` for
specific affordances, not whole-output matching:

```go
view := m.View()
if !strings.Contains(view, "create a new profile") {
    t.Errorf("picker view missing create-new affordance; got:\n%s", view)
}
```

Whole-output matches break on theme tweaks, terminal width changes,
etc. Match the text that expresses the user-facing affordance.

## Common pitfalls

1. **Forgetting `Init()`.** Some screens don't do anything in
   `Init()`, but auto-advance screens do. If you test only
   `Update`, you miss whether `Init()` fired the correct Cmd.
2. **Type-asserting the wrong model after Update.** `Update` returns
   `tea.Model`. Assert to the concrete type:
   `m = newModel.(*profilePickerModel)`. If the screen returns a
   *different* type mid-flow (rare but possible), your assertion
   will panic — this is usually a signal your test is modeling the
   wrong transition.
3. **Testing `tea.Sequence` element order.** The order matters —
   `popToRoot` before `switchTo(newScreen)` is the only order that
   doesn't flatten the new screen. Always assert which cmd is
   first vs last.
4. **Driving keystrokes as strings.** `tea.KeyMsg{Type: tea.KeyRunes}`
   with a `Runes: []rune{...}`. Passing a whole string doesn't work.
5. **Not seeding `RepoPath`.** Screens that read
   `filepath.Join(ctx.RepoPath, "profiles", ...)` will silently see
   an empty dir. Use `os.MkdirAll` and `os.WriteFile` to seed
   whatever the test needs.

## Related skills

- `ccsync-repro-first` — when to write a TUI test vs. another kind.
- `ccsync-isolated-run` — for visual verification when a unit test
  can't tell you whether the screen *looks* right.
