# Codemap Hooks for Claude Code

Turn Claude into a codebase-aware assistant. These hooks give Claude automatic context at every step - like GPS navigation for your code.

## The Full Experience

| When | What Happens |
|------|--------------|
| **Session starts** | Claude sees your project structure and knows which files are high-impact |
| **You mention a file** | Claude automatically gets context about that file |
| **Before editing** | Claude is warned if the file is a hub (imported by many others) |
| **After editing** | Claude sees what other files depend on what was just changed |
| **Before memory clears** | Hub state is saved so Claude remembers what's important |
| **Session ends** | Summary of all files modified and their impact |

---

## Quick Setup

**Tell Claude:** "Add codemap hooks to my Claude settings"

Add to `.claude/settings.local.json` in your project (or `~/.claude/settings.json` globally):

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "codemap hook session-start"
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "codemap hook pre-edit"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "codemap hook post-edit"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "codemap hook prompt-submit"
          }
        ]
      }
    ],
    "PreCompact": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "codemap hook pre-compact"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "codemap hook session-stop"
          }
        ]
      }
    ]
  }
}
```

Restart Claude Code. You'll immediately see your project structure at session start.

---

## What Claude Sees

### At Session Start
```
📍 Project Context:

Files: 85
Top types: .go(25), .yml(22), .md(10), .sh(8)

⚠️  High-impact files (hubs):
   ⚠️  HUB FILE: scanner/types.go (imported by 10 files)
   ⚠️  HUB FILE: scanner/walker.go (imported by 8 files)
   ⚠️  HUB FILE: scanner/deps.go (imported by 5 files)
```

### Before/After Editing a Hub
```
⚠️  HUB FILE: scanner/types.go
   Imported by 10 files - changes have wide impact!

   Dependents:
   • main.go
   • mcp/main.go
   • watch/watch.go
   ... and 7 more
```

### At Session End
```
📊 Session Summary
==================

Files modified:
  ⚠️  scanner/types.go (HUB - imported by 10 files)
  • main.go
  • render/tree.go

New files created:
  + cmd/hooks.go
```

---

## Available Hooks

| Command | When to Use |
|---------|-------------|
| `codemap hook session-start` | Start of session - shows project structure |
| `codemap hook pre-edit` | Before Edit/Write - warns about hub files |
| `codemap hook post-edit` | After Edit/Write - shows file impact |
| `codemap hook prompt-submit` | User message - detects file mentions |
| `codemap hook pre-compact` | Before compact - saves hub state |
| `codemap hook session-stop` | Session end - summarizes changes |

---

## Why This Matters

**Hub files** are imported by 3+ other files. When Claude edits them:
- More code paths are affected
- Bugs ripple further
- Tests may break in unexpected places

With these hooks, Claude:
1. **Knows** which files are hubs before touching them
2. **Sees** the blast radius after making changes
3. **Remembers** important files even after context compaction

---

## Prerequisites

```bash
# macOS
brew install jonesrussell/tap/codemap

# Windows
scoop bucket add codemap https://github.com/jonesrussell/scoop-bucket
scoop install codemap

# Go
go install github.com/jonesrussell/codemap@latest
```

---

## Verify It Works

1. Add hooks to your Claude settings (copy the JSON above)
2. Restart Claude Code (or start a new session)
3. You should see project structure at the top
4. Ask Claude to edit a core file - watch for hub warnings
5. End your session and see the summary
