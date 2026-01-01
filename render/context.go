package render

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codemap/watch"

	"github.com/charmbracelet/lipgloss"
)

// Color palette
var (
	pink      = lipgloss.Color("212")
	purple    = lipgloss.Color("99")
	cyan      = lipgloss.Color("86")
	green     = lipgloss.Color("78")
	yellow    = lipgloss.Color("220")
	orange    = lipgloss.Color("208")
	red       = lipgloss.Color("196")
	gray      = lipgloss.Color("245")
	darkGray  = lipgloss.Color("238")
	white     = lipgloss.Color("255")
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(pink).
			MarginBottom(1)

	headerBox = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(purple).
			Padding(0, 2).
			MarginBottom(1)

	sectionTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(cyan).
			MarginTop(1).
			MarginBottom(0)

	statLabel = lipgloss.NewStyle().
			Foreground(gray)

	statValue = lipgloss.NewStyle().
			Bold(true).
			Foreground(white)

	hubStyle = lipgloss.NewStyle().
			Foreground(purple)

	hubCountHigh = lipgloss.NewStyle().
			Foreground(orange).
			Bold(true)

	hubCountMed = lipgloss.NewStyle().
			Foreground(yellow)

	hubCountLow = lipgloss.NewStyle().
			Foreground(gray)

	eventCreate = lipgloss.NewStyle().
			Foreground(green).
			Bold(true)

	eventWrite = lipgloss.NewStyle().
			Foreground(yellow)

	eventRemove = lipgloss.NewStyle().
			Foreground(red)

	deltaPlus = lipgloss.NewStyle().
			Foreground(green)

	deltaMinus = lipgloss.NewStyle().
			Foreground(red)

	timeStyle = lipgloss.NewStyle().
			Foreground(darkGray)

	dimStyle = lipgloss.NewStyle().
			Foreground(gray)

	activeStyle = lipgloss.NewStyle().
			Foreground(pink).
			Bold(true)

	sparkFull  = lipgloss.NewStyle().Foreground(green).Render("█")
	sparkMed   = lipgloss.NewStyle().Foreground(yellow).Render("▆")
	sparkLow   = lipgloss.NewStyle().Foreground(orange).Render("▃")
	sparkEmpty = lipgloss.NewStyle().Foreground(darkGray).Render("▁")
)

// Context renders the daemon state and recent activity
func Context(root string) {
	daemonRunning := watch.IsRunning(root)
	state := stateFromJSON(root)
	events := readRecentEvents(root, 100)

	// Filter meaningful events
	meaningful := filterMeaningful(events)

	projectName := filepath.Base(root)

	// === HEADER ===
	var statusDot string
	var statusText string
	if daemonRunning {
		statusDot = lipgloss.NewStyle().Foreground(green).Render("●")
		statusText = "watching"
	} else {
		statusDot = lipgloss.NewStyle().Foreground(gray).Render("○")
		statusText = "idle"
	}

	header := titleStyle.Render(projectName) + "  " + statusDot + " " + dimStyle.Render(statusText)
	fmt.Println(headerBox.Render(header))

	// === STATS ROW ===
	if state != nil {
		statsLine := statLabel.Render("files ") + statValue.Render(fmt.Sprintf("%d", state.FileCount)) +
			statLabel.Render("  ·  hubs ") + statValue.Render(fmt.Sprintf("%d", len(state.Hubs)))

		// Add activity spark
		if len(meaningful) > 0 {
			spark := generateActivitySpark(meaningful)
			statsLine += statLabel.Render("  ·  activity ") + spark
		}
		fmt.Println(statsLine)
	}

	// === HUBS ===
	if state != nil && len(state.Hubs) > 0 {
		fmt.Println(sectionTitle.Render("◆ Hub Files"))

		// Sort hubs by importer count
		type hubInfo struct {
			path  string
			count int
		}
		hubs := make([]hubInfo, 0, len(state.Hubs))
		for _, h := range state.Hubs {
			hubs = append(hubs, hubInfo{h, len(state.Importers[h])})
		}
		sort.Slice(hubs, func(i, j int) bool {
			return hubs[i].count > hubs[j].count
		})

		maxShow := 6
		for i, h := range hubs {
			if i >= maxShow {
				remaining := len(hubs) - maxShow
				fmt.Println(dimStyle.Render(fmt.Sprintf("  ... +%d more", remaining)))
				break
			}

			// Color by importance
			var countStyle lipgloss.Style
			if h.count >= 10 {
				countStyle = hubCountHigh
			} else if h.count >= 5 {
				countStyle = hubCountMed
			} else {
				countStyle = hubCountLow
			}

			bar := strings.Repeat("━", min(h.count, 12))
			barStyled := countStyle.Render(bar)

			fmt.Printf("  %s %s %s\n",
				hubStyle.Render(h.path),
				barStyled,
				countStyle.Render(fmt.Sprintf("%d", h.count)))
		}
	}

	// === RECENT ACTIVITY ===
	if len(meaningful) > 0 {
		fmt.Println(sectionTitle.Render("◆ Recent Activity"))

		maxEvents := 8
		for i, e := range meaningful {
			if i >= maxEvents {
				break
			}

			var icon, pathStyled string
			switch e.Op {
			case "CREATE":
				icon = eventCreate.Render("+")
				pathStyled = eventCreate.Render(e.Path)
			case "WRITE":
				icon = eventWrite.Render("~")
				pathStyled = eventWrite.Render(e.Path)
			case "REMOVE":
				icon = eventRemove.Render("-")
				pathStyled = eventRemove.Render(e.Path)
			default:
				icon = dimStyle.Render("·")
				pathStyled = dimStyle.Render(e.Path)
			}

			delta := ""
			if e.Delta > 0 {
				delta = deltaPlus.Render(fmt.Sprintf(" +%d", e.Delta))
			} else if e.Delta < 0 {
				delta = deltaMinus.Render(fmt.Sprintf(" %d", e.Delta))
			}

			timeAgo := formatTimeAgo(e.Time)

			fmt.Printf("  %s %s%s %s\n", icon, pathStyled, delta, timeStyle.Render(timeAgo))
		}
	} else if !daemonRunning {
		fmt.Println()
		fmt.Println(dimStyle.Render("  No activity tracked"))
		fmt.Println(dimStyle.Render("  Run: ") + activeStyle.Render("codemap watch start"))
	}

	// === HOT FILES ===
	if len(meaningful) > 5 {
		hot := findHotFiles(meaningful)
		if len(hot) > 0 {
			fmt.Println(sectionTitle.Render("◆ Hot Files"))
			maxHot := 3
			for i, h := range hot {
				if i >= maxHot {
					break
				}
				flames := strings.Repeat("🔥", min(h.count/2+1, 3))
				fmt.Printf("  %s %s %s\n",
					activeStyle.Render(h.path),
					flames,
					dimStyle.Render(fmt.Sprintf("%d edits", h.count)))
			}
		}
	}

	fmt.Println()
}

