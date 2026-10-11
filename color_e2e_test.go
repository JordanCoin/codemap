package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPipedCodemap runs the built binary with stdout captured in a pipe (never a
// terminal), the way agents, hooks and shells pipes see it.
func runPipedCodemap(t *testing.T, dir string, env []string, args ...string) (string, string) {
	t.Helper()
	command := exec.Command(codemapTestBinaryPath, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("codemap %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func TestColorEndToEnd(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"src/main.go":  "package main\n",
		"src/util.go":  "package main\n",
		"README.md":    "# demo\n",
		"web/app.html": "<html></html>\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("piped output carries no ANSI escapes", func(t *testing.T) {
		out, _ := runPipedCodemap(t, repo, nil, ".")
		if strings.Contains(out, "\x1b[") {
			t.Fatalf("piped `codemap .` emitted ANSI escapes:\n%q", out)
		}
		for _, want := range []string{"src/", "main", "util", "app.html"} {
			if !strings.Contains(out, want) {
				t.Fatalf("piped output lost %q:\n%s", want, out)
			}
		}
	})

	t.Run("color=always restores ANSI escapes even with NO_COLOR", func(t *testing.T) {
		out, _ := runPipedCodemap(t, repo, []string{"NO_COLOR=1"}, "--color=always", ".")
		if !strings.Contains(out, "\x1b[1;34m  src/\x1b[0m") {
			t.Fatalf("`codemap --color=always .` lacks the blue directory escape:\n%q", out)
		}
	})

	t.Run("color=never and NO_COLOR are quiet", func(t *testing.T) {
		for _, tc := range []struct {
			env  []string
			args []string
		}{
			{nil, []string{"--color=never", "."}},
			{nil, []string{"--color", "never", "."}},
			{[]string{"NO_COLOR=1"}, []string{"."}},
			{[]string{"TERM=dumb"}, []string{"."}},
		} {
			out, _ := runPipedCodemap(t, repo, tc.env, tc.args...)
			if strings.Contains(out, "\x1b[") {
				t.Fatalf("codemap %v with env %v emitted ANSI escapes:\n%q", tc.args, tc.env, out)
			}
		}
	})

	t.Run("json is identical regardless of color mode", func(t *testing.T) {
		auto, _ := runPipedCodemap(t, repo, nil, "--json", ".")
		always, _ := runPipedCodemap(t, repo, nil, "--color=always", "--json", ".")
		never, _ := runPipedCodemap(t, repo, nil, "--color=never", "--json", ".")
		if auto != always || auto != never {
			t.Fatalf("--json output differs across colour modes:\nauto:\n%s\nalways:\n%s\nnever:\n%s", auto, always, never)
		}
		if strings.Contains(auto, "\x1b[") {
			t.Fatalf("--json output contains ANSI escapes:\n%q", auto)
		}
	})

	t.Run("unknown color mode is rejected", func(t *testing.T) {
		command := exec.Command(codemapTestBinaryPath, "--color=sometimes", ".")
		command.Dir = repo
		out, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("`codemap --color=sometimes` unexpectedly succeeded:\n%s", out)
		}
		if !strings.Contains(string(out), "--color: unknown mode") {
			t.Fatalf("unexpected error text:\n%s", out)
		}
	})
}
