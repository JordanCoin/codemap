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
	// Guard: require git repo
	gitDir := filepath.Join(root, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		fmt.Println("📍 Not a git repository - skipping project context")
		fmt.Println("   (codemap hooks work best in git repos)")
		return nil
	}

	// Handle meta-repo: parent git repo containing child git repos
	childRepos := findChildRepos(root)
	if len(childRepos) > 1 {
		return hookSessionStartMultiRepo(root, childRepos)
	}

	// Check for previous session context before starting new daemon
	lastSessionEvents := getLastSessionEvents(root)

	// Start the watch daemon in background (if not already running)
	if !watch.IsRunning(root) {
		startDaemon(root)
	}

	fmt.Println("📍 Project Context:")
	fmt.Println()

	// IMPORTANT: Hook output goes directly into Claude's "Messages" context, not system prompt.
	// This means hook output competes with conversation history for the ~200k token limit.
	// A 1.3MB output (like a full tree of a 10k file repo) = ~500k tokens = instant context overflow.
	//
	// We enforce two limits:
	// 1. Adaptive depth: larger repos get shallower trees (depth 2-4 based on file count)
	// 2. Hard cap: 60KB max output (~15k tokens, <10% of context window)
	//
	// Future: Consider structured output that Claude Code can format/truncate intelligently.
	exe, err := os.Executable()
	if err == nil {
		// Count files to determine appropriate depth
		fileCount := 0
		if state := watch.ReadState(root); state != nil {
			fileCount = state.FileCount
		}

		// Adaptive depth: large repos get shallower trees
		depth := "4"
		if fileCount > 5000 {
			depth = "2"
		} else if fileCount > 2000 {
			depth = "3"
		}

		cmd := exec.Command(exe, "--depth", depth, root)

		// Capture output to enforce size limit
		var buf strings.Builder
		cmd.Stdout = &buf
		cmd.Stderr = os.Stderr
		cmd.Run()

		output := buf.String()
		const maxBytes = 60000 // ~15k tokens, <10% of 200k context

		if len(output) > maxBytes {
			// Truncate and add warning
			output = output[:maxBytes]
			// Find last newline to avoid cutting mid-line
			if idx := strings.LastIndex(output, "\n"); idx > maxBytes-1000 {
				output = output[:idx]
			}
			output += "\n\n... (truncated - repo has " + fmt.Sprintf("%d", fileCount) + " files, use `codemap .` for full tree)\n"
		}

		fmt.Print(output)
		fmt.Println()
	}

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

	// Show diff vs main if on a feature branch
	showDiffVsMain(root)

	// Show last session context if resuming work
	if len(lastSessionEvents) > 0 {
		showLastSessionContext(root, lastSessionEvents)
	}

	return nil
}

// showDiffVsMain shows files changed on this branch vs main
func showDiffVsMain(root string) {
	// Check if we're on a branch other than main
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = root
	branchOut, err := branchCmd.Output()
	if err != nil {
		return
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "main" || branch == "master" {
		return // No diff to show on main branch
	}

	// Run codemap --diff to show changes
	exe, err := os.Executable()
	if err != nil {
		return
	}

	fmt.Println()
	fmt.Printf("📝 Changes on branch '%s' vs main:\n", branch)
	cmd := exec.Command(exe, "--diff", root)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

// getLastSessionEvents reads events.log for previous session context
func getLastSessionEvents(root string) []string {
	eventsFile := filepath.Join(root, ".codemap", "events.log")
	data, err := os.ReadFile(eventsFile)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return nil
	}

	// Get last 20 non-empty lines
	var recent []string
	for i := len(lines) - 1; i >= 0 && len(recent) < 20; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			recent = append([]string{lines[i]}, recent...)
		}
	}
	return recent
}