// generateActivitySpark creates a mini sparkline of recent activity
func generateActivitySpark(events []eventEntry) string {
	// Group events into 6 buckets over last 24 hours
	buckets := make([]int, 6)
	now := time.Now()
	bucketDuration := 4 * time.Hour

	for _, e := range events {
		age := now.Sub(e.Time)
		bucket := int(age / bucketDuration)
		if bucket >= 0 && bucket < len(buckets) {
			buckets[len(buckets)-1-bucket]++ // Reverse so newest is rightmost
		}
	}

	// Find max for scaling
	maxCount := 1
	for _, c := range buckets {
		if c > maxCount {
			maxCount = c
		}
	}

	// Build sparkline
	var spark string
	for _, c := range buckets {
		ratio := float64(c) / float64(maxCount)
		if ratio > 0.75 {
			spark += sparkFull
		} else if ratio > 0.4 {
			spark += sparkMed
		} else if ratio > 0 {
			spark += sparkLow
		} else {
			spark += sparkEmpty
		}
	}

	return spark
}

// formatTimeAgo returns a human-readable relative time
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	if d < time.Minute {
		return "just now"
	} else if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hours)
	} else {
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

func filterMeaningful(events []eventEntry) []eventEntry {
	result := make([]eventEntry, 0)
	for _, e := range events {
		if e.Op == "REMOVE" && e.Lines == 0 && e.Delta == 0 {
			continue
		}
		result = append(result, e)
	}
	return result
}

// eventEntry represents a parsed event from the log
type eventEntry struct {
	Time  time.Time
	Op    string
	Path  string
	Lines int
	Delta int
	Dirty bool
	IsHub bool
}

// hotFile tracks edit frequency
type hotFile struct {
	path  string
	count int
}

// readRecentEvents reads the last N events from the events log
func readRecentEvents(root string, limit int) []eventEntry {
	logFile := filepath.Join(root, ".codemap", "events.log")
	f, err := os.Open(logFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}

	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}

	var events []eventEntry
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}

		timeStr := strings.TrimSpace(parts[0])
		t, err := time.Parse("2006-01-02 15:04:05", timeStr)
		if err != nil {
			continue
		}

		op := strings.TrimSpace(parts[1])
		path := strings.TrimSpace(parts[2])

		var linesCount, delta int
		var dirty bool
		if len(parts) >= 4 {
			fmt.Sscanf(strings.TrimSpace(parts[3]), "%d", &linesCount)
		}
		if len(parts) >= 5 {
			fmt.Sscanf(strings.TrimSpace(parts[4]), "%d", &delta)
		}
		if len(parts) >= 6 {
			dirty = strings.Contains(parts[5], "dirty")
		}

		events = append(events, eventEntry{
			Time:  t,
			Op:    op,
			Path:  path,
			Lines: linesCount,
			Delta: delta,
			Dirty: dirty,
		})
	}

	// Reverse and dedupe
	for i := 0; i < len(events)/2; i++ {
		j := len(events) - 1 - i
		events[i], events[j] = events[j], events[i]
	}

	deduped := make([]eventEntry, 0, len(events))
	for i, e := range events {
		if i == 0 {
			deduped = append(deduped, e)
			continue
		}
		prev := deduped[len(deduped)-1]
		if e.Path == prev.Path && e.Op == prev.Op && prev.Time.Sub(e.Time) < 5*time.Second {
			continue
		}
		deduped = append(deduped, e)
	}

	return deduped
}

// findHotFiles finds files with most edits
func findHotFiles(events []eventEntry) []hotFile {
	counts := make(map[string]int)
	for _, e := range events {
		if e.Op == "WRITE" || e.Op == "CREATE" {
			counts[e.Path]++
		}
	}

	var hot []hotFile
	for path, count := range counts {
		if count > 1 {
			hot = append(hot, hotFile{path, count})
		}
	}

	sort.Slice(hot, func(i, j int) bool {
		return hot[i].count > hot[j].count
	})

	return hot
}

// stateFromJSON parses a state.json file
func stateFromJSON(root string) *watch.State {
	stateFile := filepath.Join(root, ".codemap", "state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil
	}

	var state watch.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}

	return &state
}
