package handoff

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codemap/scanner"
)

type cancelAfterHandoffChecks struct {
	context.Context
	remaining int
}

func (c *cancelAfterHandoffChecks) Err() error {
	if c.remaining <= 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func TestBuildContextPreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildContext(ctx, t.TempDir(), BuildOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildContext error = %v, want context.Canceled", err)
	}
}

func TestBuildContextTerminatesGitSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "started")
	fakeGit := filepath.Join(binDir, "git")
	script := `#!/bin/sh
if [ "$1" = "rev-parse" ]; then
  echo feature/test
  exit 0
fi
printf '%s\n' "$$" > "$CODEMAP_HANDOFF_GIT_MARKER"
exec sleep 10
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMAP_HANDOFF_GIT_MARKER", marker)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := BuildContext(ctx, root, BuildOptions{BaseRef: "main"})
		done <- err
	}()
	var blockerPID string
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(marker); err == nil && len(data) > 0 {
			blockerPID = strings.TrimSpace(string(data))
			break
		}
		select {
		case err := <-done:
			t.Fatalf("BuildContext exited before fake handoff git subprocess started: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("fake handoff git subprocess did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("BuildContext error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BuildContext did not terminate cancelled git subprocess")
	}
	if err := exec.Command("/bin/kill", "-0", blockerPID).Run(); err == nil {
		t.Fatalf("cancelled fake handoff git left blocker process %s running", blockerPID)
	}
}

func TestBuildFileDetailContextPreCancellation(t *testing.T) {
	artifact := &Artifact{Delta: DeltaSnapshot{Changed: []FileStub{{Path: "main.go"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildFileDetailContext(ctx, t.TempDir(), artifact, "main.go", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildFileDetailContext error = %v, want context.Canceled", err)
	}

	inFlight := &cancelAfterHandoffChecks{Context: context.Background(), remaining: 1}
	if _, err := BuildFileDetailContext(inFlight, t.TempDir(), artifact, "main.go", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight BuildFileDetailContext error = %v, want context.Canceled", err)
	}
}

func TestFileSHA256ContextInFlightCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.go")
	if err := os.WriteFile(path, make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &cancelAfterHandoffChecks{Context: context.Background(), remaining: 1}
	if _, err := fileSHA256Context(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("fileSHA256Context error = %v, want context.Canceled", err)
	}
}

func TestBuildAndFileDetailPreserveBestEffortScanFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission fixture and shell script are Unix-specific")
	}

	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "main.go"), []byte("package blocked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := scanner.ScanConfiguredFiles(context.Background(), root, scanner.NewGitIgnoreCache(root)); err == nil {
		t.Skip("fixture did not produce a scanner failure")
	}

	binDir := t.TempDir()
	fakeGit := filepath.Join(binDir, "git")
	script := `#!/bin/sh
if [ "$1" = "rev-parse" ]; then
  echo feature/test
fi
exit 0
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	t.Run("Build", func(t *testing.T) {
		artifact, err := BuildContext(context.Background(), root, BuildOptions{BaseRef: "HEAD"})
		if err != nil {
			t.Fatalf("ordinary scanner failure escaped BuildContext: %v", err)
		}
		if artifact == nil || artifact.Prefix.FileCount != 0 {
			t.Fatalf("BuildContext artifact = %#v, want zero-count best-effort artifact", artifact)
		}
	})

	t.Run("BuildFileDetail", func(t *testing.T) {
		artifact := &Artifact{Delta: DeltaSnapshot{Changed: []FileStub{{Path: "blocked/main.go"}}}}
		detail, err := BuildFileDetailContext(context.Background(), root, artifact, "blocked/main.go", nil)
		if err != nil {
			t.Fatalf("ordinary scanner failure escaped BuildFileDetailContext: %v", err)
		}
		if detail == nil || len(detail.Importers) != 0 || len(detail.Imports) != 0 {
			t.Fatalf("BuildFileDetailContext detail = %#v, want empty best-effort dependency context", detail)
		}
	})
}
