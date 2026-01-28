package render

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// CloneAnimation renders a tiny nautical progress indicator
type CloneAnimation struct {
	w        io.Writer
	repoName string
}

// NewCloneAnimation creates a new animation renderer
func NewCloneAnimation(w io.Writer, repoName string) *CloneAnimation {
	return &CloneAnimation{
		w:        w,
		repoName: repoName,
	}
}

// Render draws a single-line progress indicator with map emoji
func (a *CloneAnimation) Render(progress int) {
	// Move cursor to beginning of line and clear it
	fmt.Fprint(a.w, "\r\033[K")

	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}

	frame := a.buildFrame(progress)
	fmt.Fprint(a.w, frame)
}

func (a *CloneAnimation) buildFrame(progress int) string {
	var sb strings.Builder

	// Map emoji + repo name + progress dots + percentage
	name := truncate(a.repoName, 25)
	sb.WriteString(fmt.Sprintf("  🗺️  %s ", name))

	// Progress bar with dots
	barLen := 10
	filled := (progress * barLen) / 100

	for i := 0; i < barLen; i++ {
		if i < filled {
			sb.WriteString(Yellow + "·" + Reset)
		} else {
			sb.WriteString(Dim + "·" + Reset)
		}
	}

	sb.WriteString(fmt.Sprintf(" %d%%", progress))

	return sb.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-2] + ".."
}

// Demo runs a demo animation (for testing)
func (a *CloneAnimation) Demo() {
	for p := 0; p <= 100; p += 2 {
		a.Render(p)
		time.Sleep(80 * time.Millisecond)
	}
	fmt.Fprintln(a.w) // Final newline
	time.Sleep(500 * time.Millisecond)
}
