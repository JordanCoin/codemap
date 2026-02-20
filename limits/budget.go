package limits

import "strings"

const (
	// Shared text response budgets.
	MaxStructureOutputBytes = MaxContextOutputBytes
	MaxHandoffCompactBytes  = 3000
	MaxHandoffMarkdownBytes = 20000
	MaxHandoffDetailBytes   = 12000
)

// HandoffBudget controls list sizes and rendering budget for handoff payloads.
type HandoffBudget struct {
	MaxChanged       int
	MaxRisk          int
	MaxEvents        int
	MaxMarkdownBytes int
	MaxCompactBytes  int
	MaxDetailBytes   int
}

// HandoffBudgetForRepo returns a budget profile that scales down on larger repos.
func HandoffBudgetForRepo(fileCount int) HandoffBudget {
	switch {
	case fileCount > LargeRepoFileCount:
		return HandoffBudget{
			MaxChanged:       25,
			MaxRisk:          8,
			MaxEvents:        10,
			MaxMarkdownBytes: MaxHandoffMarkdownBytes,
			MaxCompactBytes:  MaxHandoffCompactBytes,
			MaxDetailBytes:   MaxHandoffDetailBytes,
		}
	case fileCount > MediumRepoFileCount:
		return HandoffBudget{
			MaxChanged:       40,
			MaxRisk:          10,
			MaxEvents:        15,
			MaxMarkdownBytes: MaxHandoffMarkdownBytes,
			MaxCompactBytes:  MaxHandoffCompactBytes,
			MaxDetailBytes:   MaxHandoffDetailBytes,
		}
	default:
		return HandoffBudget{
			MaxChanged:       60,
			MaxRisk:          15,
			MaxEvents:        25,
			MaxMarkdownBytes: MaxHandoffMarkdownBytes,
			MaxCompactBytes:  MaxHandoffCompactBytes,
			MaxDetailBytes:   MaxHandoffDetailBytes,
		}
	}
}

// TruncateAtLineBoundary trims output to maxBytes, preferring a clean newline cut.
func TruncateAtLineBoundary(output string, maxBytes int, truncatedMessage string) string {
	if maxBytes <= 0 || len(output) <= maxBytes {
		return output
	}

	trimmed := output[:maxBytes]
	lineCutThreshold := maxBytes - 1000
	if lineCutThreshold < 0 {
		lineCutThreshold = 0
	}
	if idx := strings.LastIndex(trimmed, "\n"); idx > lineCutThreshold {
		trimmed = trimmed[:idx]
	}

	if truncatedMessage == "" {
		truncatedMessage = "\n\n... (truncated)\n"
	}
	return trimmed + truncatedMessage
}
