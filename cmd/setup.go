package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"codemap/config"
	"codemap/internal/buildinfo"
	"github.com/pelletier/go-toml/v2"
)

type claudeHookSpec struct {
	Event   string
	Matcher string
	Command string
}

type claudeHookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type claudeHookEntry struct {
	Matcher string              `json:"matcher,omitempty"`
	Hooks   []claudeHookCommand `json:"hooks"`
}

type ensureHooksResult struct {
	SettingsPath   string
	CreatedFile    bool
	WroteFile      bool
	AddedHooks     int
	ExistingHooks  int
	TotalCodemap   int
	TargetIsGlobal bool
}

type setupAgent string

const (
	setupAgentBoth   setupAgent = "both"
	setupAgentClaude setupAgent = "claude"
	setupAgentCodex  setupAgent = "codex"
)

var recommendedClaudeHooks = generatedClaudeHooks(defaultIntegrationExecutable())

var recommendedCodexHooks = generatedCodexHooks(defaultIntegrationExecutable())

func defaultIntegrationExecutable() string {
	path, _ := resolveIntegrationExecutable()
	return path
}

func generatedClaudeHooks(executable string) []claudeHookSpec {
	command := quoteHookExecutable(executable, runtime.GOOS)
	return []claudeHookSpec{
		{Event: "SessionStart", Command: command + " hook session-start --integration=claude-setup"},
		{Event: "PreToolUse", Matcher: "Edit|Write", Command: command + " hook pre-edit --integration=claude-setup"},
		{Event: "PostToolUse", Matcher: "Edit|Write", Command: command + " hook post-edit --integration=claude-setup"},
		{Event: "UserPromptSubmit", Command: command + " hook prompt-submit --integration=claude-setup"},
		{Event: "PreCompact", Command: command + " hook pre-compact --integration=claude-setup"},
		{Event: "SessionEnd", Command: command + " hook session-stop --integration=claude-setup"},
	}
}

func generatedCodexHooks(executable string) []claudeHookSpec {
	command := quoteHookExecutable(executable, runtime.GOOS)
	return []claudeHookSpec{
		{Event: "SessionStart", Command: command + " hook session-start --agent=codex --integration=codex-setup"},
		{Event: "PreToolUse", Matcher: "apply_patch|Edit|Write", Command: command + " hook pre-edit --agent=codex --integration=codex-setup"},
		{Event: "PostToolUse", Matcher: "apply_patch|Edit|Write", Command: command + " hook post-edit --agent=codex --integration=codex-setup"},
		{Event: "UserPromptSubmit", Command: command + " hook prompt-submit --agent=codex --integration=codex-setup"},
		{Event: "PreCompact", Command: command + " hook pre-compact --agent=codex --integration=codex-setup"},
		{Event: "Stop", Command: command + " hook session-stop --agent=codex --integration=codex-setup"},
	}
}

