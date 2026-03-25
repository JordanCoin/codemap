package cmd

import (
	"testing"

	"codemap/config"
)

func TestClassifyIntent_Categories(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		wantCat string
	}{
		{"refactor keyword", "refactor the scanner package", "refactor"},
		{"rename keyword", "rename this function to something better", "refactor"},
		{"cleanup keyword", "clean up the old auth code", "refactor"},
		{"fix keyword", "fix the broken test in hooks", "bugfix"},
		{"bug keyword", "there's a bug in the daemon", "bugfix"},
		{"error keyword", "getting an error when I run this", "bugfix"},
		{"add keyword", "add a new MCP tool for skills", "feature"},
		{"implement keyword", "implement working-set tracking", "feature"},
		{"create keyword", "create a new subcommand", "feature"},
		{"how does keyword", "how does the handoff system work", "explore"},
		{"where is keyword", "where is the hub detection logic", "explore"},
		{"test keyword", "add test coverage for the intent system", "test"},
		{"coverage keyword", "what's the coverage on this package", "test"},
		{"document keyword", "document the new API", "docs"},
		{"readme keyword", "update the readme with new features", "docs"},
		{"empty prompt defaults", "", "feature"},
		{"no signals defaults", "hello world", "feature"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := classifyIntent(tt.prompt, nil, nil, config.ProjectConfig{})
			if intent.Category != tt.wantCat {
				t.Errorf("classifyIntent(%q).Category = %q, want %q", tt.prompt, intent.Category, tt.wantCat)
			}
		})
	}
}

func TestClassifyIntent_Confidence(t *testing.T) {
	// Pure refactor prompt should have high confidence
	intent := classifyIntent("refactor the scanner to split it into modules", nil, nil, config.ProjectConfig{})
	if intent.Confidence < 0.5 {
		t.Errorf("expected high confidence for pure refactor, got %f", intent.Confidence)
	}

	// Mixed prompt should have lower confidence
	mixed := classifyIntent("fix the bug and refactor the handler", nil, nil, config.ProjectConfig{})
	if mixed.Confidence >= 1.0 {
		t.Errorf("expected lower confidence for mixed intent, got %f", mixed.Confidence)
	}
}

func TestClassifyIntent_TieBreaking(t *testing.T) {
	// "failing test" matches both bugfix ("failing") and test ("test") —
	// result must be deterministic across runs (categoryDefs order wins)
	results := make(map[string]int)
	for i := 0; i < 20; i++ {
		intent := classifyIntent("failing test in hooks.go", nil, nil, config.ProjectConfig{})
		results[intent.Category]++
	}
	if len(results) != 1 {
		t.Errorf("expected deterministic category across 20 runs, got %v", results)
	}
}

func TestComputeScope(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{"no files", nil, "unknown"},
		{"single file", []string{"cmd/hooks.go"}, "single-file"},
		{"same package", []string{"cmd/hooks.go", "cmd/intent.go"}, "package"},
		{"cross-cutting", []string{"cmd/hooks.go", "watch/daemon.go", "mcp/main.go"}, "cross-cutting"},
		{"root files same package", []string{"main.go", "helpers.go"}, "package"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeScope(tt.files)
			if got != tt.want {
				t.Errorf("computeScope(%v) = %q, want %q", tt.files, got, tt.want)
			}
		})
	}
}

func TestAnalyzeRisk_NoHubs(t *testing.T) {
	info := &hubInfo{
		Importers: map[string][]string{
			"main.go": {"cmd/hooks.go"},
		},
	}

	risk, suggestions := analyzeRisk([]string{"main.go"}, info, "feature")
	if risk != "low" {
		t.Errorf("expected low risk for non-hub file, got %q", risk)
	}
	// Should not have review-hub suggestions for non-hub files
	for _, s := range suggestions {
		if s.Type == "review-hub" {
			t.Error("unexpected review-hub suggestion for non-hub file")
		}
	}
}

