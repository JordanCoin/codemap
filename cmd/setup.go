package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codemap/config"
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

var recommendedClaudeHooks = []claudeHookSpec{
	{Event: "SessionStart", Command: "codemap hook session-start"},
	{Event: "PreToolUse", Matcher: "Edit|Write", Command: "codemap hook pre-edit"},
	{Event: "PostToolUse", Matcher: "Edit|Write", Command: "codemap hook post-edit"},
	{Event: "UserPromptSubmit", Command: "codemap hook prompt-submit"},
	{Event: "PreCompact", Command: "codemap hook pre-compact"},
	{Event: "SessionEnd", Command: "codemap hook session-stop"},
}

// RunSetup configures codemap for the recommended hooks-first workflow.
//
// By default it creates:
//   - <project>/.codemap/config.json (if missing)
//   - <project>/.claude/settings.local.json codemap hook entries
//
// Use --global to target ~/.claude/settings.json for hooks.
func RunSetup(args []string, defaultRoot string) {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	useGlobalHooks := fs.Bool("global", false, "Install hooks into ~/.claude/settings.json instead of project-local .claude/settings.local.json")
	skipConfig := fs.Bool("no-config", false, "Skip creating .codemap/config.json")
	skipHooks := fs.Bool("no-hooks", false, "Skip writing Claude hook settings")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println("Usage: codemap setup [--global] [--no-config] [--no-hooks] [path]")
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Usage: codemap setup [--global] [--no-config] [--no-hooks] [path]")
		os.Exit(2)
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "Usage: codemap setup [--global] [--no-config] [--no-hooks] [path]")
		os.Exit(1)
	}

	root := defaultRoot
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	if !*skipConfig {
		if _, err := os.Stat(filepath.Join(absRoot, ".git")); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: %s is not a git repository root; continuing setup anyway.\n", absRoot)
		}
	}

	fmt.Println("codemap setup")
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
			os.Exit(1)
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
	} else {
		hookPath, err := claudeSettingsPath(absRoot, *useGlobalHooks)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Hooks: failed to resolve settings path (%v)\n", err)
			os.Exit(1)
		}
		hookResult, err := ensureClaudeHooks(hookPath, *useGlobalHooks)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Hooks: failed (%v)\n", err)
			os.Exit(1)
		}
		switch {
		case hookResult.AddedHooks == 0:
			fmt.Printf("Hooks: already configured (%s)\n", hookResult.SettingsPath)
		case hookResult.CreatedFile:
			fmt.Printf("Hooks: created %s (+%d codemap hooks)\n", hookResult.SettingsPath, hookResult.AddedHooks)
		default:
			fmt.Printf("Hooks: updated %s (+%d codemap hooks)\n", hookResult.SettingsPath, hookResult.AddedHooks)
		}
	}

	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  1. Restart Claude Code (or open a new session).")
	fmt.Println("  2. Verify hook output appears at session start.")
	fmt.Println("  3. Tune .codemap/config.json if you want narrower context.")
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

func ensureClaudeHooks(settingsPath string, global bool) (ensureHooksResult, error) {
	result := ensureHooksResult{
		SettingsPath:   settingsPath,
		TotalCodemap:   len(recommendedClaudeHooks),
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
		}
	case os.IsNotExist(err):
		result.CreatedFile = true
	default:
		return result, fmt.Errorf("read %s: %w", settingsPath, err)
	}

	hooksByEvent := make(map[string][]claudeHookEntry)
	if raw, ok := root["hooks"]; ok && raw != nil {
		rawJSON, err := json.Marshal(raw)
		if err != nil {
			return result, fmt.Errorf("encode existing hooks in %s: %w", settingsPath, err)
		}
		if string(rawJSON) != "null" {
			if err := json.Unmarshal(rawJSON, &hooksByEvent); err != nil {
				return result, fmt.Errorf("parse hooks in %s: %w", settingsPath, err)
			}
		}
	}

	for _, spec := range recommendedClaudeHooks {
		if hasHookSpec(hooksByEvent[spec.Event], spec) {
			result.ExistingHooks++
			continue
		}
		entry := claudeHookEntry{
			Matcher: spec.Matcher,
			Hooks: []claudeHookCommand{
				{
					Type:    "command",
					Command: spec.Command,
				},
			},
		}
		hooksByEvent[spec.Event] = append(hooksByEvent[spec.Event], entry)
		result.AddedHooks++
	}

	// Preserve no-op behavior when settings already contain all recommended hooks.
	if settingsExisted && result.AddedHooks == 0 {
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
