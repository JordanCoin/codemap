package limits

// Context output budgets for hook and MCP text responses.
const (
	MaxContextOutputBytes = 60000 // ~15k tokens, <10% of a 200k context window
)

// Watch daemon retention limits to keep long-running sessions bounded.
const (
	MaxDaemonEvents      = 1000
	MaxStateRecentEvents = 50

	MaxEventLogBytes     = 512 * 1024 // rotate when events.log exceeds 512KB
	EventLogTrimToBytes  = 384 * 1024 // keep newest 384KB after rotation
	MaxEventLogReadBytes = 128 * 1024 // hooks only read the tail of large logs
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
