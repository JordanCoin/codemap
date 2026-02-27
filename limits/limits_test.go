package limits

import (
	"strings"
	"testing"
)

// TestTruncateAtLineBoundary covers the core context-bloat guard used throughout
// the hook system. It must never let hook output exceed the budget.
func TestTruncateAtLineBoundary(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		maxBytes    int
		msg         string
		wantLen     int    // exact expected length (0 = skip check)
		wantSuffix  string // expected suffix after truncation
		wantUnchanged bool  // expect identical output (no truncation)
	}{
		{
			name:          "content within budget is unchanged",
			input:         "line1\nline2\nline3\n",
			maxBytes:      100,
			wantUnchanged: true,
		},
		{
			name:          "empty string is unchanged",
			input:         "",
			maxBytes:      100,
			wantUnchanged: true,
		},
		{
			name:          "maxBytes zero returns unchanged (guard clause)",
			input:         "some content",
			maxBytes:      0,
			wantUnchanged: true,
		},
		{
			name:          "maxBytes negative returns unchanged (guard clause)",
			input:         "some content",
			maxBytes:      -1,
			wantUnchanged: true,
		},
		{
			name:     "content exceeding budget is truncated",
			input:    strings.Repeat("x", 10000),
			maxBytes: 100,
			msg:      "\n... (truncated)\n",
		},
		{
			name:       "truncation appends custom message",
			input:      strings.Repeat("a", 5000),
			maxBytes:   1000,
			msg:        "\n\n... (custom truncation message)\n",
			wantSuffix: "\n\n... (custom truncation message)\n",
		},
		{
			name:       "truncation appends default message when msg is empty",
			input:      strings.Repeat("b", 5000),
			maxBytes:   1000,
			msg:        "",
			wantSuffix: "\n\n... (truncated)\n",
			// default message is 18 bytes; slack must cover it
		},
		{
			name: "truncation prefers clean line boundary",
			// Put a newline near the cutoff so truncation can split there
			input:    strings.Repeat("z", 800) + "\n" + strings.Repeat("z", 800),
			maxBytes: 1000,
			msg:      "...\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateAtLineBoundary(tt.input, tt.maxBytes, tt.msg)

			if tt.wantUnchanged {
				if got != tt.input {
					t.Errorf("expected unchanged output, got %q (len=%d)", got[:min(len(got), 50)], len(got))
				}
				return
			}

			// Output must fit within budget plus the appended message.
			// When msg is empty, the function substitutes the default 18-byte message.
			effectiveMsg := tt.msg
			if effectiveMsg == "" {
				effectiveMsg = "\n\n... (truncated)\n"
			}
			maxAllowed := tt.maxBytes + len(effectiveMsg) + 10 // small slack for line boundary
			if len(got) > maxAllowed {
				t.Errorf("output length %d exceeds budget %d + msg overhead", len(got), tt.maxBytes)
			}

			if tt.wantSuffix != "" && !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("expected suffix %q, got %q", tt.wantSuffix, got[max(0, len(got)-len(tt.wantSuffix)):])
			}
		})
	}
}

// TestAdaptiveDepth verifies that tree depth shrinks proportionally with repo size,
// preventing hook startup from injecting massive tree output into context.
func TestAdaptiveDepth(t *testing.T) {
	tests := []struct {
		name      string
		fileCount int
		wantDepth int
	}{
		{"unknown repo size defaults to shallow (0)", 0, 2},
		{"negative treated as unknown", -1, 2},
		{"small repo gets full depth", 100, 4},
		{"medium repo threshold (2001)", 2001, 3},
		{"large repo threshold (5001)", 5001, 2},
		{"exactly medium threshold (2000)", 2000, 4},
		{"exactly large threshold (5000)", 5000, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdaptiveDepth(tt.fileCount)
			if got != tt.wantDepth {
				t.Errorf("AdaptiveDepth(%d) = %d, want %d", tt.fileCount, got, tt.wantDepth)
			}
		})
	}
}

// TestHandoffBudgetForRepo verifies that handoff list sizes scale down for large
// repos to prevent context bloat in the session handoff output.
func TestHandoffBudgetForRepo(t *testing.T) {
	small := HandoffBudgetForRepo(100)
	medium := HandoffBudgetForRepo(MediumRepoFileCount + 1)
	large := HandoffBudgetForRepo(LargeRepoFileCount + 1)

	// Budgets should monotonically decrease as repo size grows.
	if small.MaxChanged <= medium.MaxChanged {
		t.Errorf("small repo should have larger MaxChanged than medium: %d vs %d",
			small.MaxChanged, medium.MaxChanged)
	}
	if medium.MaxChanged <= large.MaxChanged {
		t.Errorf("medium repo should have larger MaxChanged than large: %d vs %d",
			medium.MaxChanged, large.MaxChanged)
	}
	if small.MaxRisk <= medium.MaxRisk {
		t.Errorf("small repo should have larger MaxRisk than medium: %d vs %d",
			small.MaxRisk, medium.MaxRisk)
	}
	if medium.MaxRisk <= large.MaxRisk {
		t.Errorf("medium repo should have larger MaxRisk than large: %d vs %d",
			medium.MaxRisk, large.MaxRisk)
	}
	if small.MaxEvents <= large.MaxEvents {
		t.Errorf("small repo should have more MaxEvents than large: %d vs %d",
			small.MaxEvents, large.MaxEvents)
	}

	// Markdown and detail budgets are shared (not repo-scaled) — verify they're nonzero.
	for _, b := range []HandoffBudget{small, medium, large} {
		if b.MaxMarkdownBytes == 0 {
			t.Error("MaxMarkdownBytes must be nonzero")
		}
		if b.MaxDetailBytes == 0 {
			t.Error("MaxDetailBytes must be nonzero")
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
