package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"codemap/scanner"
	"codemap/watch"
)

// hubInfo contains hub file information from daemon or fresh scan
type hubInfo struct {
	Hubs      []string
	Importers map[string][]string
	Imports   map[string][]string
}

// getHubInfo returns hub info from daemon state (fast) or fresh scan (slow)
func getHubInfo(root string) *hubInfo {
	// Try daemon state first (instant)
	if state := watch.ReadState(root); state != nil {
		return &hubInfo{
			Hubs:      state.Hubs,
			Importers: state.Importers,
			Imports:   state.Imports,
		}
	}

	// Fall back to fresh scan (slower)
	fg, err := scanner.BuildFileGraph(root)
	if err != nil {
		return nil
	}

	return &hubInfo{
		Hubs:      fg.HubFiles(),
		Importers: fg.Importers,
		Imports:   fg.Imports,
	}
}

// RunHook executes the named hook with the given project root
func RunHook(hookName, root string) error {
	switch hookName {
	case "session-start":
		return hookSessionStart(root)
	case "pre-edit":
		return hookPreEdit(root)
	case "post-edit":
		return hookPostEdit(root)
	case "prompt-submit":
		return hookPromptSubmit(root)
	case "pre-compact":
		return hookPreCompact(root)
	case "session-stop":
		return hookSessionStop(root)
	default:
		return fmt.Errorf("unknown hook: %s\nAvailable: session-start, pre-edit, post-edit, prompt-submit, pre-compact, session-stop", hookName)
	}
}

// hookSessionStart shows project structure, starts daemon, and shows hub warnings
func hookSessionStart(root string) error {
	// Start the watch daemon in background (if not already running)
	if !watch.IsRunning(root) {
		startDaemon(root)
	}

	fmt.Println("📍 Project Context:")
	fmt.Println()

	// Show project structure
	gitCache := scanner.NewGitIgnoreCache(root)
	files, err := scanner.ScanFiles(root, gitCache)
	if err != nil {
		return err
	}

	// Build and render a simple tree
	project := scanner.Project{
		Root:  root,
		Mode:  "tree",
		Files: files,
	}

	// Import render package would create circular dep, so just print summary
	fmt.Printf("Files: %d\n", len(files))

	// Count by extension
	extCounts := make(map[string]int)
	for _, f := range files {
		ext := f.Ext
		if ext == "" {
			ext = "(no ext)"
		}
		extCounts[ext]++
	}

	// Show top extensions
	fmt.Print("Top types: ")
	count := 0
	for ext, n := range extCounts {
		if count > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%s(%d)", ext, n)
		count++
		if count >= 5 {
			break
		}
	}
	fmt.Println()
	fmt.Println()

	// Show hub files (from daemon if running, otherwise fresh scan)
	info := getHubInfo(root)
	if info != nil && len(info.Hubs) > 0 {
		fmt.Println("⚠️  High-impact files (hubs):")
		for i, hub := range info.Hubs {
			if i >= 10 {
				fmt.Printf("   ... and %d more\n", len(info.Hubs)-10)
				break
			}
			importers := len(info.Importers[hub])
			fmt.Printf("   ⚠️  HUB FILE: %s (imported by %d files)\n", hub, importers)
		}
	}

	_ = project // silence unused warning
	return nil
}

// startDaemon launches the watch daemon in background
func startDaemon(root string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "watch", "start", root)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.Start()
	// Give daemon a moment to initialize
	time.Sleep(200 * time.Millisecond)
}

// hookPreEdit warns before editing hub files (reads JSON from stdin)
func hookPreEdit(root string) error {
	filePath, err := extractFilePathFromStdin()
	if err != nil || filePath == "" {
		return nil // silently skip if no file path
	}

	return checkFileImporters(root, filePath)
}

