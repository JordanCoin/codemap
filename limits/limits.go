package limits

// Context output budgets for hook and MCP text responses.
const (
	MaxContextOutputBytes = 60000 // ~15k tokens, <10% of a 200k context window
)

// Repo-size thresholds used to scale expensive analysis work.
const (
	MediumRepoFileCount = 2000
	LargeRepoFileCount  = 5000
)

// AdaptiveDepth returns a safe tree depth based on repository size.
// Unknown file count (<=0) defaults to a conservative depth.
func AdaptiveDepth(fileCount int) int {
	if fileCount <= 0 {
		return 2
	}
	if fileCount > LargeRepoFileCount {
		return 2
	}
	if fileCount > MediumRepoFileCount {
		return 3
	}
	return 4
}
