# Install and setup

This doc answers: how do I install codemap outside Homebrew or Scoop, what does `codemap setup` change, and how do I check it worked?

Homebrew and Scoop one-liners are in the [README](../README.md#install). Other options: [Releases](https://github.com/JordanCoin/codemap/releases) | `go install` | build from source.

## CI / tarball install

Release tarballs ship `codemap` and the bundled rules but not the `ast-grep` executable, which `--deps` needs. Either install it separately:

```bash
apk add --no-cache curl jq bash python3 py3-pip

ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then ARCH="amd64"; elif [ "$ARCH" = "aarch64" ]; then ARCH="arm64"; fi

CODEMAP_VERSION=$(curl -fsSL https://api.github.com/repos/JordanCoin/codemap/releases/latest | jq -r '.tag_name' | tr -d 'v')
curl -fsSL "https://github.com/JordanCoin/codemap/releases/download/v${CODEMAP_VERSION}/codemap_${CODEMAP_VERSION}_linux_${ARCH}.tar.gz" \
  | tar xz -C /usr/local/bin/ codemap

python3 -m pip install --no-cache-dir ast-grep-cli
```

…or use the self-contained `codemap-full` artifact, which bundles `codemap`, `ast-grep`, and `sg`:

```bash
curl -fsSL "https://github.com/JordanCoin/codemap/releases/download/v${CODEMAP_VERSION}/codemap-full_${CODEMAP_VERSION}_linux_${ARCH}.tar.gz" \
  | tar xz -C /usr/local/bin/ codemap ast-grep sg
```

## Setup

Run setup anywhere inside your git repo. Repo-scoped commands such as
`setup`, `doctor`, `config`, `watch`, `skill`, `context`, `serve`, and
managed hooks resolve the nearest git root automatically, including linked
worktrees with a `.git` file.

```bash
cd /path/to/your/project
codemap setup
```

`codemap setup` configures the agents it detects: Claude Code when `~/.claude`, `~/.claude.json`, or `claude` on `PATH` exists, Codex when `~/.codex` or `codex` on `PATH` exists, and both when it finds neither. It:

- creates `.codemap/` with `config.json` (auto-detected language filters) and `.codemap/.gitignore`
- names machine-state files an older codemap left under `.codemap/`: committed ones with the `git rm --cached -- <files>` command that untracks them (it never runs it), and untracked ones as no longer used and safe to delete
- merges Claude hooks into `.claude/settings.local.json` and configures MCP in `.mcp.json` (project scope)
- merges Codex hooks into `~/.codex/hooks.json` and configures MCP in `~/.codex/config.toml` (user scope, since Codex skips a project's `.codex/` until the project is trusted; `--project` writes `.codex/` instead)
- writes the builtin skills to `.claude/skills/` for Claude, keeping any skill file you edited
- hooks start and read daemon state at session start

`.codemap/` holds only what you author: `config.json` and `skills/`, both safe to commit. Its `.gitignore` ignores anything else, which is only ever leftovers from older versions; codemap writes that file only when it is missing, so your edits to it stay.

Machine state never goes into the repo, set up or not, primary checkout or linked worktree. Hooks, the watch daemon and every command keep it under `~/.codemap/state/<project key>/`, next to the daemon registry in `~/.codemap/daemons/`; `XDG_CACHE_HOME` is not consulted. Every directory is private (0700), every file 0600, and codemap refuses a symlinked state directory or state file. `codemap doctor` prints the exact directory on its `state dir:` line. Only `codemap setup`, `codemap config init` and `codemap skill init` create `.codemap/`; nothing else writes there, and a linked worktree never gets one. A repo without `config.json` gets auto-detected filters stored in the state dir (`auto-config.json`); every full scan (`codemap .`) re-derives them, and the tree says `auto filter: ...` when they apply. Run `codemap config init` to commit filters instead. When there is no home directory, codemap runs without saved state and never creates one.

Rerunning setup is safe: it changes only what is missing or stale. `--no-config`, `--no-hooks`, `--no-mcp`, and `--no-skills` skip a piece.

Managed entries record the verified absolute path of the running `codemap`, so agents don't depend on your shell `PATH`. Rerun setup if that path changes.

```bash
codemap setup --agent claude   # one agent only
codemap setup --agent codex
codemap setup --global         # Claude at user scope too (~/.claude), applies to every project
codemap setup --project        # Codex at project scope (.codex/) instead of ~/.codex
```

After a `brew upgrade`, the session-start hook prints one line naming any integration still stamped with the old version and the command that refreshes it.

## Verify

```bash
codemap doctor            # validate this project's integrations
codemap doctor --global   # validate user-scope configuration
```

Doctor checks project scope and falls back to user scope, reporting which one satisfied each check. For Codex, trust the hooks from `/hooks` in CLI or Settings → Hooks in Desktop, then start a new session.

Next: [what the hooks show your agent](HOOKS.md)