// RunSetup configures codemap for the recommended hooks-first workflow.
//
// By default it creates:
//   - <project>/.codemap/config.json (if missing)
//   - <project>/.claude/settings.local.json codemap hook entries
//
// Use --global to target ~/.claude/settings.json for hooks.
func RunSetup(args []string, defaultRoot string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	useGlobalHooks := fs.Bool("global", false, "Install hooks into global agent settings instead of project-local settings")
	agent := fs.String("agent", "", "Install hooks for only claude or codex (default: both)")
	skipConfig := fs.Bool("no-config", false, "Skip creating .codemap/config.json")
	skipHooks := fs.Bool("no-hooks", false, "Skip writing agent hook settings")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println("Usage: codemap setup [--global] [--agent claude|codex] [--no-config] [--no-hooks] [path]")
			return 0
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Usage: codemap setup [--global] [--agent claude|codex] [--no-config] [--no-hooks] [path]")
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "Usage: codemap setup [--global] [--agent claude|codex] [--no-config] [--no-hooks] [path]")
		return 2
	}
	selectedAgent := setupAgentBoth
	if value := strings.ToLower(strings.TrimSpace(*agent)); value != "" {
		selectedAgent = setupAgent(value)
	}
	if selectedAgent != setupAgentClaude && selectedAgent != setupAgentCodex && strings.TrimSpace(*agent) != "" {
		fmt.Fprintln(os.Stderr, "Error: --agent must be claude or codex")
		return 2
	}

	root := defaultRoot
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		return 1
	}
	executable, err := resolveIntegrationExecutable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving codemap executable: %v\n", err)
		return 1
	}

	if !*skipConfig {
		if _, err := os.Stat(filepath.Join(absRoot, ".git")); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: %s is not a git repository root; continuing setup anyway.\n", absRoot)
		}
	}

	fmt.Printf("Project: %s\n", absRoot)
	fmt.Println()

	if *skipConfig {
		fmt.Println("Config: skipped (--no-config)")
	} else {
		cfgResult, err := initProjectConfig(absRoot)
		switch {
		case errors.Is(err, errConfigExists):
			fmt.Printf("Config: already exists (%s)\n", config.ConfigPath(absRoot))
		case err != nil:
			fmt.Fprintf(os.Stderr, "Config: failed (%v)\n", err)
			return 1
		default:
			if len(cfgResult.TopExts) == 0 {
				fmt.Printf("Config: created %s (no code extensions detected)\n", cfgResult.Path)
			} else {
				fmt.Printf("Config: created %s (only=%s)\n", cfgResult.Path, strings.Join(cfgResult.TopExts, ","))
			}
		}
	}

	if *skipHooks {
		fmt.Println("Hooks: skipped (--no-hooks)")
	}
	failed := false
	if selectedAgent == setupAgentBoth || selectedAgent == setupAgentClaude {
		if !*skipHooks && configureHooks("Claude", claudeSettingsPath, func(path string, global bool) (ensureHooksResult, error) {
			return ensureClaudeHooksWithExecutable(path, global, executable)
		}, absRoot, *useGlobalHooks) != nil {
			failed = true
		}
		if configureMCP("Claude", claudeMCPPath, func(path string) (bool, error) {
			return ensureClaudeMCPWithExecutable(path, executable)
		}, absRoot, *useGlobalHooks) != nil {
			failed = true
		}
	}
	if selectedAgent == setupAgentBoth || selectedAgent == setupAgentCodex {
		if !*skipHooks && configureHooks("Codex", codexHooksPath, func(path string, global bool) (ensureHooksResult, error) {
			return ensureCodexHooksWithExecutable(path, global, executable)
		}, absRoot, *useGlobalHooks) != nil {
			failed = true
		}
		if configureMCP("Codex", codexConfigPath, func(path string) (bool, error) {
			return ensureCodexMCPWithExecutable(path, executable)
		}, absRoot, *useGlobalHooks) != nil {
			failed = true
		}
	}
	if failed {
		fmt.Fprintln(os.Stderr, "Setup incomplete: one or more agent integrations failed.")
		return 1
	}

	fmt.Println()
	fmt.Println("Next:")
	codexHooksRelevant := !*skipHooks && (selectedAgent == setupAgentBoth || selectedAgent == setupAgentCodex)
	if codexHooksRelevant && validateCodexHookTrust(absRoot) != nil {
		printCodexHookReviewInstructions(absRoot)
		fmt.Println("  4. Verify hook output appears at session start.")
		fmt.Println("  5. Tune .codemap/config.json if you want narrower context.")
	} else if codexHooksRelevant {
		fmt.Println("  1. Start a new Codex task/session.")
		fmt.Println("  2. Verify hook output appears at session start.")
		fmt.Println("  3. Tune .codemap/config.json if you want narrower context.")
	} else {
		fmt.Println("  1. Restart the configured coding agent (or open a new session).")
		fmt.Println("  2. Verify hook output appears at session start.")
		fmt.Println("  3. Tune .codemap/config.json if you want narrower context.")
	}
	return 0
}

func printCodexHookReviewInstructions(root string) {
	fmt.Printf("  1. Open the project in Codex CLI (`codex -C %s`) or Desktop.\n", quoteHookExecutable(root, runtime.GOOS))
	fmt.Println("  2. Trust Codemap hooks from `/hooks` in CLI or Settings > Hooks in Desktop.")
	fmt.Println("  3. Start a new Codex task/session.")
}

