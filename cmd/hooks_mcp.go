package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codemap/internal/buildinfo"
	codemapmcp "codemap/mcp"
	pluginbundle "codemap/plugins"

	"github.com/pelletier/go-toml/v2"
)

// hostMCPEntry is one codemap MCP registration the hook's host agent loads.
type hostMCPEntry struct {
	Path              string
	Global            bool
	Integration       string
	ConfiguredVersion string
}

// hostCodemapMCPEntries reads only the host's own config files, never a
// subprocess, so it is cheap enough for every hook.
func hostCodemapMCPEntries(root, agent string) []hostMCPEntry {
	home, _ := os.UserHomeDir()
	var entries []hostMCPEntry
	add := func(path string, global bool, raw any) {
		server, ok := raw.(map[string]any)
		if !ok {
			return
		}
		entry := hostMCPEntry{Path: path, Global: global}
		if _, args, err := ParseGlobalRootOptions(mcpServerArgs(server)); err == nil && len(args) == 5 && args[0] == "mcp" && args[1] == "--configured-version" && args[3] == "--integration" {
			entry.ConfiguredVersion, entry.Integration = args[2], args[4]
		}
		entries = append(entries, entry)
	}
	readJSON := func(path string) map[string]any {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var payload map[string]any
		if json.Unmarshal(data, &payload) != nil {
			return nil
		}
		return payload
	}
	jsonServer := func(payload map[string]any) any {
		servers, _ := payload["mcpServers"].(map[string]any)
		return servers["codemap"]
	}

	if agent == "codex" {
		for _, scope := range []struct {
			path   string
			global bool
		}{{filepath.Join(root, ".codex", "config.toml"), false}, {filepath.Join(home, ".codex", "config.toml"), true}} {
			data, err := os.ReadFile(scope.path)
			if err != nil {
				continue
			}
			payload := map[string]any{}
			if toml.Unmarshal(data, &payload) != nil {
				continue
			}
			servers, _ := payload["mcp_servers"].(map[string]any)
			add(scope.path, scope.global, servers["codemap"])
		}
		pluginMCP := filepath.Join(home, ".codex", "plugins", pluginbundle.CodemapPluginName, ".mcp.json")
		add(pluginMCP, true, jsonServer(readJSON(pluginMCP)))
		return entries
	}

	projectMCP := filepath.Join(root, ".mcp.json")
	add(projectMCP, false, jsonServer(readJSON(projectMCP)))
	userMCP := filepath.Join(home, ".claude.json")
	if payload := readJSON(userMCP); payload != nil {
		add(userMCP, true, jsonServer(payload))
		projects, _ := payload["projects"].(map[string]any)
		project, _ := projects[root].(map[string]any)
		add(userMCP, false, jsonServer(project))
	}
	return entries
}

// codemapMCPTool maps a CLI nudge to the MCP tool that answers the same question.
func codemapMCPTool(command string) string {
	switch {
	case strings.HasPrefix(command, "codemap --importers "):
		return "get_importers"
	case command == "codemap --deps":
		return "get_dependencies"
	case strings.HasPrefix(command, "codemap --diff"):
		return "get_diff"
	case command == "codemap .":
		return "get_structure"
	case strings.HasPrefix(command, "codemap skill show"):
		return "get_skill"
	}
	return ""
}

// mcpToolHint returns " (MCP: <tool>)" for a CLI nudge when the host has
// codemap MCP registered for root, and "" otherwise.
func mcpToolHint(root string) func(command string) string {
	registered := len(hostCodemapMCPEntries(root, detectAgentID())) > 0
	return func(command string) string {
		if tool := codemapMCPTool(command); registered && tool != "" {
			return " (MCP: " + tool + ")"
		}
		return ""
	}
}

// integrationDriftLine reports the first managed entry stamped with another
// codemap version, or "" when every entry matches the running binary.
func integrationDriftLine(entries []hostMCPEntry) string {
	for _, entry := range entries {
		repair := codemapmcp.RepairCommand(entry.Integration)
		if entry.ConfiguredVersion == "" || repair == "" || buildinfo.Equal(entry.ConfiguredVersion, buildinfo.Current()) {
			continue
		}
		if entry.Global && strings.HasPrefix(repair, "codemap setup ") {
			repair += " --global"
		}
		return fmt.Sprintf("⚠️  Codemap integration is stale: %s was configured for %s, running %s. Run `%s` to refresh it.", entry.Path, entry.ConfiguredVersion, buildinfo.Current(), repair)
	}
	return ""
}
