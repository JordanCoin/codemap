package projectpath

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelectLinkedWorktreeUsesPrimarySetupAndLocalRuntime(t *testing.T) {
	ResetSetupRoot()
	t.Cleanup(ResetSetupRoot)

	primary, linked := makeLinkedWorktreeFixture(t, true)

	got, err := Select(linked)
	if err != nil {
		t.Fatalf("Select() error: %v", err)
	}
	if got.ProjectRoot != linked || got.SetupRoot != primary || got.RuntimeRoot != linked || got.Source != SourceLinkedWorktree {
		t.Fatalf("Select() = %#v, want project/runtime %q, setup %q, source %q", got, linked, primary, SourceLinkedWorktree)
	}
}

func TestSelectDiscoversLinkedWorktreeFromNestedProjectPath(t *testing.T) {
	ResetSetupRoot()
	t.Cleanup(ResetSetupRoot)

	primary, linked := makeLinkedWorktreeFixture(t, true)
	nested := filepath.Join(linked, "pkg", "feature")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Select(nested)
	if err != nil {
		t.Fatalf("Select() error: %v", err)
	}
	if got.ProjectRoot != linked || got.SetupRoot != primary || got.RuntimeRoot != linked || got.Source != SourceLinkedWorktree {
		t.Fatalf("Select() = %#v, want project/runtime %q, setup %q, source %q", got, linked, primary, SourceLinkedWorktree)
	}
}

func TestParseMetadataPathPreservesWindowsGitdir(t *testing.T) {
	want := `C:\Users\agent\repo\.git\worktrees\feature`
	got, err := parseMetadataPath([]byte("gitdir: "+want+"\r\n"), "gitdir:")
	if err != nil {
		t.Fatalf("parseMetadataPath() error: %v", err)
	}
	if got != want {
		t.Fatalf("parseMetadataPath() = %q, want %q", got, want)
	}
}

