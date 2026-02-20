package handoff

import (
	"fmt"
	"strings"
)

// RenderMarkdown returns a markdown handoff summary suitable for chat context.
// Output is deterministic for the same artifact content.
func RenderMarkdown(a *Artifact) string {
	if a == nil {
		return ""
	}
	normalizeArtifact(a)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Handoff (%s)\n", a.Branch))
	b.WriteString(fmt.Sprintf("Base ref: `%s`\n", a.BaseRef))

	b.WriteString("\n### Prefix (Stable Context)\n")
	if a.Prefix.FileCount > 0 {
		b.WriteString(fmt.Sprintf("- File count: %d\n", a.Prefix.FileCount))
	}
	if len(a.Prefix.Hubs) == 0 {
		b.WriteString("- Hub files: none\n")
	} else {
		b.WriteString(fmt.Sprintf("- Hub files: %d\n", len(a.Prefix.Hubs)))
		for i, hub := range a.Prefix.Hubs {
			if i >= 15 {
				b.WriteString(fmt.Sprintf("- ... and %d more\n", len(a.Prefix.Hubs)-15))
				break
			}
			b.WriteString(fmt.Sprintf("- `%s` (%d importers)\n", hub.Path, hub.Importers))
		}
	}

	b.WriteString("\n### Delta (Recent Work)\n")
	if len(a.Delta.Changed) == 0 {
		b.WriteString("- Changed files: none detected\n")
	} else {
		b.WriteString(fmt.Sprintf("- Changed files: %d\n", len(a.Delta.Changed)))
		for i, stub := range a.Delta.Changed {
			if i >= 20 {
				b.WriteString(fmt.Sprintf("- ... and %d more\n", len(a.Delta.Changed)-20))
				break
			}
			status := stub.Status
			if status == "" {
				status = "changed"
			}
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", stub.Path, status))
		}
	}

	b.WriteString("\n### Risk Files\n")
	if len(a.Delta.RiskFiles) == 0 {
		b.WriteString("- None flagged\n")
	} else {
		for _, r := range a.Delta.RiskFiles {
			hub := ""
			if r.IsHub {
				hub = " [HUB]"
			}
			b.WriteString(fmt.Sprintf("- `%s` (%d importers)%s\n", r.Path, r.Importers, hub))
		}
	}

	b.WriteString("\n### Recent Timeline\n")
	if len(a.Delta.RecentEvents) == 0 {
		b.WriteString("- No recent events captured\n")
	} else {
		for _, e := range a.Delta.RecentEvents {
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

	if len(a.Delta.NextSteps) > 0 {
		b.WriteString("\n### Next Steps\n")
		for _, step := range a.Delta.NextSteps {
			b.WriteString(fmt.Sprintf("- %s\n", step))
		}
	}
	if len(a.Delta.OpenQuestions) > 0 {
		b.WriteString("\n### Open Questions\n")
		for _, q := range a.Delta.OpenQuestions {
			b.WriteString(fmt.Sprintf("- %s\n", q))
		}
	}

	return b.String()
}

// RenderPrefixMarkdown renders only the stable prefix layer.
func RenderPrefixMarkdown(p PrefixSnapshot) string {
	var b strings.Builder
	b.WriteString("## Handoff Prefix\n")
	if p.FileCount > 0 {
		b.WriteString(fmt.Sprintf("- File count: %d\n", p.FileCount))
	}
	if len(p.Hubs) == 0 {
		b.WriteString("- Hub files: none\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("- Hub files: %d\n", len(p.Hubs)))
	for _, hub := range p.Hubs {
		b.WriteString(fmt.Sprintf("- `%s` (%d importers)\n", hub.Path, hub.Importers))
	}
	return b.String()
}

// RenderDeltaMarkdown renders only the delta layer.
func RenderDeltaMarkdown(d DeltaSnapshot) string {
	var b strings.Builder
	b.WriteString("## Handoff Delta\n")
	if len(d.Changed) == 0 {
		b.WriteString("- Changed files: none\n")
	} else {
		b.WriteString(fmt.Sprintf("- Changed files: %d\n", len(d.Changed)))
		for _, stub := range d.Changed {
			status := stub.Status
			if status == "" {
				status = "changed"
			}
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", stub.Path, status))
		}
	}

	if len(d.RiskFiles) > 0 {
		b.WriteString("\n### Risk Files\n")
		for _, r := range d.RiskFiles {
			b.WriteString(fmt.Sprintf("- `%s` (%d importers)\n", r.Path, r.Importers))
		}
	}
	return b.String()
}

// RenderFileDetailMarkdown renders lazy-loaded detail for one file stub.
func RenderFileDetailMarkdown(d *FileDetail) string {
	if d == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Handoff File Detail: `%s`\n", d.Path))
	if d.Status != "" {
		b.WriteString(fmt.Sprintf("- Status: %s\n", d.Status))
	}
	if d.Hash != "" {
		b.WriteString(fmt.Sprintf("- Hash: `%s`\n", d.Hash))
	}
	if d.Size > 0 {
		b.WriteString(fmt.Sprintf("- Size: %d bytes\n", d.Size))
	}
	if d.IsHub {
		b.WriteString("- Hub: yes\n")
	}

	b.WriteString("\n### Importers\n")
	if len(d.Importers) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, importer := range d.Importers {
			b.WriteString(fmt.Sprintf("- `%s`\n", importer))
		}
	}

	b.WriteString("\n### Imports\n")
	if len(d.Imports) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, imp := range d.Imports {
			b.WriteString(fmt.Sprintf("- `%s`\n", imp))
		}
	}

	if len(d.RecentEvents) > 0 {
		b.WriteString("\n### Recent Events\n")
		for _, e := range d.RecentEvents {
			b.WriteString(fmt.Sprintf("- %s `%s` (%d)\n", e.Time.Format("15:04:05"), e.Op, e.Delta))
		}
	}
	return b.String()
}

// RenderCompact produces a short plain-text summary for session-start hooks.
func RenderCompact(a *Artifact, maxItems int) string {
	if a == nil {
		return ""
	}
	normalizeArtifact(a)
	if maxItems <= 0 {
		maxItems = 5
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("   Branch: %s\n", a.Branch))
	b.WriteString(fmt.Sprintf("   Base ref: %s\n", a.BaseRef))
	b.WriteString(fmt.Sprintf("   Changed files: %d\n", len(a.Delta.Changed)))

	if len(a.Delta.Changed) > 0 {
		b.WriteString("   Top changes:\n")
		for i, stub := range a.Delta.Changed {
			if i >= maxItems {
				b.WriteString(fmt.Sprintf("   ... and %d more\n", len(a.Delta.Changed)-maxItems))
				break
			}
			status := stub.Status
			if status == "" {
				status = "changed"
			}
			b.WriteString(fmt.Sprintf("   • %s (%s)\n", stub.Path, status))
		}
	}

	if len(a.Delta.RiskFiles) > 0 {
		b.WriteString("   Risk files:\n")
		for i, r := range a.Delta.RiskFiles {
			if i >= maxItems {
				b.WriteString(fmt.Sprintf("   ... and %d more\n", len(a.Delta.RiskFiles)-maxItems))
				break
			}
			b.WriteString(fmt.Sprintf("   ⚠️  %s (%d importers)\n", r.Path, r.Importers))
		}
	}

	return b.String()
}