func configureHooks(label string, pathFor func(string, bool) (string, error), ensure func(string, bool) (ensureHooksResult, error), root string, global bool) error {
	path, err := pathFor(root, global)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s hooks: failed to resolve settings path (%v)\n", label, err)
		return err
	}
	result, err := ensure(path, global)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s hooks: failed (%v)\n", label, err)
		return err
	}
	if result.AddedHooks == 0 {
		fmt.Printf("%s hooks: already configured (%s)\n", label, result.SettingsPath)
	} else if result.CreatedFile {
		fmt.Printf("%s hooks: created %s (+%d codemap hooks)\n", label, result.SettingsPath, result.AddedHooks)
	} else {
		fmt.Printf("%s hooks: updated %s (+%d codemap hooks)\n", label, result.SettingsPath, result.AddedHooks)
	}
	return nil
}

func configureMCP(label string, pathFor func(string, bool) (string, error), ensure func(string) (bool, error), root string, global bool) error {
	path, err := pathFor(root, global)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s MCP: failed to resolve configuration path (%v)\n", label, err)
		return err
	}
	added, err := ensure(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s MCP: failed (%v)\n", label, err)
		return err
	}
	if added {
		fmt.Printf("%s MCP: configured (%s)\n", label, path)
	} else {
		fmt.Printf("%s MCP: already configured (%s)\n", label, path)
	}
	return nil
}

func claudeSettingsPath(projectRoot string, global bool) (string, error) {
	if !global {
		return filepath.Join(projectRoot, ".claude", "settings.local.json"), nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".claude", "settings.json"), nil
}

func claudeMCPPath(projectRoot string, global bool) (string, error) {
	if !global {
		return filepath.Join(projectRoot, ".mcp.json"), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".claude.json"), nil
}

func codexHooksPath(projectRoot string, global bool) (string, error) {
	if !global {
		return filepath.Join(projectRoot, ".codex", "hooks.json"), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".codex", "hooks.json"), nil
}

func codexConfigPath(projectRoot string, global bool) (string, error) {
	if !global {
		return filepath.Join(projectRoot, ".codex", "config.toml"), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".codex", "config.toml"), nil
}

func ensureClaudeMCPWithExecutable(path, executable string) (bool, error) {
	payload := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &payload); err != nil {
				return false, fmt.Errorf("parse %s: %w", path, err)
			}
			if payload == nil {
				return false, fmt.Errorf("parse %s: root must be an object", path)
			}
		}
	case os.IsNotExist(err):
	default:
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var servers map[string]any
	if raw, exists := payload["mcpServers"]; exists {
		var ok bool
		servers, ok = raw.(map[string]any)
		if !ok {
			return false, fmt.Errorf("%s field 'mcpServers' must be an object", path)
		}
	} else {
		servers = map[string]any{}
		payload["mcpServers"] = servers
	}
	if server, exists := servers["codemap"]; exists {
		if !isOwnedCodemapMCPServer(server, "claude-setup") {
			return false, fmt.Errorf("%s already defines a conflicting codemap MCP server", path)
		}
		serverMap := server.(map[string]any)
		args := managedMCPArgs(buildinfo.Current(), "claude-setup")
		if serverMap["command"] == executable && stringSlicesEqual(mcpServerArgs(serverMap), args) {
			return false, nil
		}
		serverMap["command"] = executable
		serverMap["args"] = args
	} else {
		servers["codemap"] = map[string]any{"command": executable, "args": managedMCPArgs(buildinfo.Current(), "claude-setup")}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(out, '\n'), 0o644)
}

