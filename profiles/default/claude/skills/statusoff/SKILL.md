---
name: statusoff
description: Switch statusline to off mode (prints nothing — silent)
user-invocable: true
---

Switch the statusline to off mode for this session. Run the following command and confirm to the user:

```bash
sid=$(cat ~/.claude/session-by-ccpid/$PPID 2>/dev/null)
[ -z "$sid" ] && sid=$(cat ~/.claude/.current-session-id 2>/dev/null)
if [ -n "$sid" ]; then
  echo off > ~/.claude/statusline-mode-${sid}
else
  echo off > ~/.claude/statusline-mode
fi
```

Tell the user: "Statusline switched to off mode (this session only)."
