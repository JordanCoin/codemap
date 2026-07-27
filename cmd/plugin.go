package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codemap/internal/buildinfo"
	pluginbundle "codemap/plugins"
	"github.com/pelletier/go-toml/v2"
)

type pluginActivationStatus string

type codexPluginList struct {
	Installed []struct {
		PluginID  string `json:"pluginId"`
		Version   string `json:"version"`
		Installed bool   `json:"installed"`
		Enabled   bool   `json:"enabled"`
		Source    struct {
			Path string `json:"path"`
		} `json:"source"`
	} `json:"installed"`
}

const (
	pluginActivationInstalled pluginActivationStatus = "installed"
	pluginActivationUpdated   pluginActivationStatus = "updated"
	pluginActivationUnchanged pluginActivationStatus = "unchanged"
)

var runCodexPluginCommand = func(home string, args ...string) ([]byte, error) {
	cmd := exec.Command("codex", args...)
	if home != "" {
		env := make([]string, 0, len(os.Environ())+3)
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			if strings.EqualFold(name, "HOME") || strings.EqualFold(name, "USERPROFILE") || strings.EqualFold(name, "CODEX_HOME") {
				continue
			}
			env = append(env, entry)
		}
		cmd.Env = append(env, "HOME="+home, "USERPROFILE="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
	}
	out, err := cmd.Output()
	return out, codexCommandError(err)
}

func codexCommandError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if detail := strings.TrimSpace(string(exitErr.Stderr)); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
	}
	return err
}

var activateCodexPlugin = func(home, reference, desiredVersion string, sourceChanged bool) (pluginActivationStatus, error) {
	out, err := runCodexPluginCommand(home, "plugin", "list", "--json")
	if err != nil {
		return "", fmt.Errorf("inspect installed Codex plugins: %w", err)
	}
	var list codexPluginList
	if err := json.Unmarshal(out, &list); err != nil {
		return "", fmt.Errorf("parse installed Codex plugins: %w", err)
	}

	alreadyInstalled := false
	alreadyActive := false
	installedVersion := ""
	for _, plugin := range list.Installed {
		if plugin.PluginID == reference && plugin.Installed {
			alreadyInstalled = true
			alreadyActive = plugin.Enabled
			installedVersion = plugin.Version
			break
		}
	}
	if alreadyActive && installedVersion == desiredVersion && !sourceChanged {
		return pluginActivationUnchanged, nil
	}
	if _, err := runCodexPluginCommand(home, "plugin", "add", reference, "--json"); err != nil {
		return "", fmt.Errorf("install Codex plugin: %w", err)
	}
	if alreadyInstalled {
		return pluginActivationUpdated, nil
	}
	return pluginActivationInstalled, nil
}

// RunPlugin handles the "codemap plugin" subcommand.
func RunPlugin(args []string) {
	subCmd := ""
	if len(args) > 0 {
		subCmd = args[0]
	}

	switch subCmd {
	case "install":
		runPluginInstall(args[1:])
	default:
		fmt.Println("Usage: codemap plugin install")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  install       Install or update and activate the Codemap plugin")
	}
}

