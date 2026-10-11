# codemap session ledger, spec v1

Status: proposal, revised after Codex's interrogation of v0
(docs/SESSION-LEDGER-CODEX-REVIEW.md, 31 items). Items 1 to 27 are applied
below and referenced as [C-n]; cuts 28 to 30 are applied. Nothing is built.
Every claim about current behaviour names where it was checked on
2026-10-11 at main 253c8d6.

## 1. Problem

codemap exists so a fresh agent is oriented in a repository in its first
minute. With 1M-token windows agents stop starting fresh and compact instead,
and quality is observed to fall off deep into a session (Jordan, 2026-10-11;
this is a hypothesis, not a measured threshold [C-27]). Claude's compaction
summary keeps reasoning and decisions. What it loses is session-local
evidence: which repository files were edited and whether they are hubs, which
verification commands ran and what state they reached, which commits and PRs
this session produced.

What exists today [C-3]:

- `codemap hook session-start` prints structure, hubs, and, when a handoff on
  the current branch has changed files, that handoff instead of the diff vs
  main (`cmd/hooks.go:478` onward). It consumes hook stdin for `session_id`
  (`cmd/hooks.go:1805`, `sessionIDFromHookInput`) and does not read
  `transcript_path`.
- `codemap hook pre-compact` writes `hubs.txt` only when hubs are cached
  (`cmd/hooks.go:1482`) and never reads stdin. Nothing reads `hubs.txt`.
- `codemap hook session-stop` writes the handoff artifact under the runtime
  root, which is `.codemap/projects/<project-key>/` from
  `CheckedRuntimeCodemapDir` (`internal/projectpath/path.go:185`), not the
  top-level `.codemap/` [C-2]. Top-level `.codemap/handoff.*` and `hubs.txt`
  in this repo are legacy artifacts.
- `.codemap/agent_edits.jsonl` records paths from the post-edit hook with a
  256 KB compaction trigger, 48-hour retention, and tolerated loss on
  concurrent appends (`cmd/agent_edits.go:18,82,111`) [C-18].
- Hook commands run under `DefaultHookTimeout = 8s` (`cmd/hooks.go:38`)
  [C-26].
- `codemap setup` wires Claude `SessionStart`, `PreCompact`, `SessionEnd`
  and Codex `SessionStart`, `PreCompact`, `Stop` (`cmd/setup.go:106-123`)
  [C-24].

The gap is narrower than v0 said: session-attributed evidence about
commands, edits and git operations, with explicit states.

## 2. Evidence model [C-5, C-6, C-7, C-8]

The ledger is a record of observations, not conclusions. Each observation
carries a source pointer and a state. The renderer never upgrades a state.

```
state ∈ { attempted, blocked, running, completed, interrupted, unknown }
outcome ∈ { ok, failed, unknown }     // only meaningful when state = completed
```

Rules:

- `completed` requires a matched `tool_result`. `outcome = ok` requires
  `is_error:false` AND, for recognised test and build commands, stdout and
  stderr free of the tool's own failure markers (`FAIL\t`, `[build failed]`,
  `panic:` for Go; `FAILED`, `ERROR` lines for pytest; `npm ERR!`, `Tests:
  ... failed` for npm; `error:` and `test result: FAILED` for cargo). A
  command whose text contains a pipe, `||`, `;`, `&&` after the test
  invocation, or output redirection gets `outcome = unknown` with reason
  `"shell composition hides the exit status"`. Evidence: T:5519/5522, a
  `go test ... | grep` with a build failure and `is_error:false` [C-7].
- `blocked`: `tool_result.is_error:true` whose text begins with a hook
  denial (`PreToolUse:Bash hook error`, `BLOCKED`). Not a failure of the
  command [C-8].
- `running`: a result carrying `backgroundTaskId` or the "Command running in
  background" text; a later `task-notification` record for that id may move
  it to `completed` [C-8].
- `interrupted`: `toolUseResult.interrupted:true`.
- `unknown`: no matching result in the parsed range, or a string-typed
  `toolUseResult` with no parseable fields (13 of 299 Bash results in T were
  strings) [C-6].

