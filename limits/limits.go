package limits

// Context output budgets for hook and MCP text responses.
const (
	MaxContextOutputBytes = 60000 // ~15k tokens, <10% of a 200k context window
)

// Watch daemon retention limits to keep long-running sessions bounded.
const (
	MaxDaemonEvents      = 1000
	MaxStateRecentEvents = 50

	MaxEventLogBytes     = 512 * 1024 // trim events.log when it exceeds 512KB
	EventLogTrimToBytes  = 384 * 1024 // keep newest 384KB after trimming
	MaxEventLogReadBytes = 128 * 1024 // hooks only read the tail of large logs
)

// Repo-size thresholds used to scale expensive analysis work.
const (
	MediumRepoFileCount = 2000
	LargeRepoFileCount  = 5000

	// DefaultMaxScanFiles bounds the files a scan keeps and the size of repo
	// a dependency scan is attempted on. Measured on 2026-10-11: --deps on a
	// synthetic 60k-file TypeScript repo peaked at 421 MB RSS, so two
	// concurrent scans of 200k-file repos exhaust a laptop (#186). Raised
	// per run with --max-files or per repo with config max_files; a
	// negative value removes the bound.
	DefaultMaxScanFiles = 50000
)

// ScanFileBound resolves a configured bound: zero means the default,
// negative means unbounded.
func ScanFileBound(configured int) int {
	if configured == 0 {
		return DefaultMaxScanFiles
	}
	if configured < 0 {
		return 0
	}
	return configured
}

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
