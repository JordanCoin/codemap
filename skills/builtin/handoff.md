---
name: handoff
description: Build and consume handoff artifacts for multi-agent work. Use when switching between Claude, Codex, Cursor, or other AI agents.
priority: 5
keywords: ["handoff", "agent", "switch", "continue", "resume", "context"]
---

# Multi-Agent Handoff

## Building a handoff

```bash
codemap handoff .                 # Build + save full artifact
codemap handoff --json .          # Machine-readable for other tools
codemap handoff --prefix .        # Stable context only (hub summaries, file count)
codemap handoff --delta .         # Recent work only (changed files, risk, events)
```

## What's in a handoff

- **Prefix** (stable): project file count, hub summaries — changes rarely
- **Delta** (dynamic): changed files, risk files, recent edit events, next steps
- **Hashes**: deterministic hashes for cache validation across agents

## Consuming a handoff

```bash
codemap handoff --latest .        # Read most recent saved artifact
codemap handoff --detail file.go . # Lazy-load full context for one file
```

The MCP tool `get_handoff` provides the same capabilities for tool-based consumption.

## When to handoff

- Switching from Claude to Codex (or vice versa)
- Starting a new Claude session on the same branch
- Context window getting full — compact and resume
- Passing work to a teammate's AI session

## Artifacts written

| File | Content |
|------|---------|
| `.codemap/handoff.latest.json` | Full artifact (all layers) |
| `.codemap/handoff.prefix.json` | Stable prefix snapshot |
| `.codemap/handoff.delta.json` | Dynamic delta snapshot |
| `.codemap/handoff.metrics.log` | Append-only metrics stream |