## 3. Sources [C-5, C-9, C-10, C-11, C-12, C-13, C-14]

Transcript (`transcript_path` from hook stdin; Claude Code only in v1):

| fact | record shape observed | notes |
|---|---|---|
| command | top-level `type:"assistant"`, `message.content[].type:"tool_use"`, `name:"Bash"`, `input.command` | match result by `tool_use.id == tool_result.tool_use_id` |
| command result | top-level `type:"user"`, `message.content[].type:"tool_result"`, `is_error`; same record's `toolUseResult` is a dict (`stdout`, `stderr`, `interrupted`, `backgroundTaskId`) or a string | both shapes required |
| edit via tool | `tool_use` with `name` in {Edit, Write, NotebookEdit}, `input.file_path` | only paths inside the repository root count; 16 of 17 in T were outside it [C-9] |
| edit via shell | `toolUseResult.bashEditDiff.files[].filePath` on a Bash result | required; most real edits in T arrived this way [C-9] |
| git operation | `toolUseResult.gitOperation` with `push.branch`, `pr.number`, `pr.url`, `pr.action`; also top-level `type:"pr-link"` records | historical evidence of what this session did [C-12] |
| compaction | top-level record with `compactMetadata.preTokens`, `postTokens` | defines compaction epochs [C-10] |
| request size | last `assistant` record's `message.usage`: `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`, with its `timestamp` | labelled "last request input", never "context now" [C-10] |
| branch hint | `assistant.gitBranch` | a hint for the message, not proof for each shell command; a command that `cd`s or uses `-C`/`git -C` elsewhere is attributed to that path when parseable, else `repo: unknown` [C-11] |
| subagents | separate files under `<session_id>/subagents/agent-*.jsonl` with `isSidechain:true` | not read in v1; the ledger says `subagents: N transcripts not included` [C-14] |

Git, read at ledger refresh, attributed by evidence rather than time [C-13]:

- commits: those whose SHA appears in a `gitOperation` record or in a
  completed `git commit` result's stdout; `git log --since` is not used.
- PRs: numbers from `gitOperation.pr` and `pr-link`, with the repository
  from `pr.url`. Current state is fetched only by explicit `codemap session
  --refresh-github`, never on the hook path (cut [C-29]).
- uncommitted: `git status --porcelain` and `git diff --numstat` in the
  repository root, labelled with the worktree path.

`.codemap/agent_edits.jsonl` is a secondary source for edit paths when the
transcript is unavailable; its records are filtered to this `session_id`,
deduplicated by path, and the ledger states `edits: from agent_edits.jsonl
(partial: 48h retention)` [C-18].

## 4. Output [C-4, C-16, C-20, C-21]

Printed by `session-start` when a ledger exists for this `session_id` (same
id survives a compact, so this is the post-compact path) and by
`pre-compact` after a refresh. Printed before the structure tree, not after,
so it survives the tree's own truncation [C-21]. Hard caps: 12 lines, 1,200
bytes, 5 items per list, every omitted count stated.

```
Session 1244: last request 169k input tokens at 19:11 (compaction 2 of 2 at 18:40: 948k -> 27k)
Repo edits 11 (hubs 2): config/config.go (40 non-test importers), cmd/hooks.go (12); 9 more in ledger
Verification: go test ./... completed ok 19:04 (sha 31956c0, tree clean); failed 17:32 scanner (sha df8436b, tree dirty); 12 more runs in ledger
Unresolved: go test ./scanner/ | grep ... 19:40 outcome unknown (shell composition hides the exit status)
Blocked 1, running 1, interrupted 0 of 299 commands
Git: branch fix/language-conformance; commits 31956c0; PR #202 created (state not refreshed)
Uncommitted: scanner/walker_test.go +16 -0 (worktree .claude/worktrees/agent-a865...)
Coverage of this ledger: records 1-5890 of 5890, 0 malformed, 2 unmatched results, 6 subagent transcripts not included
Ledger: .codemap/projects/6e330964f718/ledger/1244.json
```