func runPluginInstall(args []string) {
	fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	homeDir := fs.String("home", "", "Override the home directory used for plugin installation")
	pluginPath := fs.String("plugin-path", "", "Override the installed plugin path")
	marketplacePath := fs.String("marketplace-path", "", "Override the marketplace manifest path")
	noActivate := fs.Bool("no-activate", false, "Install the plugin files without activating them in Codex")
	deprecatedActivate := fs.Bool("activate", false, "Deprecated: activation is now the default")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println("Usage: codemap plugin install [--no-activate] [--home <dir>] [--plugin-path <dir>] [--marketplace-path <file>]")
			fmt.Println("Activation is the default; use --no-activate to skip it. The legacy --activate is accepted but deprecated.")
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Usage: codemap plugin install [--no-activate] [--home <dir>] [--plugin-path <dir>] [--marketplace-path <file>]")
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: codemap plugin install [--no-activate] [--home <dir>] [--plugin-path <dir>] [--marketplace-path <file>]")
		os.Exit(1)
	}
	if *deprecatedActivate {
		fmt.Fprintln(os.Stderr, "Warning: --activate is deprecated; activation is now the default. Omit --activate.")
	}

	executable, err := resolveIntegrationExecutable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Plugin install failed: %v\n", err)
		os.Exit(1)
	}
	result, err := pluginbundle.InstallCodemapPlugin(pluginbundle.InstallOptions{
		HomeDir:         *homeDir,
		PluginPath:      *pluginPath,
		MarketplacePath: *marketplacePath,
		ExecutablePath:  executable,
		BinaryVersion:   buildinfo.Current(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Plugin install failed: %v\n", err)
		os.Exit(1)
	}
	hookReviewRoot := ""
	customMarketplaceStaging := *marketplacePath != ""
	if *homeDir == "" {
		migrated, err := migrateExistingCodexProjectIntegration(executable)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Plugin files installed, but project integration migration failed: %v\n", err)
			os.Exit(1)
		}
		if migrated > 0 {
			fmt.Printf("Project integration files migrated: %d\n", migrated)
		}
		if cwd, err := os.Getwd(); err == nil {
			if root := existingGitRepositoryRoot(cwd); root != "" {
				if owned, _ := hasOwnedCodexHooks(filepath.Join(root, ".codex", "hooks.json"), executable); owned && !*noActivate && !customMarketplaceStaging {
					if err := validateCodexHookTrust(root); err != nil {
						hookReviewRoot = root
					}
				}
			}
		}
	}

	absPluginPath, _ := filepath.Abs(result.PluginPath)
	absMarketplacePath, _ := filepath.Abs(result.MarketplacePath)

	fmt.Printf("Plugin: %s\n", absPluginPath)
	fmt.Printf("Marketplace: %s\n", absMarketplacePath)
	fmt.Printf("Marketplace name: %s\n", result.MarketplaceName)
	fmt.Printf("Files written: %d\n", result.FilesWritten)
	if result.FilesRemoved > 0 {
		fmt.Printf("Files removed: %d\n", result.FilesRemoved)
	}
	if result.FilesUnchanged > 0 {
		fmt.Printf("Files unchanged: %d\n", result.FilesUnchanged)
	}
	switch {
	case result.CreatedMarketplace:
		fmt.Println("Marketplace entry: created")
	case result.UpdatedMarketplace:
		fmt.Println("Marketplace entry: updated")
	default:
		fmt.Println("Marketplace entry: already configured")
	}

	sourceChanged := result.FilesWritten > 0 || result.FilesRemoved > 0 || result.CreatedMarketplace || result.UpdatedMarketplace
	pendingPath := filepath.Join(filepath.Dir(result.PluginPath), ".codemap-activation-pending")
	activationPending := sourceChanged
	if sourceChanged {
		if err := os.WriteFile(pendingPath, []byte("pending\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Plugin files installed, but activation state could not be recorded: %v\n", err)
			os.Exit(1)
		}
	} else if _, err := os.Stat(pendingPath); err == nil {
		activationPending = true
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Plugin files installed, but activation state could not be read: %v\n", err)
		os.Exit(1)
	}
	if !*noActivate && !customMarketplaceStaging {
		desiredVersion, err := codemapPluginVersion(result.PluginPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Plugin files installed, but the plugin version could not be read: %v\n", err)
			os.Exit(1)
		}
		status, err := activateCodexPlugin(*homeDir, pluginbundle.CodemapPluginName+"@"+result.MarketplaceName, desiredVersion, activationPending)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Plugin files installed, but Codex activation failed: %v\n", err)
			os.Exit(1)
		}
		if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: Codemap was activated, but activation state could not be cleared: %v\n", err)
		}
		fmt.Println()
		switch status {
		case pluginActivationInstalled:
			fmt.Println("Codemap installed and activated in Codex.")
		case pluginActivationUpdated:
			fmt.Println("Codemap plugin updated and activated in Codex.")
		case pluginActivationUnchanged:
			if hookReviewRoot == "" {
				fmt.Println("Codemap plugin is already current and active. No restart needed.")
			} else {
				fmt.Println("Codemap plugin is already current and active.")
			}
		}
		if hookReviewRoot != "" {
			fmt.Println("Next:")
			printCodexHookReviewInstructions(hookReviewRoot)
		} else if status != pluginActivationUnchanged {
			fmt.Println("Start a new Codex task/session.")
		}
		return
	}

	fmt.Println()
	if customMarketplaceStaging && !*noActivate {
		fmt.Println("Plugin files installed; Codex activation skipped for custom --marketplace-path.")
		fmt.Println("Omit --marketplace-path to install and activate in Codex automatically.")
		return
	}
	if activationPending {
		fmt.Println("Plugin files installed; Codex activation skipped (--no-activate).")
		fmt.Printf("Activate later with: codex plugin add %s@%s\n", pluginbundle.CodemapPluginName, result.MarketplaceName)
	} else {
		fmt.Println("Plugin files are already current; Codex activation skipped (--no-activate).")
	}
}

func migrateExistingCodexProjectIntegration(executable string) (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	root := existingGitRepositoryRoot(cwd)
	if root == "" {
		return 0, nil
	}
	migrated := 0
	configPath := filepath.Join(root, ".codex", "config.toml")
	if owned, err := hasOwnedCodexMCP(configPath); err != nil {
		return migrated, err
	} else if owned {
		changed, err := ensureCodexMCPWithExecutable(configPath, executable)
		if err != nil {
			return migrated, err
		}
		if changed {
			migrated++
		}
	}
	hooksPath := filepath.Join(root, ".codex", "hooks.json")
	if owned, err := hasOwnedCodexHooks(hooksPath, executable); err != nil {
		return migrated, err
	} else if owned {
		result, err := ensureCodexHooksWithExecutable(hooksPath, false, executable)
		if err != nil {
			return migrated, err
		}
		if result.WroteFile {
			migrated++
		}
	}
	return migrated, nil
}

func existingGitRepositoryRoot(start string) string {
	for current := start; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
	}
}

func hasOwnedCodexMCP(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	payload := map[string]any{}
	if err := toml.Unmarshal(data, &payload); err != nil {
		return false, nil
	}
	servers, _ := payload["mcp_servers"].(map[string]any)
	return isOwnedCodemapMCPServer(servers["codemap"], "codex-setup"), nil
}

func hasOwnedCodexHooks(path, executable string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		return false, nil
	}
	hooksByEvent, _ := root["hooks"].(map[string]any)
	specs := generatedCodexHooks(executable)
	aliases := codexHookCommandAliases(specs)
	for event := range hooksByEvent {
		entries, err := rawHookEntries(hooksByEvent, event)
		if err != nil {
			return false, nil
		}
		for _, rawEntry := range entries {
			entry, _ := rawEntry.(map[string]any)
			hooks, _ := entry["hooks"].([]any)
			for _, rawHook := range hooks {
				hook, _ := rawHook.(map[string]any)
				command, _ := hook["command"].(string)
				if _, ok := aliases[strings.TrimSpace(command)]; ok {
					return true, nil
				}
				for _, spec := range specs {
					if _, ok := migrateOwnedHookCommand(command, spec.Command); ok {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}

func codemapPluginVersion(pluginPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(pluginPath, ".codex-plugin", "plugin.json"))
	if err != nil {
		return "", err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return "", errors.New("plugin manifest has no version")
	}
	return manifest.Version, nil
}