func ensureCodexMCPWithExecutable(path, executable string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	payload := map[string]any{}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := toml.Unmarshal(data, &payload); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if rawServers, exists := payload["mcp_servers"]; exists {
		servers, ok := rawServers.(map[string]any)
		if !ok {
			return false, fmt.Errorf("%s field 'mcp_servers' must be a table", path)
		}
		if server, exists := servers["codemap"]; exists {
			if !isOwnedCodemapMCPServer(server, "codex-setup") {
				return false, fmt.Errorf("%s already defines a conflicting codemap MCP server", path)
			}
			serverMap := server.(map[string]any)
			args := managedMCPArgs(buildinfo.Current(), "codex-setup")
			if serverMap["command"] == executable && stringSlicesEqual(mcpServerArgs(serverMap), args) {
				return false, nil
			}
			updated, err := replaceCodexMCPCommand(data, executable, args)
			if err != nil {
				return false, fmt.Errorf("update %s: %w", path, err)
			}
			return true, writeValidatedTOML(path, updated)
		}
	}
	const section = "[mcp_servers.codemap]"
	body := section + "\ncommand = " + tomlString(executable) + "\nargs = " + tomlStringArray(managedMCPArgs(buildinfo.Current(), "codex-setup")) + "\n"
	addition := "\n" + body
	if len(data) == 0 {
		addition = body
	} else if data[len(data)-1] == '\n' {
		addition = body
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, writeValidatedTOML(path, append(data, []byte(addition)...))
}

func writeValidatedTOML(path string, data []byte) error {
	var parsed map[string]any
	if err := toml.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("refuse to write invalid TOML: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func isOwnedCodemapMCPServer(raw any, integration string) bool {
	server, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	command, ok := server["command"].(string)
	if !ok {
		return false
	}
	args := mcpServerArgs(server)
	if command == "codemap" && stringSlicesEqual(args, []string{"mcp"}) {
		return true
	}
	return isAbsoluteIntegrationPath(command) && len(args) == 5 && args[0] == "mcp" && args[1] == "--configured-version" && args[2] != "" && args[3] == "--integration" && args[4] == integration
}

func ensureClaudeHooks(settingsPath string, global bool) (ensureHooksResult, error) {
	executable, err := resolveIntegrationExecutable()
	if err != nil {
		return ensureHooksResult{}, err
	}
	return ensureClaudeHooksWithExecutable(settingsPath, global, executable)
}

func ensureClaudeHooksWithExecutable(settingsPath string, global bool, executable string) (ensureHooksResult, error) {
	return ensureHooks(settingsPath, global, generatedClaudeHooks(executable), nil)
}

func ensureCodexHooksWithExecutable(settingsPath string, global bool, executable string) (ensureHooksResult, error) {
	specs := generatedCodexHooks(executable)
	return ensureHooks(settingsPath, global, specs, codexHookCommandAliases(specs))
}

func codexHookCommandAliases(specs []claudeHookSpec) map[string]string {
	return map[string]string{
		"CODEX=1 codemap hook session-start":                specs[0].Command,
		"CODEX=1 codemap hook pre-edit":                     specs[1].Command,
		"CODEX=1 codemap hook post-edit":                    specs[2].Command,
		"CODEX=1 codemap hook prompt-submit":                specs[3].Command,
		"CODEX=1 codemap hook pre-compact":                  specs[4].Command,
		"CODEX=1 codemap hook session-stop >/dev/null":      specs[5].Command,
		"CODEX=1 codemap hook session-stop > /dev/null":     specs[5].Command,
		"CODEX=1 codemap hook session-stop 2>/dev/null":     specs[5].Command,
		"CODEX=1 codemap hook session-stop 2> /dev/null":    specs[5].Command,
		"CODEX=1 codemap hook session-stop >/dev/null 2>&1": specs[5].Command,
	}
}

func ensureHooks(settingsPath string, global bool, specs []claudeHookSpec, commandAliases map[string]string) (ensureHooksResult, error) {
	result := ensureHooksResult{
		SettingsPath:   settingsPath,
		TotalCodemap:   len(specs),
		TargetIsGlobal: global,
	}

	settingsExisted := false
	root := make(map[string]interface{})
	data, err := os.ReadFile(settingsPath)
	switch {
	case err == nil:
		settingsExisted = true
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return result, fmt.Errorf("parse %s: %w", settingsPath, err)
			}
			if root == nil {
				return result, fmt.Errorf("parse %s: root must be an object", settingsPath)
			}
		}
	case os.IsNotExist(err):
		result.CreatedFile = true
	default:
		return result, fmt.Errorf("read %s: %w", settingsPath, err)
	}

	hooksByEvent := make(map[string]any)
	if raw, ok := root["hooks"]; ok && raw != nil {
		var ok bool
		hooksByEvent, ok = raw.(map[string]any)
		if !ok {
			return result, fmt.Errorf("parse hooks in %s: hooks must be an object", settingsPath)
		}
	}
	migrated, err := migrateRawHookCommands(hooksByEvent, commandAliases, specs)
	if err != nil {
		return result, fmt.Errorf("parse hooks in %s: %w", settingsPath, err)
	}

	for _, spec := range specs {
		entries, err := rawHookEntries(hooksByEvent, spec.Event)
		if err != nil {
			return result, fmt.Errorf("parse hooks in %s: %w", settingsPath, err)
		}
		if hasRawHookSpec(entries, spec) {
			result.ExistingHooks++
			continue
		}
		entry := map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": spec.Command}},
		}
		if spec.Matcher != "" {
			entry["matcher"] = spec.Matcher
		}
		hooksByEvent[spec.Event] = append(entries, entry)
		result.AddedHooks++
	}

	// Preserve no-op behavior when settings already contain all recommended hooks.
	if settingsExisted && result.AddedHooks == 0 && !migrated {
		return result, nil
	}

	root["hooks"] = hooksByEvent

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return result, fmt.Errorf("create .claude directory: %w", err)
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return result, fmt.Errorf("encode settings: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		return result, fmt.Errorf("write %s: %w", settingsPath, err)
	}
	result.WroteFile = true

	return result, nil
}