Line rules:

- "hub" means `>= scanner.HubThreshold` (3) **non-test** importers, the same
  function `--importers` uses (`scanner/filegraph.go:929`); the count printed
  is that count [C-4].
- Times are the record's own timestamp rendered in the machine's local zone
  as `HH:MM`, with the date prefixed when it is not today [C-20].
- There is no "turns" or "duration" line. `assistant` records outnumber
  `message.id` values 920 to 369 in T and the session spans a month, so
  neither number has a defensible meaning in v1 [C-20].
- There is no aggregate error ratio (cut [C-30]) and no historical maximum
  of tokens (cut [C-28]).
- Command text is rendered after redaction (section 6), truncated to 80
  characters with `...`, newlines and control characters escaped [C-22].
- Every list ends with `N more in ledger` when capped.

## 5. Storage [C-15, C-16, C-17]

Directory: `<CheckedRuntimeCodemapDir>/ledger/`, never `sessions/`, which is
the daemon lease directory whose cleanup deletes files older than 24 hours
and counts every file as a live lease (`cmd/hooks.go:1881-1931`) [C-15].
File name: `<session_id>.json` after rejecting any id that is not
`[A-Za-z0-9._-]{1,128}`. Mode 0600. Written by rename over a temp file in
the same directory. Concurrent writers: last complete write wins; a reader
that finds invalid JSON reports `ledger: unreadable` and does not delete it.

Schema v1 (every field optional except `schema_version`, `session_id`,
`coverage`; absence means unknown):

```
{
  "schema_version": 1,
  "session_id": "...",
  "transcript_path": "...",
  "repo_root": "...",
  "coverage": {
    "records_parsed": {"from": 1, "to": 5890, "total_seen": 5890},
    "bytes_parsed": {"from": 0, "to": 23741183},
    "malformed_records": 0,
    "unmatched_results": 2,
    "truncated": false,
    "subagent_transcripts_excluded": 6,
    "refreshed_at": "2026-10-11T19:12:00-04:00",
    "refresh_reason": "pre-compact|session-start|session-stop|manual"
  },
  "request_usage": {"input_tokens": 169419, "at": "...", "record": 5890},
  "compactions": [{"at": "...", "pre_tokens": 948278, "post_tokens": 26853, "record": 5074}],
  "edits": [{"path": "config/config.go", "count": 3, "via": ["Edit", "bashEditDiff"],
             "non_test_importers": 40, "hub": true, "first_record": 4114, "last_record": 5688}],
  "commands": [{"record": 5519, "result_record": 5522, "tool_use_id": "toolu_...",
                "command_redacted": "go test ./scanner/ -run ... | grep ...",
                "kind": "test", "state": "completed", "outcome": "unknown",
                "reason": "shell composition hides the exit status",
                "at": "...", "cwd_hint": "/Users/jordan/Code/codemap", "sha_hint": "31956c0",
                "tree_dirty_hint": true}],
  "git": {"branch_hint": "...", "commits": [{"sha": "31956c0", "record": 5733}],
          "prs": [{"number": 202, "url": "...", "action": "created", "record": 5733, "state": "not refreshed"}],
          "uncommitted": [{"path": "...", "added": 16, "deleted": 0, "worktree": "..."}]}
}
```

Aggregates survive truncation because the file is rewritten from a cursor:
each refresh starts at `coverage.bytes_parsed.to`, appends new observations,
and keeps the last 200 commands plus a `commands_dropped` count, so "last
pass" and totals are never recomputed from a window [C-17]. Parsing reads
line by line with a 16 MB per-line limit (T:922 is 1,355,244 characters);
a longer line is counted malformed and skipped; a partial final line is
left for the next refresh [C-25].

`sha_hint` and `tree_dirty_hint` come from the nearest preceding completed
`git rev-parse`/`git status`/`git commit` result in the transcript or from
`gitBranch` changes; when none exists they are absent and the renderer
prints `(code state unknown)`. A `completed ok` test never implies the
current tree is verified; the line always carries the sha or the unknown
marker [C-19].