// hookPostEdit shows impact after editing (reads JSON from stdin)
func hookPostEdit(root string) error {
	filePath, err := extractFilePathFromStdin()
	if err != nil || filePath == "" {
		return nil
	}

	return checkFileImporters(root, filePath)
}

// hookPromptSubmit detects file mentions in user prompt
func hookPromptSubmit(root string) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil
	}

	// Extract prompt from JSON
	var data map[string]interface{}
	if err := json.Unmarshal(input, &data); err != nil {
		return nil
	}

	prompt, ok := data["prompt"].(string)
	if !ok || prompt == "" {
		return nil
	}

	// Look for file patterns in the prompt
	var filesMentioned []string

	// Check for common source file extensions
	extensions := []string{"go", "ts", "js", "py", "rs", "rb", "java", "swift", "kt", "c", "cpp", "h"}
	for _, ext := range extensions {
		pattern := regexp.MustCompile(`[a-zA-Z0-9_/-]+\.` + ext)
		matches := pattern.FindAllString(prompt, 3)
		filesMentioned = append(filesMentioned, matches...)
	}

	if len(filesMentioned) == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("📍 Context for mentioned files:")

	info := getHubInfo(root)
	if info == nil {
		return nil
	}

	for _, file := range filesMentioned {
		// Try to find the file in the graph
		if importers := info.Importers[file]; len(importers) > 0 {
			if len(importers) >= 3 {
				fmt.Printf("   ⚠️  %s is a HUB (imported by %d files)\n", file, len(importers))
			} else {
				fmt.Printf("   📍 %s (imported by %d files)\n", file, len(importers))
			}
		}
	}
	fmt.Println()

	return nil
}

// hookPreCompact saves hub state before context compaction
func hookPreCompact(root string) error {
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		return err
	}

	info := getHubInfo(root)
	if info == nil || len(info.Hubs) == 0 {
		return nil
	}

	// Write hub state
	hubsFile := filepath.Join(codemapDir, "hubs.txt")
	f, err := os.Create(hubsFile)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# Hub files at %s\n", time.Now().Format(time.RFC3339))
	for _, hub := range info.Hubs {
		fmt.Fprintln(f, hub)
	}

	fmt.Println()
	fmt.Printf("💾 Saved hub state to .codemap/hubs.txt before compact\n")
	fmt.Printf("   (%d hub files tracked)\n", len(info.Hubs))
	fmt.Println()

	return nil
}

// hookSessionStop summarizes what changed in the session and stops the daemon
func hookSessionStop(root string) error {
	// Read state BEFORE stopping daemon (includes timeline)
	state := watch.ReadState(root)

	// Stop the watch daemon
	stopDaemon(root)

	fmt.Println()
	fmt.Println("📊 Session Summary")
	fmt.Println("==================")

	// Show timeline from daemon events (if available)
	if state != nil && len(state.RecentEvents) > 0 {
		fmt.Println()
		fmt.Println("Edit Timeline:")

		// Calculate stats
		totalDelta := 0
		fileEdits := make(map[string]int) // file -> edit count
		hubEdits := 0

		for _, e := range state.RecentEvents {
			totalDelta += e.Delta
			fileEdits[e.Path]++
			if e.IsHub {
				hubEdits++
			}
		}

		// Show last 10 events
		events := state.RecentEvents
		start := 0
		if len(events) > 10 {
			start = len(events) - 10
			fmt.Printf("  ... %d earlier events\n", start)
		}

		for _, e := range events[start:] {
			deltaStr := ""
			if e.Delta > 0 {
				deltaStr = fmt.Sprintf(" +%d", e.Delta)
			} else if e.Delta < 0 {
				deltaStr = fmt.Sprintf(" %d", e.Delta)
			}

			hubStr := ""
			if e.IsHub {
				hubStr = " ⚠️HUB"
			}

			fmt.Printf("  %s %-6s %s%s%s\n",
				e.Time.Format("15:04:05"),
				e.Op,
				e.Path,
				deltaStr,
				hubStr,
			)
		}

		// Show stats
		fmt.Println()
		fmt.Printf("Stats: %d events, %d files touched, %+d lines",
			len(state.RecentEvents), len(fileEdits), totalDelta)
		if hubEdits > 0 {
			fmt.Printf(", %d hub edits", hubEdits)
		}
		fmt.Println()
	} else {
		// Fallback to git diff if no daemon events
		gitCmd := exec.Command("git", "diff", "--name-only")
		gitCmd.Dir = root
		output, err := gitCmd.Output()
		if err != nil {
			fmt.Println("No changes tracked.")
			return nil
		}

		modified := strings.TrimSpace(string(output))
		if modified == "" {
			fmt.Println("No files modified.")
			return nil
		}

		info := getHubInfo(root)

		fmt.Println()
		fmt.Println("Files modified:")
		lineScanner := bufio.NewScanner(strings.NewReader(modified))
		count := 0
		for lineScanner.Scan() {
			file := lineScanner.Text()
			count++
			if count > 10 {
				fmt.Printf("  ... and more\n")
				break
			}

			if info != nil && info.isHub(file) {
				importers := len(info.Importers[file])
				fmt.Printf("  ⚠️  %s (HUB - imported by %d files)\n", file, importers)
			} else {
				fmt.Printf("  • %s\n", file)
			}
		}
	}

	fmt.Println()
	return nil
}

