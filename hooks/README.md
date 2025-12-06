# Codemap Hooks for Claude Code

These hooks integrate codemap with Claude Code for automatic, proactive context.

## What are Hooks?

Claude Code hooks run shell commands at specific lifecycle events:
- **SessionStart**: When a conversation begins
- **PreToolUse**: Before a tool executes (with matcher for specific tools)
- **PostToolUse**: After a tool completes

## Available Hooks

### session-start.sh
Shows project structure and hub file warnings at the start of each session.

```json
{
  "hooks": {
    "SessionStart": [{
      "command": "/path/to/codemap/hooks/session-start.sh"
    }]
  }
}
```

### pre-edit-hub-check.sh
Warns before editing hub files (files imported by 3+ other files).

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Edit",
      "command": "/path/to/codemap/hooks/pre-edit-hub-check.sh"
    }]
  }
}
```

## Installation

1. Copy hooks to a permanent location:
   ```bash
   cp -r hooks ~/.config/codemap/hooks
   chmod +x ~/.config/codemap/hooks/*.sh
   ```

2. Add to your Claude Code settings (`~/.claude/settings.json`):
   ```json
   {
     "hooks": {
       "SessionStart": [{
         "command": "~/.config/codemap/hooks/session-start.sh"
       }],
       "PreToolUse": [{
         "matcher": "Edit",
         "command": "~/.config/codemap/hooks/pre-edit-hub-check.sh"
       }]
     }
   }
   ```

## Why Hub Warnings Matter

Hub files are central to your codebase - they're imported by 3+ other files. Editing them:
- Affects more code paths
- Requires more careful testing
- May need broader review

The hooks surface this information automatically, so Claude can factor it into decisions without you having to remember to ask.

## Customization

The hooks are simple shell scripts. Customize thresholds, output format, or add project-specific logic as needed.
