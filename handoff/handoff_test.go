package handoff

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/watch"
)

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func TestBuildWriteRead(t *testing.T) {
	root := t.TempDir()

	runCmd(t, root, "git", "init")

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc A() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package main\n\nfunc B() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0644); err != nil {
		t.Fatal(err)
	}

	runCmd(t, root, "git", "add", ".")
	runCmd(t, root, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	// Local modification to show up in handoff changed files.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc A() int { return 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Noise file should not be included in changed files handoff output.
	if err := os.WriteFile(filepath.Join(root, "firebase-debug.log"), []byte("debug noise\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Binary noise file (no extension) should also be excluded.
	if err := os.WriteFile(filepath.Join(root, "codemap-dev"), []byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00}, 0755); err != nil {
		t.Fatal(err)
	}

	state := &watch.State{
		Importers: map[string][]string{
			"a.go": {"x.go", "y.go", "z.go"},
		},
		RecentEvents: []watch.Event{
			{
				Time:  time.Now().Add(-20 * time.Minute),
				Op:    "WRITE",
				Path:  "a.go",
				Delta: 3,
				IsHub: true,
			},
		},
	}

	artifact, err := Build(root, BuildOptions{
		BaseRef: "HEAD",
		Since:   time.Hour,
		State:   state,
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if artifact.SchemaVersion != SchemaVersion {
		t.Fatalf("expected schema version %d, got %d", SchemaVersion, artifact.SchemaVersion)
	}
	if !contains(artifact.ChangedFiles, "a.go") {
		t.Fatalf("expected changed file a.go in %+v", artifact.ChangedFiles)
	}
	if !contains(artifact.ChangedFiles, "go.mod") {
		t.Fatalf("expected config file go.mod in changed files: %+v", artifact.ChangedFiles)
	}
	if contains(artifact.ChangedFiles, "firebase-debug.log") {
		t.Fatalf("did not expect log noise file in changed files: %+v", artifact.ChangedFiles)
	}
	if contains(artifact.ChangedFiles, "codemap-dev") {
		t.Fatalf("did not expect binary noise file in changed files: %+v", artifact.ChangedFiles)
	}
	if len(artifact.RiskFiles) == 0 {
		t.Fatalf("expected risk files in artifact")
	}
	if artifact.RiskFiles[0].Path != "a.go" {
		t.Fatalf("expected first risk file to be a.go, got %s", artifact.RiskFiles[0].Path)
	}
	if artifact.PrefixHash == "" || artifact.DeltaHash == "" || artifact.CombinedHash == "" {
		t.Fatalf("expected non-empty hashes, got prefix=%q delta=%q combined=%q", artifact.PrefixHash, artifact.DeltaHash, artifact.CombinedHash)
	}
	if len(artifact.Prefix.Hubs) == 0 {
		t.Fatalf("expected prefix hubs to be populated")
	}
	if len(artifact.Delta.Changed) == 0 {
		t.Fatalf("expected delta changed stubs")
	}

	if err := WriteLatest(root, artifact); err != nil {
		t.Fatalf("WriteLatest failed: %v", err)
	}
	if _, err := os.Stat(PrefixPath(root)); err != nil {
		t.Fatalf("expected prefix snapshot file: %v", err)
	}
	if _, err := os.Stat(DeltaPath(root)); err != nil {
		t.Fatalf("expected delta snapshot file: %v", err)
	}
	if _, err := os.Stat(MetricsPath(root)); err != nil {
		t.Fatalf("expected metrics log file: %v", err)
	}

	// Validate split snapshots are parseable JSON.
	var prefix PrefixSnapshot
	if data, err := os.ReadFile(PrefixPath(root)); err != nil {
		t.Fatalf("read prefix snapshot failed: %v", err)
	} else if err := json.Unmarshal(data, &prefix); err != nil {
		t.Fatalf("parse prefix snapshot failed: %v", err)
	}
	if len(prefix.Hubs) == 0 {
		t.Fatalf("expected hubs in persisted prefix snapshot")
	}

	readBack, err := ReadLatest(root)
	if err != nil {
		t.Fatalf("ReadLatest failed: %v", err)
	}
	if readBack == nil {
		t.Fatalf("expected artifact from ReadLatest")
	}
	if !contains(readBack.ChangedFiles, "a.go") {
		t.Fatalf("expected read-back changed file a.go in %+v", readBack.ChangedFiles)
	}
	if len(readBack.Delta.Changed) == 0 {
		t.Fatalf("expected read-back delta stubs")
	}
	if readBack.Metrics.TotalBytes == 0 {
		t.Fatalf("expected cache metrics to be populated")
	}
}

func TestBuildReturnsNonNilSlicesWithoutState(t *testing.T) {
	root := t.TempDir()
	runCmd(t, root, "git", "init")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, root, "git", "add", ".")
	runCmd(t, root, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	artifact, err := Build(root, BuildOptions{
		BaseRef: "HEAD",
		Since:   time.Hour,
		State:   nil,
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if artifact.ChangedFiles == nil {
		t.Fatal("ChangedFiles should be non-nil")
	}
	if artifact.RiskFiles == nil {
		t.Fatal("RiskFiles should be non-nil")
	}
	if artifact.RecentEvents == nil {
		t.Fatal("RecentEvents should be non-nil")
	}
	if artifact.NextSteps == nil {
		t.Fatal("NextSteps should be non-nil")
	}
	if artifact.OpenQuestions == nil {
		t.Fatal("OpenQuestions should be non-nil")
	}
	if artifact.Prefix.FileCount == 0 {
		t.Fatal("Prefix.FileCount should be populated when daemon state is unavailable")
	}
}

func TestReadLatestMissing(t *testing.T) {
	root := t.TempDir()
	got, err := ReadLatest(root)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil artifact when file missing")
	}
}

func TestRenderMarkdown(t *testing.T) {
	a := &Artifact{
		Branch:      "feature/test",
		BaseRef:     "main",
		GeneratedAt: time.Now(),
		PrefixHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DeltaHash:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Metrics:     CacheMetrics{ReuseRatio: 0.5, UnchangedBytes: 50, TotalBytes: 100},
		Delta: DeltaSnapshot{
			Changed: []FileStub{{Path: "a.go", Status: "modified"}},
			RiskFiles: []RiskFile{
				{Path: "a.go", Importers: 3, IsHub: true},
			},
			RecentEvents: []EventSummary{
				{Time: time.Now(), Op: "WRITE", Path: "a.go", Delta: 2},
			},
			NextSteps: []string{"Run tests"},
		},
	}

	md := RenderMarkdown(a)
	if !strings.Contains(md, "Handoff") || !strings.Contains(md, "a.go") || !strings.Contains(md, "Prefix (Stable Context)") {
		t.Fatalf("markdown render missing expected content: %s", md)
	}
	if strings.Contains(md, "Prefix hash:") || strings.Contains(md, "Cache reuse:") {
		t.Fatalf("markdown output should hide cache telemetry details: %s", md)
	}
}

func TestRenderCompactDeterministic(t *testing.T) {
	base := &Artifact{
		Branch:      "feature/test",
		BaseRef:     "main",
		GeneratedAt: time.Now(),
		Delta: DeltaSnapshot{
			Changed: []FileStub{{Path: "a.go", Status: "modified"}},
		},
	}
	other := *base
	other.GeneratedAt = base.GeneratedAt.Add(45 * time.Minute)

	out1 := RenderCompact(base, 5)
	out2 := RenderCompact(&other, 5)
	if out1 != out2 {
		t.Fatalf("compact output should be deterministic across generated_at changes:\n%s\n---\n%s", out1, out2)
	}
}

func TestBuildFileDetail(t *testing.T) {
	root := t.TempDir()
	runCmd(t, root, "git", "init")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, root, "git", "add", ".")
	runCmd(t, root, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\n// changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	state := &watch.State{
		Importers: map[string][]string{
			"a.go": {"x.go", "y.go", "z.go"},
		},
		Imports: map[string][]string{
			"a.go": {"dep.go"},
		},
		RecentEvents: []watch.Event{
			{Time: time.Now(), Op: "WRITE", Path: "a.go", Delta: 2, IsHub: true},
		},
	}

	artifact, err := Build(root, BuildOptions{BaseRef: "HEAD", State: state})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	detail, err := BuildFileDetail(root, artifact, "a.go", state)
	if err != nil {
		t.Fatalf("BuildFileDetail failed: %v", err)
	}
	if detail.Path != "a.go" {
		t.Fatalf("expected path a.go, got %s", detail.Path)
	}
	if !detail.IsHub {
		t.Fatalf("expected a.go to be marked as hub detail")
	}
	if len(detail.Importers) != 3 {
		t.Fatalf("expected 3 importers, got %d", len(detail.Importers))
	}
}

func TestMetricsLogCapped(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(MetricsPath(root)), 0755); err != nil {
		t.Fatalf("mkdir .codemap failed: %v", err)
	}

	artifact := &Artifact{
		GeneratedAt:  time.Now(),
		Branch:       "feature/test",
		BaseRef:      "main",
		PrefixHash:   "p",
		DeltaHash:    "d",
		CombinedHash: "c",
	}

	for i := 0; i < maxMetricsLines+50; i++ {
		if err := appendMetrics(root, artifact); err != nil {
			t.Fatalf("appendMetrics failed: %v", err)
		}
	}

	data, err := os.ReadFile(MetricsPath(root))
	if err != nil {
		t.Fatalf("read metrics failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != maxMetricsLines {
		t.Fatalf("expected %d metrics lines after cap, got %d", maxMetricsLines, len(lines))
	}
}
