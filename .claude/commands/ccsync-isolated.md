---
name: ccsync-isolated
description: Spin up a sandboxed $HOME and a local bare git repo, build ccsync, and report the sandbox paths so you can manually drive the TUI without touching the real ~/.claude or any remote. Invoke when a TUI change needs visual verification.
allowed-tools: Bash, Read
---

# /ccsync-isolated

Sets up an isolated sandbox for manual ccsync runs. After this
command, you have a built `./ccsync`, a tmp `$HOME`, and a local
bare remote — drive the TUI, inspect state, and tear down without
touching the user's real config.

The full procedure and rationale live in the `ccsync-isolated-run`
skill. This command is the sandbox-setup half only.

## What to do

1. Create the sandbox.

    ```bash
    SANDBOX="$(mktemp -d -t ccsync-sandbox.XXXXXX)"
    mkdir -p "$SANDBOX/home/.claude"
    git init --bare "$SANDBOX/remote.git"
    ```

2. Build ccsync from the current worktree.

    ```bash
    (cd "$(git rev-parse --show-toplevel)" && go build -o ./ccsync ./cmd/ccsync)
    ```

3. Report back to the user — in the chat — with:

    - Sandbox path: `$SANDBOX`
    - Bare remote: `file://$SANDBOX/remote.git`
    - How to start the TUI:
      ```
      HOME=$SANDBOX/home ./ccsync
      ```
    - How to tear down when done:
      ```
      rm -rf "$SANDBOX"
      ```

4. **Do not** start the TUI from within this command — the TUI is
   interactive and needs the user's actual terminal. Give them the
   invocation to run and wait for their feedback.

## Optional: two-machine sandbox

If the user wants to verify a multi-machine flow (the common case
for bug reports), offer the two-home variant:

```bash
SANDBOX="$(mktemp -d -t ccsync-sandbox.XXXXXX)"
git init --bare "$SANDBOX/remote.git"
mkdir -p "$SANDBOX/home1/.claude" "$SANDBOX/home2/.claude"
echo "Machine 1: HOME=$SANDBOX/home1 ./ccsync"
echo "Machine 2: HOME=$SANDBOX/home2 ./ccsync"
echo "Bare remote: file://$SANDBOX/remote.git"
```

## Safety notes

- `mktemp -d` produces a fresh dir each invocation — no collision
  risk with prior runs.
- Nothing touches `~/.claude`, `~/.ccsync`, or any network. The
  `file://` remote is local.
- If the user's shell is still in the sandbox session (`HOME`
  exported) they should `unset HOME` or start a fresh shell
  before doing anything else. Mention this when reporting.

## Related

- `ccsync-isolated-run` skill — full procedure, multi-machine
  patterns, inspection commands, pitfalls.
- `/ccsync-verify` — for automated verification; use that first,
  then `/ccsync-isolated` only for what tests can't cover.
