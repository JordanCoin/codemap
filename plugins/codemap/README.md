# Codemap Plugin

This is a Codex plugin bundle for Codemap.

It bundles:

- the Codemap skill under `./skills/`
- a local MCP configuration in [`.mcp.json`](./.mcp.json)
- a launcher script that prefers `codemap mcp` and falls back to `codemap-mcp`
- packaged logo/icon assets under `./assets/`

Install/runtime expectations:

- `codemap` should be installed on `PATH`
- preferred runtime is a Codemap build that supports `codemap mcp`
- fallback runtime is a separate `codemap-mcp` binary on `PATH`

Install globally with:

```bash
codemap plugin install
```

That writes the plugin bundle to `~/plugins/codemap` and updates `~/.agents/plugins/marketplace.json`.

For repo-local discovery while developing, pair this plugin with `.agents/plugins/marketplace.json` in the repo root.
