---
name: ccsync-repro-first
description: Repro-first bug workflow for ccsync. When the user reports a bug, use this before editing any code — it produces a failing test that pins the bug, then a fix, then a re-introduction to confirm the test is load-bearing. Invoke on every bug report.
user-invocable: true
---

# ccsync: repro-first bug workflow

The v0.6.0–v0.6.4 sequence was four adjacent fixes in one session
because I edited code before I had a test that would have caught the
bug. Don't do that again. This skill is the procedure.

## When to load

User reports a bug (past session or new). Examples that qualify:

- "it did not ask me if I wanted to create a new profile"
- "on first sync there was a conflict with settings.json"
- "the TUI crashed when I pressed X"
- "my oauthAccount got overwritten"

If the user is asking a *question* or requesting a *feature*, this
skill doesn't apply. Use it only when there's a specific behavior
that is wrong.

## Procedure

### 1. Restate the bug in one sentence

State the bug as observable behavior, not as a diagnosis. Good:

> On machine #2, `ccsync` on first launch silently picked
> `default` instead of offering the profile picker.

Bad (jumps to a cause):

> The picker screen is inside the onboarding model and getting
> unmounted by popToRoot.

The one-sentence restatement is what the test name encodes, and it
forces you to separate **what is observably wrong** from **what you
think the cause is**. Do not skip this step.

### 2. Classify the bug

Pick one of three shapes. This determines where the test goes.

| Shape                       | Signals                                                         | Test goes in                                          |
|-----------------------------|-----------------------------------------------------------------|-------------------------------------------------------|
| Cross-machine sync behavior | Involves ≥2 machines, a profile, pushes/pulls, merge conflicts  | `internal/harness/scenarios_test.go` (new subtest)    |
| TUI screen behavior         | Involves a keystroke, a screen transition, a rendered view      | `internal/tui/<screen>_test.go` (unit test)           |
| Single-file pure-Go logic   | Can be expressed with inputs → outputs, no TUI, no fake FS      | The package's own `_test.go`                          |
| None of the above           | Subtle visual/UX issue only visible in a real terminal          | Write a manual script, run via `/ccsync-isolated`     |

The last row is escape-hatch only. If you can phrase the bug as
"given state X, action Y should produce Z", it belongs in one of the
first three.

Load the relevant companion skill:

- Shape 1 → load **ccsync-harness-author**
- Shape 2 → load **ccsync-tui-test**
- Shape 4 → load **ccsync-isolated-run**

### 3. Write the failing test FIRST

Before any code edit. Before any hypothesis about the fix.

Rules:

1. The test name reads back the observable bug from step 1.
   Good: `TestProfilePickerShowsPickerWhenContentExists`.
   Bad:  `TestFixOnboardingRegression`.
2. The failure message, if the test fails, must tell someone *cold*
   what actually went wrong. Avoid `t.Fatal("bad")`. Prefer:
   `t.Fatalf("picker Init returned a non-nil Cmd when repo has existing content; user may never see the picker (v0.6.0/v0.6.1 regression shape)")`.
3. Leave a comment above the test that names the bug and the
   scenario — future-you will thank you.

### 4. Run the test and confirm it fails

```
go test ./internal/harness/ -run TestFirstSyncTakesRemoteOnSettingsConflict -v
```

Or the relevant package. Read the failure message. If it's vague
or just says "expected X got Y" with no context about *why X
mattered*, rewrite the message until it's specific. The test is as
valuable as its failure message — often more.

### 5. Fix the code

Now, and not before. Keep the fix surgical — you are targeting the
failing test, not every adjacent smell.

### 6. Run the test and confirm it passes

### 7. Prove the test is load-bearing

Revert the fix (`git stash`), re-run the test, confirm the SAME
specific failure appears. This rules out the test accidentally
passing for a different reason. Then `git stash pop` to restore the
fix.

This step is non-negotiable. It's the step you skipped in v0.6.1.

### 8. Run the whole verify pipeline

Invoke `/ccsync-verify`. Everything green, or the fix is incomplete.

### 9. If the bug was user-visible in the TUI

Also spot-check with `/ccsync-isolated` that the actual user flow
now looks right. `go test` can't see a screen that renders
"setting up…" for 20 seconds.

## Anti-patterns

- **Fixing before testing.** If you find yourself opening an editor
  in step 2, stop.
- **Catching-all test names.** `TestBugFix` tells future-you
  nothing; `TestProfilePickerAutoAdvancesOnFreshlyBootstrappedRepo`
  tells them exactly what invariant held.
- **Deleting a test after shipping.** It was cheap to write; it's
  cheap to keep. Flaky tests get fixed, not removed.
- **Asserting `len(conflicts) > 0` when you actually care about
  which path conflicted.** The first wrong commit gets the
  satisfaction of a green test; the second wrong commit passes for
  the same spurious reason. Be specific.

## Related skills

- `ccsync-harness-author` — for cross-machine scenarios.
- `ccsync-tui-test` — for bubbletea unit tests.
- `ccsync-primitives` — reference for the internal APIs you'll
  probably need to touch.
- `ccsync-isolated-run` — manual verification sandbox.
