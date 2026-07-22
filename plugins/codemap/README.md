# Codemap Plugin

This is a Codex plugin bundle for Codemap.

It bundles:

- the Codemap skill under `./skills/`
- an MCP configuration generated at install time
- packaged logo/icon assets under `./assets/`

Install/runtime expectations:

- the installer records the absolute path and version of the running `codemap`
- rerun installation after upgrades if the recorded path or version changes

Install globally with:

```bash
codemap plugin install
```

Run the same command after upgrading the Codemap binary. It refreshes the
global plugin and migrates managed Codex files in the current project only;
run `codemap setup` and `codemap doctor` in each configured project afterward.
Codex does not automatically update this generated local plugin. After
installation, start a new task in Desktop or a new session in CLI.

That writes the plugin to `~/.codex/plugins/codemap`, updates the personal
marketplace, and installs or refreshes it through Codex CLI. The installed
plugin is available to CLI and Desktop when they share that Codex environment.
Repeat installation for another host or `CODEX_HOME`; this does not upgrade the
Codex applications or other projects. Use `--no-activate` only when preparing
files without invoking Codex CLI. Legacy `--activate` is deprecated because
activation is now the default.

For repo-local discovery while developing, pair this plugin with `.agents/plugins/marketplace.json` in the repo root.