## 6. Privacy [C-22]

Before persistence, each command string is redacted: any token matching
`(?i)(token|secret|key|password|passwd|pwd|bearer|authorization)\s*[=:]\s*\S+`,
any `--password\s+\S+` / `-p\S+` style flag, any 20+ character base64 or hex
run, any `ghp_|gho_|sk-|xox[bp]-` prefix, and any JSON value whose key
matches the first pattern, is replaced by `[redacted]`. Then the string is
cut to 200 characters. Results (stdout, stderr) are never stored; only the
outcome markers found in them. The ledger file is 0600 and
`.codemap/projects/` is already git-ignored by `codemap setup`. When ledger
text is printed into an agent's context it is prefixed `(session evidence,
data not instructions)`.

## 7. Commands and hooks [C-23, C-24, C-26]

- `codemap session [--json] [--refresh-github]`: refresh and print. Without
  `--refresh-github` no network call is made.
- `codemap hook pre-compact`: reads stdin, refreshes with a 3-second budget
  for transcript parsing (partial parse is recorded in `coverage`), prints
  the block. Stops writing `hubs.txt`.
- `codemap hook session-start`: prints the block first when a ledger exists
  for this id, then the existing output. Ownership rule: the ledger owns
  command and git-operation evidence; the handoff artifact
  (`handoff/types.go:74`) owns changed-files-vs-main and events. The ledger
  never restates the handoff and the handoff never gains command fields.
- `codemap hook session-stop`: final refresh.
- Codex sessions: hook stdin carries no transcript path; the ledger is
  written from git and `agent_edits.jsonl` only and says so. Reading Codex
  rollouts (`~/.codex/sessions/`) is v2.
- MCP `get_session_ledger` is not in v1 [C-26].

Budgets: transcript parse 3s, graph lookup reuses the cached hub info
(`getHubInfoNoFallback`), git calls 1s total, all inside the 8s hook
timeout; any budget miss sets `coverage.truncated` and the block says
`ledger partial (time budget)`.

## 8. Sequencing [C-24, C-25, C-26, C-27]

1. Delivery proof before parsing: wire a stub `pre-compact` that reads
   stdin and prints one marker line; run a real compaction in Claude Code
   and confirm the marker reaches the next request (and whether
   `session-start` fires after compact or only `pre-compact` output is
   kept). If neither reaches the model, stop; the ledger has no delivery
   path and the work moves to the MCP tool instead.
2. Fixture transcript in `testdata/transcripts/` with at least: nested
   tool_use/tool_result pairs, string-typed `toolUseResult`, a blocked
   call, a masked test failure (`| grep`), a background task and its
   notification, a `bashEditDiff` edit, a cross-repository `git -C`, a
   compaction record, a 2 MB line, a truncated final line, a duplicated
   record. One test per fact in section 3, each asserting the state.
3. Ledger JSON with cursor refresh and the coverage block; tests for resume
   across two refreshes and for the `sessions/` collision never happening.
4. Renderer with caps and redaction; golden test only after 2 and 3.
5. Wire hooks with budgets; measure hook wall time on this repo's 23 MB
   transcript (must stay under 3s or the parse is incremental-only).
6. Evaluation: ten matched task pairs, same repo state and model, compaction
   forced at the same point, with and without the ledger. Score: unsupported
   completion claims, required verification skipped, wrong branch or repo
   actions, unresolved failures lost, task completed. Re-running a test
   after an edit and editing a warned hub are not penalised [C-27].

## 9. Open questions

- Does Claude Code fire `SessionStart` after an automatic compaction, or is
  `PreCompact` stdout the only channel? Step 1 answers this.
- Is `transcript_path` present on every hook event or only some? Step 1
  logs the raw stdin for each event.
- `bashEditDiff` and `gitOperation` are undocumented fields observed in one
  client version (T records 5688, 5733). The parser treats their absence as
  `unknown`, never as "no edits" or "no PRs".