func migrateRawHookCommands(hooksByEvent map[string]any, aliases map[string]string, specs []claudeHookSpec) (bool, error) {
	migrated := false
	for event := range hooksByEvent {
		entries, err := rawHookEntries(hooksByEvent, event)
		if err != nil {
			return false, err
		}
		for entryIndex, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				return false, fmt.Errorf("event %q entry %d must be an object", event, entryIndex)
			}
			rawHooks, exists := entry["hooks"]
			if !exists || rawHooks == nil {
				continue
			}
			hooks, ok := rawHooks.([]any)
			if !ok {
				return false, fmt.Errorf("event %q entry %d hooks must be an array", event, entryIndex)
			}
			for hookIndex, rawHook := range hooks {
				hook, ok := rawHook.(map[string]any)
				if !ok {
					return false, fmt.Errorf("event %q entry %d hook %d must be an object", event, entryIndex, hookIndex)
				}
				typeName, _ := hook["type"].(string)
				if !strings.EqualFold(strings.TrimSpace(typeName), "command") {
					continue
				}
				command, _ := hook["command"].(string)
				if replacement, ok := aliases[strings.TrimSpace(command)]; ok {
					hook["command"] = replacement
					migrated = true
					continue
				}
				for _, spec := range specs {
					if replacement, ok := migrateOwnedHookCommand(command, spec.Command); ok {
						if command != replacement {
							hook["command"] = replacement
							migrated = true
						}
						break
					}
				}
			}
		}
	}
	return migrated, nil
}

func rawHookEntries(hooksByEvent map[string]any, event string) ([]any, error) {
	raw, exists := hooksByEvent[event]
	if !exists || raw == nil {
		return nil, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("event %q must be an array", event)
	}
	return entries, nil
}

func hasRawHookSpec(entries []any, spec claudeHookSpec) bool {
	targetCommand := strings.TrimSpace(spec.Command)
	requiredMatcher := strings.TrimSpace(spec.Matcher)
	for _, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		matcher, _ := entry["matcher"].(string)
		if requiredMatcher != "" && !strings.EqualFold(strings.TrimSpace(matcher), requiredMatcher) {
			continue
		}
		hooks, _ := entry["hooks"].([]any)
		for _, rawHook := range hooks {
			hook, ok := rawHook.(map[string]any)
			if !ok {
				continue
			}
			typeName, _ := hook["type"].(string)
			command, _ := hook["command"].(string)
			if strings.EqualFold(strings.TrimSpace(typeName), "command") && strings.TrimSpace(command) == targetCommand {
				return true
			}
		}
	}
	return false
}

func hasHookSpec(entries []claudeHookEntry, spec claudeHookSpec) bool {
	targetCommand := strings.TrimSpace(spec.Command)
	requiredMatcher := strings.TrimSpace(spec.Matcher)
	for _, entry := range entries {
		if requiredMatcher != "" && !strings.EqualFold(strings.TrimSpace(entry.Matcher), requiredMatcher) {
			continue
		}
		for _, hook := range entry.Hooks {
			if strings.EqualFold(strings.TrimSpace(hook.Type), "command") && strings.TrimSpace(hook.Command) == targetCommand {
				return true
			}
		}
	}
	return false
}

