package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	pluginbundle "codemap/plugins"
)

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
		fmt.Println("Usage: codemap plugin <install>")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  install       Install the Codemap plugin into ~/.agents/plugins and ~/plugins")
	}
}

func runPluginInstall(args []string) {
	fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	homeDir := fs.String("home", "", "Override the home directory used for plugin installation")
	pluginPath := fs.String("plugin-path", "", "Override the installed plugin path")
	marketplacePath := fs.String("marketplace-path", "", "Override the marketplace manifest path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println("Usage: codemap plugin install [--home <dir>] [--plugin-path <dir>] [--marketplace-path <file>]")
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Usage: codemap plugin install [--home <dir>] [--plugin-path <dir>] [--marketplace-path <file>]")
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: codemap plugin install [--home <dir>] [--plugin-path <dir>] [--marketplace-path <file>]")
		os.Exit(1)
	}

	result, err := pluginbundle.InstallCodemapPlugin(pluginbundle.InstallOptions{
		HomeDir:         *homeDir,
		PluginPath:      *pluginPath,
		MarketplacePath: *marketplacePath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Plugin install failed: %v\n", err)
		os.Exit(1)
	}

	absPluginPath, _ := filepath.Abs(result.PluginPath)
	absMarketplacePath, _ := filepath.Abs(result.MarketplacePath)

	fmt.Println("codemap plugin install")
	fmt.Printf("Plugin: %s\n", absPluginPath)
	fmt.Printf("Marketplace: %s\n", absMarketplacePath)
	fmt.Printf("Files written: %d\n", result.FilesWritten)
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
	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  1. Restart Codex or reload plugins.")
	fmt.Println("  2. Verify the Codemap plugin appears in your plugin list.")
	fmt.Println("  3. If Codemap was installed before this release, update it so `codemap mcp` is available.")
}
