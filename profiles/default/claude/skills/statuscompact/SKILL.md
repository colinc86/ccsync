---
name: statuscompact
description: Switch statusline to compact mode (hides rate limit bars and dividers)
user-invocable: true
---

Switch the statusline to compact mode for this session. Run the following command and confirm to the user:

```bash
sid=$(cat ~/.claude/session-by-ccpid/$PPID 2>/dev/null)
[ -z "$sid" ] && sid=$(cat ~/.claude/.current-session-id 2>/dev/null)
if [ -n "$sid" ]; then
  echo compact > ~/.claude/statusline-mode-${sid}
else
  echo compact > ~/.claude/statusline-mode
fi
```

Tell the user: "Statusline switched to compact mode (this session only)."
