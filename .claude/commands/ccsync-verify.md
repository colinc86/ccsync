---
name: ccsync-verify
description: Run the full verification pipeline (go fmt, go vet, go test, go build) for ccsync and report per-step pass/fail. Use before claiming any change is done, and always before cutting a release.
allowed-tools: Bash, Read
---

# /ccsync-verify

Run the standard verification pipeline on the current working tree
and report the result. This is the "is my change green" gate — use
it before claiming any feature, fix, or refactor is complete.

## What to run

In order, stopping at the first failure:

1. `go fmt ./...` — should produce no output; non-empty output means
   files were unformatted and you need to commit the formatting
   change.
2. `go vet ./...` — static analysis. Must exit 0.
3. `go test ./...` — full test suite, including `internal/harness/`.
   Must exit 0.
4. `go build ./cmd/ccsync` — sanity build into the repo root as
   `./ccsync`. Must exit 0.

Use the `make` targets when they match (`make vet test build`) —
easier to read than the raw `go` invocations.

## Reporting

Output for the user in this shape:

```
go fmt:    ✓
go vet:    ✓
go test:   ✓ (N packages, M tests)
go build:  ✓ ./ccsync
```

or, on failure:

```
go fmt:    ✓
go vet:    ✗
<first 30 lines of vet output>
```

Stop at the first failure. Don't run later stages against a broken
build — the later output obscures the real cause.

## When to use

- At the end of any code-modifying task, before reporting completion
  to the user.
- Before running the `ccsync-release` checklist.
- After a large refactor, to confirm nothing cross-package
  regressed.

## When NOT to use

- For read-only exploration — no need to verify nothing.
- While iterating on a single failing test — just
  `go test ./internal/<pkg>/ -run TestName -v` instead. Use the
  full pipeline only when you think you're done.

## Related

- `ccsync-repro-first` skill — the failing-test-first workflow.
- `ccsync-release` skill — includes this step as step 3.
