# ccsync sync repo

This repo is managed by [ccsync](https://github.com/colinc86/ccsync). It stores
one or more snapshots of a user's Claude Code configuration, redacted so
secrets (API keys, OAuth tokens) live only in each machine's OS keychain.

## Profiles

- `default`

## Last sync

- **host:** electrocampbell
- **active profile:** work
- **time:** 2026-04-22T02:18:20Z UTC

## What's safe to edit by hand

- `.syncignore` — gitignore-syntax rules for what ccsync sends up
- `ccsync.yaml` — JSON include/exclude/redact rules per file

Everything under `profiles/<name>/` is auto-generated. If you hand-edit a
profile file, the next ccsync run will surface that as a three-way conflict
— no silent clobber.