func TestSelectPrecedenceFallbackAndSafety(t *testing.T) {
	t.Run("explicit override wins linked discovery", func(t *testing.T) {
		primary, linked := makeLinkedWorktreeFixture(t, true)
		explicit := t.TempDir()
		SetSetupRoot(explicit)
		t.Cleanup(ResetSetupRoot)

		got, err := Select(linked)
		if err != nil {
			t.Fatalf("Select() error: %v", err)
		}
		if got.ProjectRoot != linked || got.SetupRoot != explicit || got.RuntimeRoot != explicit || got.Source != SourceExplicit {
			t.Fatalf("Select() = %#v, want explicit root %q (primary was %q)", got, explicit, primary)
		}
	})

	t.Run("linked worktree without primary setup remains project local", func(t *testing.T) {
		ResetSetupRoot()
		_, linked := makeLinkedWorktreeFixture(t, false)
		got, err := Select(linked)
		if err != nil {
			t.Fatalf("Select() error: %v", err)
		}
		if got.SetupRoot != linked || got.RuntimeRoot != linked || got.Source != SourceProject {
			t.Fatalf("Select() = %#v, want project-local selection", got)
		}
	})

	t.Run("primary worktree remains project local", func(t *testing.T) {
		ResetSetupRoot()
		primary := t.TempDir()
		if err := os.MkdirAll(filepath.Join(primary, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(primary, ".codemap"), 0o755); err != nil {
			t.Fatal(err)
		}
		primary, _ = filepath.EvalSymlinks(primary)
		got, err := Select(primary)
		if err != nil {
			t.Fatalf("Select() error: %v", err)
		}
		if got.SetupRoot != primary || got.RuntimeRoot != primary || got.Source != SourceProject {
			t.Fatalf("Select() = %#v, want primary project-local selection", got)
		}
	})

	t.Run("one process selects each linked project independently", func(t *testing.T) {
		ResetSetupRoot()
		primaryA, linkedA := makeLinkedWorktreeFixture(t, true)
		primaryB, linkedB := makeLinkedWorktreeFixture(t, true)
		gotA, err := Select(linkedA)
		if err != nil {
			t.Fatalf("Select(A) error: %v", err)
		}
		gotB, err := Select(linkedB)
		if err != nil {
			t.Fatalf("Select(B) error: %v", err)
		}
		if gotA.SetupRoot != primaryA || gotB.SetupRoot != primaryB || gotA.SetupRoot == gotB.SetupRoot {
			t.Fatalf("Select() roots = %q, %q; want independent %q, %q", gotA.SetupRoot, gotB.SetupRoot, primaryA, primaryB)
		}
	})

	t.Run("malformed gitfile fails closed", func(t *testing.T) {
		ResetSetupRoot()
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not a gitdir\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Select(root); err == nil || !strings.Contains(err.Error(), "expected gitdir:") {
			t.Fatalf("Select() error = %v, want bounded malformed-gitfile error", err)
		}
	})

	t.Run("symlinked primary storage fails closed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		ResetSetupRoot()
		primary, linked := makeLinkedWorktreeFixture(t, false)
		if err := os.Symlink(t.TempDir(), filepath.Join(primary, ".codemap")); err != nil {
			t.Fatal(err)
		}
		if _, err := Select(linked); err == nil || !strings.Contains(err.Error(), "unsafe Codemap storage") {
			t.Fatalf("Select() error = %v, want unsafe-storage error", err)
		}
	})

	t.Run("symlinked linked runtime storage fails closed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		ResetSetupRoot()
		_, linked := makeLinkedWorktreeFixture(t, true)
		if err := os.Symlink(t.TempDir(), filepath.Join(linked, ".codemap")); err != nil {
			t.Fatal(err)
		}
		if _, err := Select(linked); err == nil || !strings.Contains(err.Error(), "unsafe Codemap storage") {
			t.Fatalf("Select() error = %v, want unsafe runtime-storage error", err)
		}
	})

	t.Run("symlinked linked runtime without primary setup fails closed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		ResetSetupRoot()
		_, linked := makeLinkedWorktreeFixture(t, false)
		if err := os.Symlink(t.TempDir(), filepath.Join(linked, ".codemap")); err != nil {
			t.Fatal(err)
		}
		if _, err := Select(linked); err == nil || !strings.Contains(err.Error(), "unsafe Codemap storage") {
			t.Fatalf("Select() error = %v, want unsafe project-local storage error", err)
		}
	})

	t.Run("symlinked ordinary project storage fails closed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		ResetSetupRoot()
		project := t.TempDir()
		if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(project, ".codemap")); err != nil {
			t.Fatal(err)
		}
		if _, err := Select(project); err == nil || !strings.Contains(err.Error(), "unsafe Codemap storage") {
			t.Fatalf("Select() error = %v, want unsafe project-local storage error", err)
		}
	})
}

func TestRuntimeCodemapDirSeparatesAutomaticAndExplicitStorage(t *testing.T) {
	ResetSetupRoot()
	primary, linked := makeLinkedWorktreeFixture(t, true)
	if got, want := CodemapDir(linked), filepath.Join(primary, ".codemap"); got != want {
		t.Fatalf("CodemapDir() = %q, want policy dir %q", got, want)
	}
	if got, want := RuntimeCodemapDir(linked), filepath.Join(linked, ".codemap"); got != want {
		t.Fatalf("RuntimeCodemapDir() = %q, want local runtime dir %q", got, want)
	}

	explicit := t.TempDir()
	SetSetupRoot(explicit)
	t.Cleanup(ResetSetupRoot)
	digest := sha256.Sum256([]byte(linked))
	want := filepath.Join(explicit, ".codemap", "runtime", hex.EncodeToString(digest[:]))
	if got := RuntimeCodemapDir(linked); got != want {
		t.Fatalf("explicit RuntimeCodemapDir() = %q, want project namespace %q", got, want)
	}
}

