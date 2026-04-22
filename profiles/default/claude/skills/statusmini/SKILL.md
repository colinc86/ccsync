---
name: statusmini
description: Switch statusline to mini mode (single-line glyph-driven watch face — model star, ctx icon, dynamic warnings)
user-invocable: true
---

Switch the statusline to mini mode for this session. Run the following command and confirm to the user:

```bash
sid=$(cat ~/.claude/session-by-ccpid/$PPID 2>/dev/null)
[ -z "$sid" ] && sid=$(cat ~/.claude/.current-session-id 2>/dev/null)
if [ -n "$sid" ]; then
  echo mini > ~/.claude/statusline-mode-${sid}
else
  echo mini > ~/.claude/statusline-mode
fi
```

Re-invoking `/statusmini` preserves sticky state — once a trigger has fired in the session (5h, 7d, ctx pressure, per-model rate), it stays visible across mode switches and re-invocations. To manually clear sticky state, run `sid=$(cat ~/.claude/session-by-ccpid/$PPID 2>/dev/null || cat ~/.claude/.current-session-id); rm -f ~/.claude/statusline-auto-sticky-${sid}`.

Tell the user: "Statusline switched to mini mode (this session only)."
