package handoff

import (
	"fmt"
	"strings"
	"time"
)

// RenderMarkdown returns a markdown handoff summary suitable for chat context.
func RenderMarkdown(a *Artifact) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Handoff (%s)\n", a.Branch))
	b.WriteString(fmt.Sprintf("Generated: %s\n", a.GeneratedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Base ref: `%s`\n", a.BaseRef))

	b.WriteString("\n### Changed Files\n")
	if len(a.ChangedFiles) == 0 {
		b.WriteString("- None detected\n")
	} else {
		b.WriteString(fmt.Sprintf("- %d files changed\n", len(a.ChangedFiles)))
		for i, file := range a.ChangedFiles {
			if i >= 20 {
				b.WriteString(fmt.Sprintf("- ... and %d more\n", len(a.ChangedFiles)-20))
				break
			}
			b.WriteString(fmt.Sprintf("- `%s`\n", file))
		}
	}

	b.WriteString("\n### Risk Files\n")
	if len(a.RiskFiles) == 0 {
		b.WriteString("- None flagged\n")
	} else {
		for _, r := range a.RiskFiles {
			hub := ""
			if r.IsHub {
				hub = " [HUB]"
			}
			b.WriteString(fmt.Sprintf("- `%s` (%d importers)%s\n", r.Path, r.Importers, hub))
		}
	}

	b.WriteString("\n### Recent Timeline\n")
	if len(a.RecentEvents) == 0 {
		b.WriteString("- No recent events captured\n")
	} else {
		for _, e := range a.RecentEvents {
			delta := ""
			if e.Delta > 0 {
				delta = fmt.Sprintf(" (+%d)", e.Delta)
			} else if e.Delta < 0 {
				delta = fmt.Sprintf(" (%d)", e.Delta)
			}
			hub := ""
			if e.IsHub {
				hub = " [HUB]"
			}
			b.WriteString(fmt.Sprintf("- %s `%s` `%s`%s%s\n", e.Time.Format("15:04:05"), e.Op, e.Path, delta, hub))
		}
	}

	if len(a.NextSteps) > 0 {
		b.WriteString("\n### Next Steps\n")
		for _, step := range a.NextSteps {
			b.WriteString(fmt.Sprintf("- %s\n", step))
		}
	}
	if len(a.OpenQuestions) > 0 {
		b.WriteString("\n### Open Questions\n")
		for _, q := range a.OpenQuestions {
			b.WriteString(fmt.Sprintf("- %s\n", q))
		}
	}

	return b.String()
}

// RenderCompact produces a short plain-text summary for session-start hooks.
func RenderCompact(a *Artifact, maxItems int) string {
	if a == nil {
		return ""
	}
	if maxItems <= 0 {
		maxItems = 5
	}

	var b strings.Builder
	age := time.Since(a.GeneratedAt).Round(time.Minute)
	if age < 0 {
		age = 0
	}
	b.WriteString(fmt.Sprintf("   Branch: %s (%s ago)\n", a.Branch, age))
	b.WriteString(fmt.Sprintf("   Changed files: %d\n", len(a.ChangedFiles)))

	if len(a.ChangedFiles) > 0 {
		b.WriteString("   Top changes:\n")
		for i, f := range a.ChangedFiles {
			if i >= maxItems {
				b.WriteString(fmt.Sprintf("   ... and %d more\n", len(a.ChangedFiles)-maxItems))
				break
			}
			b.WriteString(fmt.Sprintf("   • %s\n", f))
		}
	}

	if len(a.RiskFiles) > 0 {
		b.WriteString("   Risk files:\n")
		for i, r := range a.RiskFiles {
			if i >= maxItems {
				b.WriteString(fmt.Sprintf("   ... and %d more\n", len(a.RiskFiles)-maxItems))
				break
			}
			b.WriteString(fmt.Sprintf("   ⚠️  %s (%d importers)\n", r.Path, r.Importers))
		}
	}

	return b.String()
}
