package handoff

import (
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

	runCmd(t, root, "git", "add", ".")
	runCmd(t, root, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	// Local modification to show up in handoff changed files.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc A() int { return 1 }\n"), 0644); err != nil {
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
	if len(artifact.RiskFiles) == 0 {
		t.Fatalf("expected risk files in artifact")
	}
	if artifact.RiskFiles[0].Path != "a.go" {
		t.Fatalf("expected first risk file to be a.go, got %s", artifact.RiskFiles[0].Path)
	}

	if err := WriteLatest(root, artifact); err != nil {
		t.Fatalf("WriteLatest failed: %v", err)
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
		Branch:       "feature/test",
		BaseRef:      "main",
		GeneratedAt:  time.Now(),
		ChangedFiles: []string{"a.go"},
		RiskFiles: []RiskFile{
			{Path: "a.go", Importers: 3, IsHub: true},
		},
		RecentEvents: []EventSummary{
			{Time: time.Now(), Op: "WRITE", Path: "a.go", Delta: 2},
		},
		NextSteps: []string{"Run tests"},
	}

	md := RenderMarkdown(a)
	if !strings.Contains(md, "Handoff") || !strings.Contains(md, "a.go") {
		t.Fatalf("markdown render missing expected content: %s", md)
	}
}
