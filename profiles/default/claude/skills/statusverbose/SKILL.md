---
name: statusverbose
description: Switch statusline to verbose mode (shows rate limit bars and time bars)
user-invocable: true
---

Switch the statusline to verbose mode for this session. Run the following command and confirm to the user:

```bash
sid=$(cat ~/.claude/session-by-ccpid/$PPID 2>/dev/null)
[ -z "$sid" ] && sid=$(cat ~/.claude/.current-session-id 2>/dev/null)
if [ -n "$sid" ]; then
  echo verbose > ~/.claude/statusline-mode-${sid}
else
  echo verbose > ~/.claude/statusline-mode
fi
```

Tell the user: "Statusline switched to verbose mode (this session only)."