func TestAnalyzeRisk_SingleHub(t *testing.T) {
	info := &hubInfo{
		Importers: map[string][]string{
			"scanner/types.go": {"a.go", "b.go", "c.go", "d.go", "e.go"},
		},
	}

	risk, suggestions := analyzeRisk([]string{"scanner/types.go"}, info, "refactor")
	if risk != "medium" {
		t.Errorf("expected medium risk for single hub, got %q", risk)
	}

	// Should have review-hub and check-deps suggestions
	hasReviewHub := false
	hasCheckDeps := false
	for _, s := range suggestions {
		if s.Type == "review-hub" {
			hasReviewHub = true
		}
		if s.Type == "check-deps" {
			hasCheckDeps = true
		}
	}
	if !hasReviewHub {
		t.Error("expected review-hub suggestion for hub file")
	}
	if !hasCheckDeps {
		t.Error("expected check-deps suggestion for refactor on hub")
	}
}

func TestAnalyzeRisk_HighRisk(t *testing.T) {
	info := &hubInfo{
		Importers: map[string][]string{
			"scanner/types.go": {"a.go", "b.go", "c.go", "d.go", "e.go"},
			"config/config.go": {"f.go", "g.go", "h.go"},
		},
	}

	risk, _ := analyzeRisk([]string{"scanner/types.go", "config/config.go"}, info, "refactor")
	if risk != "high" {
		t.Errorf("expected high risk for multiple hubs, got %q", risk)
	}
}

func TestAnalyzeRisk_HighImporterCount(t *testing.T) {
	importers := make([]string, 10)
	for i := range importers {
		importers[i] = "dep" + string(rune('a'+i)) + ".go"
	}
	info := &hubInfo{
		Importers: map[string][]string{
			"core/types.go": importers,
		},
	}

	risk, _ := analyzeRisk([]string{"core/types.go"}, info, "feature")
	if risk != "high" {
		t.Errorf("expected high risk for file with 10 importers, got %q", risk)
	}
}

func TestAnalyzeRisk_BugfixSuggestions(t *testing.T) {
	info := &hubInfo{
		Importers: map[string][]string{},
	}

	_, suggestions := analyzeRisk([]string{"cmd/hooks.go"}, info, "bugfix")
	hasRunTests := false
	for _, s := range suggestions {
		if s.Type == "run-tests" {
			hasRunTests = true
		}
	}
	if !hasRunTests {
		t.Error("expected run-tests suggestion for bugfix category")
	}
}

func TestAnalyzeRisk_SuggestionsCapped(t *testing.T) {
	// Create many hub files to generate lots of suggestions
	importers := map[string][]string{}
	files := make([]string, 10)
	for i := range files {
		f := "pkg" + string(rune('a'+i)) + "/types.go"
		files[i] = f
		importers[f] = []string{"a.go", "b.go", "c.go"}
	}
	info := &hubInfo{Importers: importers}

	_, suggestions := analyzeRisk(files, info, "refactor")
	if len(suggestions) > 5 {
		t.Errorf("suggestions should be capped at 5, got %d", len(suggestions))
	}
}

func TestClassifyIntent_SubsystemMatching(t *testing.T) {
	cfg := config.ProjectConfig{
		Routing: config.RoutingConfig{
			Retrieval: config.RetrievalConfig{Strategy: "keyword", TopK: 3},
			Subsystems: []config.Subsystem{
				{ID: "watching", Keywords: []string{"hook", "daemon", "events"}},
				{ID: "scanning", Keywords: []string{"ast-grep", "dependency", "imports"}},
			},
		},
	}

	intent := classifyIntent("fix the hook timeout in the daemon", nil, nil, cfg)
	if len(intent.Subsystems) == 0 {
		t.Error("expected subsystem match for 'hook' and 'daemon' keywords")
	}
	found := false
	for _, s := range intent.Subsystems {
		if s == "watching" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'watching' subsystem in %v", intent.Subsystems)
	}
}

func TestClassifyIntent_NilInfoSafe(t *testing.T) {
	// Should not panic with nil hubInfo
	intent := classifyIntent("refactor everything", []string{"main.go"}, nil, config.ProjectConfig{})
	if intent.RiskLevel != "low" {
		t.Errorf("expected low risk with nil info, got %q", intent.RiskLevel)
	}
	if intent.Category != "refactor" {
		t.Errorf("expected refactor category, got %q", intent.Category)
	}
}

func TestFormatImporterReason(t *testing.T) {
	reason := formatImporterReason("scanner/types.go", 5)
	if reason == "" {
		t.Error("expected non-empty reason")
	}
	if !contains(reason, "scanner/types.go") {
		t.Error("reason should mention the file")
	}
	if !contains(reason, "5") {
		t.Error("reason should mention the count")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
