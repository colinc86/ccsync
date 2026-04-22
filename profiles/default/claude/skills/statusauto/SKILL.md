---
name: statusauto
description: Switch statusline to auto mode (shows rate bars, per-model bars, and warnings only when signals warrant it)
user-invocable: true
---

Switch the statusline to auto mode for this session. Run the following command and confirm to the user:

```bash
sid=$(cat ~/.claude/session-by-ccpid/$PPID 2>/dev/null)
[ -z "$sid" ] && sid=$(cat ~/.claude/.current-session-id 2>/dev/null)
if [ -n "$sid" ]; then
  echo auto > ~/.claude/statusline-mode-${sid}
else
  echo auto > ~/.claude/statusline-mode
fi
```

Re-invoking `/statusauto` preserves sticky state — once a trigger has fired in the session (5h, 7d, ctx pressure, per-model rate), it stays visible across mode switches and re-invocations. To manually clear sticky state, run `sid=$(cat ~/.claude/session-by-ccpid/$PPID 2>/dev/null || cat ~/.claude/.current-session-id); rm -f ~/.claude/statusline-auto-sticky-${sid}`.

Tell the user: "Statusline switched to auto mode (this session only)."
