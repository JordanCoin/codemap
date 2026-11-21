# Bartender → codemap `ask` (experimental)

Status: **working vertical slice**, opt-in only. Default CLI/MCP behavior is unchanged.

Design credit: [bartender](https://github.com/everoo/bartender) by [everoo](https://github.com/everoo) (Jordan's collaborator). Bartender is design docs today; this prototype brings a first retrieval loop into codemap.

## Goal

Agents using codemap (CLI and MCP) can ask a question about a codebase and get the **smallest sufficient context** built from compact, **deduplicated summary cards** of codemap's existing map — or an explicit **`NO_MATCH`** when nothing is reliable enough — instead of large dumps.

## Concept map

| Bartender / Alexandria idea | Implemented in this slice | Left open |
| --- | --- | --- |
| Alexandria: deduplicated, versioned knowledge store | In-memory cards derived from one `ScanForDeps` + `FileGraph`; stable `id` (`file:` / `package:`) and `revision` (hash of capsule) | Persistent store, hot/warm/cold tiers, append-only provenance, contributor consent |
| Routing card: short label + ~50-token capsule | `Card.Label` (≤15 tokens) + `Card.Capsule` (≤55 tokens) from path, language, ←/→ counts, symbols, match hints | Multi-type graph entries (objective, solution, experience, test), typed edges beyond importer/import basenames |
| Adaptive Fuzzy Retrieval (AFR) | Lexical rank via existing `find.Rank`, adaptive shortlist (score floor + relative keep), byte-budget early stop | Embeddings, bandit answer/expand/clarify, multi-hop expansion, historical revision expand |
| Smallest sufficient answer | `MATCH` with 1–N cards that fit `--budget` / `budget` | LLM synthesis of a prose answer from cards; participant expand round-trip |
| Explicit `NO_MATCH` | Terminal status + message when empty query, no hits, weak scores, or nothing fits budget | Distinguishing "not in map" vs "map incomplete" beyond coverage footer |
| Participating-AI tool instruction | MCP `ask` description tells agents to prefer compact cards over dumps | A/B of tool-use / routing prompts; Governor experiments |
| Learning / outcome reports | Not implemented | Minimal success/failure reports, correction loop into Alexandria |

## Surfaces (opt-in)

```bash
codemap ask "where is theme persistence handled?"
codemap ask --json --budget 900 "hub config loading"
```

MCP tool: `ask` (description prefixed `[EXPERIMENTAL bartender prototype]`).

Schema: `codemap.ask/v1-experimental`.

## What it builds on

- **Scan / graph:** `scanner.ScanForDeps`, `BuildFileGraphFromOutcome`, coverage contract
- **Retrieval vocabulary:** `find.Rank` (same identifier splitting / BM25 style as `codemap find`)
- **CLI / MCP conventions:** subcommand before flags (like `find`), structured MCP output + bounded text, experimental banner on stderr for CLI

It does **not** invent a parallel indexer or replace `find`, `context`, or `get_structure`.

## Open design questions for everoo

1. Should Alexandria cards in codemap stay **derived ephemeral views** of the map, or become a **persisted** `.codemap/` store that survives sessions and accepts corrections?
2. Is lexical rank + adaptive cut enough for the first falsifiable experiment, or should the slice require an embedding / hybrid path before calling the experiment "AFR"?
3. For `NO_MATCH`, should agents be nudged to fall back to `find` / `get_file_context`, or is silence (solve normally) the intended bartender rule?
4. Do package-level cards (`package:…`) correctly express Go's import granularity, or should hubs always stay file-addressed with package only as a link?
5. What is the preferred expand protocol when a card is "close but incomplete" — deeper capsule, prior revision, or neighboring card IDs?
6. License / credit line: is linking bartender + everoo in README + this note the right attribution for landing in codemap?
7. Lexical reliability currently requires **two** shared identifier tokens for multi-word questions (to avoid lone verbs like `save` matching unrelated tests). That makes synonym-only seconds (`theme persistence`) an intentional `NO_MATCH` in this slice — should AFR soften that with stemming, embeddings, or an expand step?

## Non-goals for this PR

- Changing default hook / session-start payloads
- Training classifiers or calling external LLMs inside codemap
- Public commons contribution / consent flows
- Replacing handoff or context envelopes
