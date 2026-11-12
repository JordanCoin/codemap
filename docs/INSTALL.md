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

- creates `.codemap/config.json` with auto-detected language filters
- merges Claude hooks into `.claude/settings.local.json` and configures MCP in `.mcp.json` (project scope)
- merges Codex hooks into `~/.codex/hooks.json` and configures MCP in `~/.codex/config.toml` (user scope, since Codex skips a project's `.codex/` until the project is trusted; `--project` writes `.codex/` instead)
- writes the builtin skills to `.claude/skills/` for Claude, keeping any skill file you edited
- hooks start and read daemon state at session start

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
