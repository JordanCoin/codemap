# Codemap Hooks for Claude Code

Turn Claude into a codebase-aware assistant. These hooks give Claude automatic context at every step - like GPS navigation for your code.

## The Full Experience

| When | What Happens |
|------|--------------|
| **Session starts** | Claude sees full project tree, hubs, branch diff, and last session context |
| **After compact** | Claude sees the tree again (context restored) |
| **You mention a file** | Claude gets hub context + mid-session awareness (files edited so far) |
| **Before editing** | Claude sees who imports the file AND what hubs it imports |
| **After editing** | Claude sees the impact of what was just changed |
| **Before memory clears** | Hub state is saved so Claude remembers what's important |
| **Session ends** | Timeline of all edits + saves layered handoff artifacts for next agent/session |

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
    "SessionEnd": [
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

### At Session Start (and after compact)
```
📍 Project Context:

╭────────────────────────────── myproject ──────────────────────────────╮
│ Files: 85 | Size: 1.2MB                                               │
│ Top Extensions: .go (25), .yml (22), .md (10), .sh (8)                │
╰───────────────────────────────────────────────────────────────────────╯
myproject
├── cmd/
│   └── hooks      hooks_test
├── render/
│   └── colors     depgraph   skyline    tree
├── scanner/
│   └── astgrep    deps       filegraph  types      walker
└── main.go        go.mod     README.md

⚠️  High-impact files (hubs):
   ⚠️  HUB FILE: scanner/types.go (imported by 10 files)
   ⚠️  HUB FILE: scanner/walker.go (imported by 8 files)

📝 Changes on branch 'feature-x' vs main:
   M scanner/types.go (+15, -3)
   A cmd/new_feature.go

🕐 Last session worked on:
   • scanner/types.go (write)
   • main.go (write)
   • cmd/hooks.go (create)
```

### Before/After Editing a File
```
📍 File: cmd/hooks.go
   Imported by 1 file(s): main.go
   Imports 16 hub(s): scanner/types.go, scanner/walker.go, watch/daemon.go...
```

Or if it's a hub:
```
⚠️  HUB FILE: scanner/types.go
   Imported by 10 files - changes have wide impact!

   Dependents:
   • main.go
   • mcp/main.go
   • watch/watch.go
   ... and 7 more
```

### When You Mention a File
```
📍 Context for mentioned files:
   ⚠️  scanner/types.go is a HUB (imported by 10 files)

📊 Session so far: 5 files edited, 2 hub edits
```

### At Session End
```
📊 Session Summary
==================

Edit Timeline:
  14:23:15 WRITE  scanner/types.go +15 ⚠️HUB
  14:25:42 WRITE  main.go +3
  14:30:11 CREATE cmd/new_feature.go +45

Stats: 8 events, 3 files touched, +63 lines, 1 hub edits
🤝 Saved handoff to .codemap/handoff.latest.json
```

### Next Session Start (Handoff Resume)
If a recent handoff exists **for the current branch**, session start includes a compact resume block:
```
🤝 Recent handoff:
   Branch: feature-x
   Base ref: main
   Changed files: 6
   Top changes:
   • cmd/hooks.go
   • mcp/main.go
   Risk files:
   ⚠️  scanner/types.go (10 importers)
```

---

## Available Hooks

| Command | Claude Event | What It Shows |
|---------|--------------|---------------|
| `codemap hook session-start` | `SessionStart` | Full tree, hubs, branch diff, last session context |
| `codemap hook pre-edit` | `PreToolUse` (Edit\|Write) | Who imports file + what hubs it imports |
| `codemap hook post-edit` | `PostToolUse` (Edit\|Write) | Impact of changes (same as pre-edit) |
| `codemap hook prompt-submit` | `UserPromptSubmit` | Hub context for mentioned files + session progress |
| `codemap hook pre-compact` | `PreCompact` | Saves hub state to .codemap/hubs.txt |
| `codemap hook session-stop` | `SessionEnd` | Edit timeline + writes `.codemap/handoff.latest.json`, `.codemap/handoff.prefix.json`, `.codemap/handoff.delta.json` |

---

## Handoff Command

Use handoff directly when switching between agents:

```bash
codemap handoff .             # build + save handoff
codemap handoff --latest .    # read latest saved handoff
codemap handoff --json .      # JSON payload for tooling
codemap handoff --prefix .    # stable prefix layer only
codemap handoff --delta .     # dynamic delta layer only
codemap handoff --detail a.go . # lazy-load full detail for one changed file
```

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