// showLastSessionContext displays what was worked on in previous session
func showLastSessionContext(root string, events []string) {
	// Extract unique files from events
	files := make(map[string]string) // file -> last operation
	for _, line := range events {
		parts := strings.Split(line, "|")
		if len(parts) >= 3 {
			op := strings.TrimSpace(parts[1])
			file := strings.TrimSpace(parts[2])
			if file != "" && op != "" {
				files[file] = op
			}
		}
	}

	if len(files) == 0 {
		return
	}

	// Fix atomic save artifacts: editors often do write-to-temp + rename,
	// which fsnotify sees as REMOVE. If file still exists, it was edited.
	for file, op := range files {
		if strings.EqualFold(op, "REMOVE") || strings.EqualFold(op, "RENAME") {
			if _, err := os.Stat(filepath.Join(root, file)); err == nil {
				files[file] = "edited"
			}
		}
	}

	fmt.Println()
	fmt.Println("🕐 Last session worked on:")
	count := 0
	for file, op := range files {
		if count >= 5 {
			fmt.Printf("   ... and %d more files\n", len(files)-5)
			break
		}
		fmt.Printf("   • %s (%s)\n", file, strings.ToLower(op))
		count++
	}
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

// hookPromptSubmit detects file mentions in user prompt and shows session context
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

	info := getHubInfo(root)

	// Look for file patterns in the prompt
	var filesMentioned []string

	// Check for common source file extensions (tsx before ts so it matches first)
	extensions := []string{"go", "tsx", "ts", "jsx", "js", "py", "rs", "rb", "java", "swift", "kt", "c", "cpp", "h"}
	for _, ext := range extensions {
		pattern := regexp.MustCompile(`[a-zA-Z0-9_/-]+\.` + ext)
		matches := pattern.FindAllString(prompt, 3)
		filesMentioned = append(filesMentioned, matches...)
	}

	// Build output for mentioned files
	var output []string
	if info != nil {
		for _, file := range filesMentioned {
			if importers := info.Importers[file]; len(importers) > 0 {
				if len(importers) >= 3 {
					output = append(output, fmt.Sprintf("   ⚠️  %s is a HUB (imported by %d files)", file, len(importers)))
				} else {
					output = append(output, fmt.Sprintf("   📍 %s (imported by %d files)", file, len(importers)))
				}
			}
		}
	}

	if len(output) > 0 {
		fmt.Println()
		fmt.Println("📍 Context for mentioned files:")
		for _, line := range output {
			fmt.Println(line)
		}
	}

	// Show mid-session awareness: what's been edited so far
	showSessionProgress(root)

	return nil
}

// showSessionProgress shows files edited so far in this session
func showSessionProgress(root string) {
	state := watch.ReadState(root)
	if state == nil || len(state.RecentEvents) == 0 {
		return
	}

	// Count unique files and hub edits
	filesEdited := make(map[string]bool)
	hubEdits := 0
	for _, e := range state.RecentEvents {
		filesEdited[e.Path] = true
		if e.IsHub {
			hubEdits++
		}
	}

	if len(filesEdited) == 0 {
		return
	}

	fmt.Println()
	fmt.Printf("📊 Session so far: %d files edited", len(filesEdited))
	if hubEdits > 0 {
		fmt.Printf(", %d hub edits", hubEdits)
	}
	fmt.Println()
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

// findChildRepos returns subdirectories that are git repositories
func findChildRepos(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var repos []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// Check if this subdirectory is a git repo
		if _, err := os.Stat(filepath.Join(root, e.Name(), ".git")); err == nil {
			repos = append(repos, e.Name())
		}
	}
	return repos
}

// hookSessionStartMultiRepo handles meta-repos containing multiple child repos
func hookSessionStartMultiRepo(root string, childRepos []string) error {
	fmt.Println("📍 Multi-Repo Project Context:")
	fmt.Printf("   %d repositories in %s\n", len(childRepos), filepath.Base(root))
	fmt.Println()

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// Run codemap on each child repo (with depth limit for compactness)
	for _, repo := range childRepos {
		repoPath := filepath.Join(root, repo)
		cmd := exec.Command(exe, "--depth", "2", repoPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
		fmt.Println()
	}

	return nil
}
