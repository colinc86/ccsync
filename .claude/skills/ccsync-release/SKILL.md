---
name: ccsync-release
description: Release checklist for ccsync. Covers version bump, full verify pipeline, release notes, commit + tag, and user-machine recovery instructions. Load when cutting any new release (patch, minor, or major).
user-invocable: true
---

# ccsync: release checklist

Every v0.6.x release in this session used this procedure. Keep
doing it. The steps aren't optional — they're the reason the v0.6.4
recovery instructions landed in the user's inbox without a second
round-trip.

## Prechecks (before you touch anything)

- `git status` — clean worktree on `main`. If not, stop. Do not
  mix a release with unrelated changes.
- `git log main..origin/main` — you're up-to-date with origin. If
  the user's sync commits have landed since your last pull (they
  share the repo), `git pull --rebase origin main` first.
- The fix or feature you're about to release has:
  - A test that pins it (from `ccsync-repro-first`).
  - The test revert-proof: reverting the fix makes the test fail.
  - `/ccsync-verify` green.

If any of these is missing, go back to `ccsync-repro-first`. Do
not bump the version to work around a weak test.

## 1. Decide the version

Semver. For ccsync specifically:

- **Patch** (0.6.4 → 0.6.5) — bug fix, no behavior changes for
  users on the happy path.
- **Minor** (0.6.x → 0.7.0) — new feature, new CLI flag, new
  policy option, breaking change to `state.json` or `ccsync.yaml`
  schema (with a migration).
- **Major** (0.x → 1.0) — not yet. Post-v1 we commit to
  backwards-compat for `ccsync.yaml` and `state.json`.

When in doubt, patch. Users run `ccsync` every day; minor bumps
earn extra scrutiny.

## 2. Bump the version variable

File: `cmd/ccsync/main.go` (near the top):

```go
var version = "0.6.4"
```

Change it. `var` (not `const`) so goreleaser's ldflag at
`.goreleaser.yaml` → `-X main.version={{.Version}}` actually
overrides it on release builds. Local `go build` / `make build`
uses the in-file value, so bump it here too — one source of truth
for the fallback, automatic override for tag builds.

## 3. Run the verify pipeline

```
make vet test build
```

Or invoke `/ccsync-verify`. All green. If anything fails, do not
tag — fix first.

## 4. Write release notes

Location: `/tmp/ccsync-vX.Y.Z-notes.md` (ephemeral — surfaced to
the user at the end of the session).

Template:

```markdown
# ccsync vX.Y.Z

## What this fixes / adds

- <one sentence per user-visible change>

## Why it mattered

<1–2 paragraphs, as if explaining to the user who reported it>

## Recovery instructions (if the user needs them)

If your previous version shipped a bug that stuck state onto the
user's machine, include the recovery sequence. Example from v0.6.4:

    curl -fsSL -H "Authorization: Bearer $(gh auth token)" \
      https://raw.githubusercontent.com/colinc86/ccsync/main/scripts/install.sh | bash
    ccsync uninstall --yes
    ccsync

## Internal notes (what actually changed)

- `internal/sync/run.go` — <what + why>
- `internal/harness/scenarios_test.go` — new scenario XYZ
```

## 5. Commit

```
git add -p
git commit -m "Release vX.Y.Z: <one-line user-facing summary>

<optional body, 1-3 short paragraphs>

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
"
```

The one-line summary is what appears in `git log` and release
pages. Make it describe the user-visible change, not the code
change. Good: "Fix first-sync settings.json conflict on new work
machine". Bad: "Add baseCommit empty-check to ActionMerge".

## 6. Tag

```
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin main
git push origin vX.Y.Z
```

Push the tag **separately** from the branch. `git push --follow-tags`
has bitten people — this is explicit.

## 7. Verify goreleaser picked it up

`goreleaser` runs in GitHub Actions on tag push. Check:

```
gh run list --limit 5
gh release view vX.Y.Z
```

The release page should have darwin/linux × amd64/arm64 binaries
after the workflow completes. If it's missing, check the Actions
tab.

## 8. Tell the user

Summarize in the chat:

1. **What shipped** (one sentence).
2. **Whether they need to do anything on their machines.** If a
   regression stuck bad state on disk, point at the recovery
   sequence in the release notes.
3. **What's left open** (if there's orthogonal unfinished work, flag
   it — don't imply the release closes things it doesn't).

## Pitfalls

- **Amending the tag commit.** Don't. The tag already points at
  the SHA. Amend = orphaned tag pointing at a gone commit.
- **Releasing without `--tags` on push.** Then the CI has nothing
  to trigger on; goreleaser won't build.
- **Mixed commits.** "Release v0.6.4 + refactor X + rename Y" is
  three things. The release should be one commit with only the
  version bump + the narrowly-scoped fix it names.
- **Skipping the test-revert proof.** Especially on a patch
  release. If the test wasn't load-bearing, the fix wasn't
  load-bearing either.

## Related

- `ccsync-verify` command — the pipeline step.
- `ccsync-repro-first` — ensures the fix you're releasing is
  actually pinned by a test.