func TestExplicitSetupRuntimeNamespacesAreDeterministicAndDistinct(t *testing.T) {
	setup := t.TempDir()
	projectA := makeProjectFixture(t)
	projectB := makeProjectFixture(t)
	SetSetupRoot(setup)
	t.Cleanup(ResetSetupRoot)

	a, err := SelectRuntime(projectA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SelectRuntime(projectB)
	if err != nil {
		t.Fatal(err)
	}
	if a.PolicyDir != b.PolicyDir || a.PolicyDir != filepath.Join(setup, ".codemap") {
		t.Fatalf("policy dirs = %q, %q; want shared setup policy", a.PolicyDir, b.PolicyDir)
	}
	if a.RuntimeDir == b.RuntimeDir {
		t.Fatalf("runtime dirs both %q; want project isolation", a.RuntimeDir)
	}
	if a.LegacyDir != a.PolicyDir || b.LegacyDir != b.PolicyDir {
		t.Fatalf("legacy dirs = %q, %q; want shared old location", a.LegacyDir, b.LegacyDir)
	}
	assertProjectMarker(t, a.RuntimeDir, a.ProjectRoot)
	assertProjectMarker(t, b.RuntimeDir, b.ProjectRoot)

	again, err := SelectRuntime(projectA)
	if err != nil || again != a {
		t.Fatalf("SelectRuntime() repeat = %#v, %v; want %#v", again, err, a)
	}
}

func TestExplicitSetupRuntimeNamespaceCanonicalizesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks may require elevated privileges")
	}
	setup := t.TempDir()
	project := makeProjectFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	SetSetupRoot(setup)
	t.Cleanup(ResetSetupRoot)

	direct, err := SelectRuntime(project)
	if err != nil {
		t.Fatal(err)
	}
	viaAlias, err := SelectRuntime(alias)
	if err != nil {
		t.Fatal(err)
	}
	if direct != viaAlias {
		t.Fatalf("direct = %#v, alias = %#v", direct, viaAlias)
	}
}

func TestExplicitSetupRuntimeRejectsMarkerMismatch(t *testing.T) {
	setup := t.TempDir()
	project := makeProjectFixture(t)
	SetSetupRoot(setup)
	t.Cleanup(ResetSetupRoot)

	selection, err := SelectRuntime(project)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(selection.RuntimeDir, "project.json")
	if err := os.WriteFile(marker, []byte(`{"canonical_root":"/different/project"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectRuntime(project); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("SelectRuntime() error = %v, want identity mismatch", err)
	}
}

func assertProjectMarker(t *testing.T, runtimeDir, wantRoot string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runtimeDir, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		CanonicalRoot string `json:"canonical_root"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.CanonicalRoot != wantRoot {
		t.Fatalf("marker root = %q, want %q", marker.CanonicalRoot, wantRoot)
	}
}

func makeProjectFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSelectRejectsNonstandardWorktreeMetadataDirectory(t *testing.T) {
	ResetSetupRoot()
	t.Cleanup(ResetSetupRoot)
	primary := t.TempDir()
	worktreesDir := filepath.Join(primary, ".git", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(primary, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreesDir, "commondir"), []byte("..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+worktreesDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Select(linked); err == nil || !strings.Contains(err.Error(), "standard worktree metadata") {
		t.Fatalf("Select() error = %v, want nonstandard-metadata rejection", err)
	}
}

func TestSelectRejectsWorktreeMetadataWithoutCommondir(t *testing.T) {
	ResetSetupRoot()
	t.Cleanup(ResetSetupRoot)
	primary := t.TempDir()
	gitDir := filepath.Join(primary, ".git", "worktrees", "broken")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Select(linked); err == nil || !strings.Contains(err.Error(), "commondir") {
		t.Fatalf("Select() error = %v, want missing-commondir rejection", err)
	}
}

func TestSelectReportsInaccessiblePrimarySetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions are platform-specific")
	}
	ResetSetupRoot()
	t.Cleanup(ResetSetupRoot)
	primary, linked := makeLinkedWorktreeFixture(t, true)
	setupDir := filepath.Join(primary, ".codemap")
	if err := os.Chmod(setupDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(setupDir, 0o755) })

	if _, err := Select(linked); err == nil || !strings.Contains(err.Error(), linked) || !strings.Contains(err.Error(), setupDir) {
		t.Fatalf("Select() error = %v, want analyzed project and inaccessible setup paths", err)
	}
}

