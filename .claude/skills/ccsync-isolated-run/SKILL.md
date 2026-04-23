---
name: ccsync-isolated-run
description: Run ccsync against a sandboxed $HOME and a local bare git repo for manual TUI verification. Use when a change affects what the user sees on screen (new screen, reshuffled flow, messaging) and unit tests alone can't confirm "the feature works" matches "the tests pass". Paired with the /ccsync-isolated command.
user-invocable: true
---

# ccsync: isolated manual-run sandbox

`go test` is blind to UX. If a screen renders "setting up…" for 20
seconds and then dumps the user on the wrong view, the tests pass
and the user hates you. This skill gives you a 60-second loop for
driving ccsync against a real tmp `$HOME` + a real local bare repo,
without touching `~/.claude` or any GitHub remote.

## The sandbox

Two tmp dirs:

- `$SANDBOX/home` — fake `$HOME`. Becomes `$HOME/.claude`,
  `$HOME/.claude.json`, `$HOME/.ccsync/`.
- `$SANDBOX/remote.git` — bare git repo for the "sync remote". You
  point ccsync at `file://$SANDBOX/remote.git` during bootstrap.

No real network, no real keychain, no pollution.

## One-shot setup

```bash
SANDBOX="$(mktemp -d -t ccsync-sandbox.XXXXXX)"
export HOME="$SANDBOX/home"
mkdir -p "$HOME"
git init --bare "$SANDBOX/remote.git"

# Optional: seed a fake .claude so you have something to sync.
mkdir -p "$HOME/.claude/agents"
cat > "$HOME/.claude.json" <<'JSON'
{ "theme": "dark", "oauthAccount": { "userId": "fake" } }
JSON
cat > "$HOME/.claude/CLAUDE.md" <<'MD'
# test CLAUDE.md
MD

# Build from current source.
go build -o ./ccsync ./cmd/ccsync

# First time: bootstrap to point this fake HOME at the fake remote.
./ccsync bootstrap --repo "file://$SANDBOX/remote.git"
# Or run the TUI and drive the wizard:
./ccsync

echo "SANDBOX=$SANDBOX"
```

The `SANDBOX` var at the end is the thing to remember — that's
where all state lives if you want to inspect it afterward.

## Multi-machine simulation

Want to verify "machine #2 joining an existing repo"? Run the
sandbox steps twice with different `HOME` dirs pointing at the
same bare remote.

```bash
SANDBOX="$(mktemp -d -t ccsync-sandbox.XXXXXX)"
git init --bare "$SANDBOX/remote.git"

# Machine 1: create content, push.
HOME="$SANDBOX/home1" mkdir -p "$HOME/.claude"
HOME="$SANDBOX/home1" ./ccsync bootstrap --repo "file://$SANDBOX/remote.git"
HOME="$SANDBOX/home1" ./ccsync sync

# Machine 2: join the repo. This is where the profile picker fires.
HOME="$SANDBOX/home2" mkdir -p "$HOME/.claude"
HOME="$SANDBOX/home2" ./ccsync   # TUI — drive the onboarding + picker by hand
```

Pay attention to what the picker shows on machine 2. That's the
exact UX that shipped wrong in v0.6.0–v0.6.3.

## Inspecting state after a run

```bash
# What ccsync thinks the local state is:
cat "$HOME/.ccsync/state.json"

# What's actually in the remote after a sync:
git -C "$SANDBOX/remote.git" log --oneline
git -C "$SANDBOX/remote.git" ls-tree -r HEAD

# Your fake .claude/:
ls -la "$HOME/.claude"
cat "$HOME/.claude.json"
```

## Tear down

```bash
rm -rf "$SANDBOX"
unset HOME  # restore your real $HOME before any further work
```

Critical: `unset HOME` (or re-export it to the real value) before
doing anything else in the same shell. Otherwise you'll run git
commands against your fake home and be confused why keys are
missing.

## When to use this vs. harness scenarios

| Question                                               | Use                                   |
|--------------------------------------------------------|---------------------------------------|
| "Does the merge do the right thing?"                   | harness scenario                      |
| "Does the picker auto-advance on fresh bootstrap?"     | TUI unit test                         |
| "Does the screen *look* right / flow feel right?"      | isolated run (this skill)             |
| "Is there a visible regression the user would notice?" | isolated run                          |
| "Did we break something the tests covered?"            | `/ccsync-verify` — don't bother with this |

The harness gives you deterministic behavior assertions. The
isolated run gives you visual truth. Both, for user-visible
changes.

## Pitfalls

1. **Forgetting to `export HOME`.** Setting it in one command line
   (`HOME=... ./ccsync`) only affects that one command. For the
   TUI, which stays running, export it for the shell.
2. **Real keychain bleed.** `secrets.MockInit()` is only active in
   tests. An isolated run uses your real OS keychain. If you
   bootstrap with a passphrase, the derived key lives in Keychain
   Access until you delete it. For safe experimentation, set
   `CCSYNC_SECRETS_BACKEND=file` — see `internal/secrets/keyring.go:43`.
3. **Leftover fake remotes.** `file://` remotes are cheap to
   create; forgetting to `rm -rf "$SANDBOX"` leaves them in
   `/tmp` where they'll linger until reboot. Not harmful, just
   untidy.
4. **Running `./ccsync` outside the repo root.** The binary looks
   for `ccsync.yaml` relative to the sync repo path, not cwd —
   but I've confused myself by running from `dist/` and getting
   weird errors. Run from the repo root.
5. **Expecting real auth to work.** `file://` remotes don't need
   SSH or HTTPS auth, so bootstrap's auth prompts can be
   skipped / given dummy values. When the flow asks for a key,
   press enter.

## Related

- `/ccsync-isolated` command — bakes the sandbox setup into a
  single invocation.
- `ccsync-tui-test` skill — the unit-test complement. Both are
  needed for TUI changes.