// stopDaemon stops the watch daemon
func stopDaemon(root string) {
	if !watch.IsRunning(root) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "watch", "stop", root)
	cmd.Run()
}

// extractFilePathFromStdin reads JSON from stdin and extracts file_path
func extractFilePathFromStdin() (string, error) {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(input, &data); err != nil {
		// Try regex fallback for non-JSON or partial JSON
		re := regexp.MustCompile(`"file_path"\s*:\s*"([^"]+)"`)
		matches := re.FindSubmatch(input)
		if len(matches) >= 2 {
			return string(matches[1]), nil
		}
		return "", err
	}

	filePath, ok := data["file_path"].(string)
	if !ok {
		return "", nil
	}

	return filePath, nil
}

// checkFileImporters checks if a file is a hub and shows its importers
func checkFileImporters(root, filePath string) error {
	info := getHubInfo(root)
	if info == nil {
		return nil // silently skip if deps unavailable
	}

	// Handle absolute paths - convert to relative
	if filepath.IsAbs(filePath) {
		if rel, err := filepath.Rel(root, filePath); err == nil {
			filePath = rel
		}
	}

	importers := info.Importers[filePath]
	if len(importers) >= 3 {
		fmt.Println()
		fmt.Printf("⚠️  HUB FILE: %s\n", filePath)
		fmt.Printf("   Imported by %d files - changes have wide impact!\n", len(importers))
		fmt.Println()
		fmt.Println("   Dependents:")
		for i, imp := range importers {
			if i >= 5 {
				fmt.Printf("   ... and %d more\n", len(importers)-5)
				break
			}
			fmt.Printf("   • %s\n", imp)
		}
		fmt.Println()
	} else if len(importers) > 0 {
		fmt.Println()
		fmt.Printf("📍 File: %s\n", filePath)
		fmt.Printf("   Imported by %d file(s): %s\n", len(importers), strings.Join(importers, ", "))
		fmt.Println()
	}

	// Also check if this file imports any hubs
	imports := info.Imports[filePath]
	var hubImports []string
	for _, imp := range imports {
		if info.isHub(imp) {
			hubImports = append(hubImports, imp)
		}
	}
	if len(hubImports) > 0 {
		fmt.Printf("   Imports %d hub(s): %s\n", len(hubImports), strings.Join(hubImports, ", "))
		fmt.Println()
	}

	return nil
}

// isHub checks if a file is a hub (has 3+ importers)
func (h *hubInfo) isHub(path string) bool {
	return len(h.Importers[path]) >= 3
}