func makeLinkedWorktreeFixture(t *testing.T, withSetup bool) (primary, linked string) {
	t.Helper()
	primary = t.TempDir()
	gitDir := filepath.Join(primary, ".git", "worktrees", "agent")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if withSetup {
		if err := os.MkdirAll(filepath.Join(primary, ".codemap"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	linked = t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked, _ = filepath.EvalSymlinks(linked)
	primary, _ = filepath.EvalSymlinks(primary)
	return primary, linked
}

func TestSetupRoot(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "project")
	setupRoot := filepath.Join(t.TempDir(), "setup")

	t.Run("defaults to project root", func(t *testing.T) {
		ResetSetupRoot()
		t.Cleanup(ResetSetupRoot)
		t.Setenv("CODEMAP_SETUP_ROOT", setupRoot)
		if got := SetupRoot(projectRoot); got != projectRoot {
			t.Fatalf("SetupRoot() = %q, want %q", got, projectRoot)
		}
		if got := ConfiguredSetupRoot(); got != "" {
			t.Fatalf("ConfiguredSetupRoot() = %q, want empty despite inherited environment", got)
		}
	})

	t.Run("uses configured setup root", func(t *testing.T) {
		SetSetupRoot(setupRoot)
		t.Cleanup(ResetSetupRoot)
		if got := SetupRoot(projectRoot); got != setupRoot {
			t.Fatalf("SetupRoot() = %q, want %q", got, setupRoot)
		}
		want := filepath.Join(setupRoot, ".codemap")
		if got := CodemapDir(projectRoot); got != want {
			t.Fatalf("CodemapDir() = %q, want %q", got, want)
		}
	})

	t.Run("clears configured setup root", func(t *testing.T) {
		SetSetupRoot(setupRoot)
		ResetSetupRoot()
		if got := SetupRoot(projectRoot); got != projectRoot {
			t.Fatalf("SetupRoot() = %q, want %q", got, projectRoot)
		}
	})
}

func TestPrependSetupRootArgs(t *testing.T) {
	SetSetupRoot("/setup/root")
	t.Cleanup(ResetSetupRoot)

	got := PrependSetupRootArgs("watch", "start", "/project")
	want := []string{"--setup-root", "/setup/root", "watch", "start", "/project"}
	if len(got) != len(want) {
		t.Fatalf("PrependSetupRootArgs() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PrependSetupRootArgs() = %#v, want %#v", got, want)
		}
	}
}
func TestRuntimeRootAndCheckedRuntimeCodemapDir(t *testing.T) {
	ResetSetupRoot()
	t.Cleanup(ResetSetupRoot)

	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := RuntimeRoot(root); got != canonicalRoot {
		t.Fatalf("RuntimeRoot() = %q, want canonical %q", got, canonicalRoot)
	}

	// Linked worktrees keep runtime state local to the worktree root.
	_, linked := makeLinkedWorktreeFixture(t, false)
	canonicalLinked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}
	if got := RuntimeRoot(linked); got != canonicalLinked {
		t.Fatalf("linked RuntimeRoot() = %q, want %q", got, canonicalLinked)
	}

	// The convenience form falls back to the cleaned input when selection fails.
	missing := filepath.Join(t.TempDir(), "missing")
	if got := RuntimeRoot(missing); got != filepath.Clean(missing) {
		t.Fatalf("RuntimeRoot() fallback = %q, want %q", got, filepath.Clean(missing))
	}
}

func TestProjectKeyScopesProjectsAndSharesRepoKey(t *testing.T) {
	a := filepath.Join(t.TempDir(), "projA")
	if err := os.MkdirAll(filepath.Join(a, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(t.TempDir(), "projB")
	if ProjectKey(a) == ProjectKey(b) {
		t.Fatal("distinct projects must not share a project key")
	}
	if ProjectKey(a) != ProjectKey(filepath.Join(a, "pkg", "sub")) {
		t.Fatal("a subdirectory must share its repo root's project key")
	}
}