func mcpServerArgs(server map[string]any) []string {
	switch args := server["args"].(type) {
	case []string:
		return args
	case []any:
		out := make([]string, len(args))
		for i, value := range args {
			text, ok := value.(string)
			if !ok {
				return nil
			}
			out[i] = text
		}
		return out
	default:
		return nil
	}
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func isAbsoluteIntegrationPath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	return len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func tomlString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func tomlStringArray(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = tomlString(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func replaceCodexMCPCommand(data []byte, executable string, args []string) ([]byte, error) {
	lines := strings.SplitAfter(string(data), "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if start < 0 {
			header := strings.TrimSpace(tomlValueWithoutComment(trimmed))
			if header == "[mcp_servers.codemap]" || header == `[mcp_servers."codemap"]` || header == "[mcp_servers.'codemap']" {
				start = i
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			end = i
			break
		}
	}
	if start < 0 {
		return nil, errors.New("recognized codemap table could not be located")
	}
	commandCount, argsCount := 0, 0
	for i := start + 1; i < end; i++ {
		line := lines[i]
		newline := ""
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			newline = "\n"
		}
		equals := strings.IndexByte(line, '=')
		if equals < 0 {
			continue
		}
		key := strings.TrimSpace(line[:equals])
		var replacement string
		switch key {
		case "command":
			commandCount++
			replacement = tomlString(executable)
		case "args":
			argsCount++
			replacement = tomlStringArray(args)
		default:
			continue
		}
		if !isSingleLineTOMLValue(line[equals+1:]) {
			return nil, fmt.Errorf("recognized codemap %s assignment spans multiple lines", key)
		}
		comment := tomlInlineComment(line[equals+1:])
		lines[i] = line[:equals+1] + " " + replacement + comment + newline
	}
	if commandCount != 1 || argsCount != 1 {
		return nil, errors.New("recognized codemap table must contain one command and one args assignment")
	}
	updated := []byte(strings.Join(lines, ""))
	var reparsed map[string]any
	if err := toml.Unmarshal(updated, &reparsed); err != nil {
		return nil, fmt.Errorf("generated invalid TOML: %w", err)
	}
	return updated, nil
}

func tomlInlineComment(value string) string {
	if index := tomlCommentIndex(value); index >= 0 {
		return " " + strings.TrimSpace(value[index:])
	}
	return ""
}

func tomlValueWithoutComment(value string) string {
	if index := tomlCommentIndex(value); index >= 0 {
		return value[:index]
	}
	return value
}

func tomlCommentIndex(value string) int {
	var quote byte
	escaped := false
	for i := 0; i < len(value); i++ {
		char := value[i]
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && char == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '"' || char == '\'' {
			quote = char
			continue
		}
		if char == '#' {
			return i
		}
	}
	return -1
}

func isSingleLineTOMLValue(value string) bool {
	value = strings.TrimSpace(tomlValueWithoutComment(value))
	if value == "" {
		return false
	}
	var parsed map[string]any
	return toml.Unmarshal([]byte("value = "+value+"\n"), &parsed) == nil
}

func migrateOwnedHookCommand(existing, target string) (string, bool) {
	existing = strings.TrimSpace(existing)
	target = strings.TrimSpace(target)
	targetHook := strings.Index(target, " hook ")
	if targetHook < 0 {
		return "", false
	}
	suffix := target[targetHook:]
	marker := " --integration="
	markerIndex := strings.LastIndex(suffix, marker)
	if markerIndex < 0 {
		return "", false
	}
	legacySuffix := suffix[:markerIndex]
	if existing == "codemap"+legacySuffix {
		return target, true
	}
	if existing == target {
		return target, true
	}
	if strings.HasSuffix(existing, legacySuffix) {
		prefix := strings.TrimSpace(strings.TrimSuffix(existing, legacySuffix))
		path, ok := unquoteHookExecutable(prefix)
		if ok && isAbsoluteIntegrationPath(path) && isLegacyCodemapHookExecutable(path) {
			return target, true
		}
	}
	if !strings.HasSuffix(existing, suffix) {
		return "", false
	}
	prefix := strings.TrimSpace(strings.TrimSuffix(existing, suffix))
	path, ok := unquoteHookExecutable(prefix)
	if !ok || !isAbsoluteIntegrationPath(path) {
		return "", false
	}
	return target, true
}

func isLegacyCodemapHookExecutable(path string) bool {
	normalized := strings.ReplaceAll(path, `\`, "/")
	name := normalized[strings.LastIndex(normalized, "/")+1:]
	return name == "codemap" || name == "codemap.exe"
}

func unquoteHookExecutable(value string) (string, bool) {
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], `'"'"'`, `'`), true
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return strings.ReplaceAll(value[1:len(value)-1], `\"`, `"`), true
	}
	if !strings.ContainsAny(value, " \t\r\n") {
		return value, true
	}
	return "", false
}
