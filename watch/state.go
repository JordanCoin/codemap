package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codemap/internal/projectpath"
	"codemap/internal/runtimefile"
)

// ErrForeignDaemonPID: the PID in watch.pid is alive but belongs to another
// process; callers treat it as nothing of ours and discard the pid file.
var ErrForeignDaemonPID = errors.New("watch.pid points to a live process that is not this repo's codemap watch daemon (stale or reused PID)")

// ErrDaemonOwnershipUnknown: the PID is alive but ownership can't be verified;
// refuse to kill and keep the pid file.
var ErrDaemonOwnershipUnknown = errors.New("could not verify that watch.pid belongs to this repo's codemap watch daemon; refusing to stop it")

var ErrDaemonExitTimeout = errors.New("watch daemon did not exit before transition deadline")

// canonicalRoot returns the same platform-normalized path identity used by
// daemon setup and event handling.
func canonicalRoot(root string) string {
	return projectpath.CanonicalPath(root)
}

// ReadState reads daemon state for hooks and returns nil when it is unavailable
// or stale without a running daemon.
func ReadState(root string) *State {
	active, err := ResolveActiveRuntime(root)
	if err != nil {
		return nil
	}
	stateFile := filepath.Join(active.Directory, "state.json")
	if err := requireRegularRuntimeFile(stateFile); err != nil {
		return nil
	}
	data, err := runtimefile.Read(stateFile)
	if err != nil {
		return nil
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}

	// Reject state owned by another project: a shared runtime dir (e.g. one
	// setup root serving several sandboxes) must never serve a different
	// project's daemon state as truth. Callers inside the daemon's project
	// (subdirectories) still see it.
	if state.Root != "" {
		caller := canonicalRoot(root)
		rel, err := filepath.Rel(state.Root, caller)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil
		}
	}

	// If state is stale, still allow it when daemon is alive.
	// This avoids expensive fallback scans during idle periods.
	if time.Since(state.UpdatedAt) > 30*time.Second && !IsRunning(root) {
		return nil
	}

	return &state
}

// WritePID writes the daemon PID to the project's runtime namespace.
func WritePID(root string) error {
	return WriteProcessPID(root, os.Getpid())
}

// WriteProcessPID publishes the already-started daemon PID in its namespace.
func WriteProcessPID(root string, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid daemon PID %d", pid)
	}
	runtimeDir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}
	pidFile := filepath.Join(runtimeDir, "watch.pid")
	return runtimefile.WriteAtomic(pidFile, []byte(fmt.Sprintf("%d", pid)), 0o644)
}

// ReadPID reads the daemon PID from the project's runtime namespace.
func ReadPID(root string) (int, error) {
	selection, err := projectpath.SelectRuntime(root)
	if err != nil {
		return 0, err
	}
	return readPIDAt(projectpath.ProjectRuntimeDir(selection.ProjectRoot))
}

// RemovePID removes the project's PID file.
func RemovePID(root string) {
	runtimeDir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		return
	}
	pidFile := filepath.Join(runtimeDir, "watch.pid")
	os.Remove(pidFile)
}

// daemonOwnership is the result of checking whether a specific PID is this
// repo's watch daemon. It distinguishes "confirmed not ours" from "could not
// determine" so callers never treat an unverifiable process as safe to discard.
type daemonOwnership int

const (
	ownershipUnknown daemonOwnership = iota // could not read the process command line
	ownershipOwned                          // command line matches this repo's watch daemon
	ownershipForeign                        // command line retrieved and does NOT match
)

// ActiveRuntime is the single ownership-checked location used by runtime consumers.
type ActiveRuntime struct {
	Directory     string
	CanonicalRoot string
	PID           int
	Legacy        bool
	StalePIDPath  string
}

// ResolveActiveRuntime selects the project namespace, or a positively owned
// live legacy daemon during forward migration.
func ResolveActiveRuntime(root string) (ActiveRuntime, error) {
	selection, err := projectpath.SelectRuntime(root)
	if err != nil {
		return ActiveRuntime{}, err
	}
	base := ActiveRuntime{Directory: projectpath.ProjectRuntimeDir(selection.ProjectRoot), CanonicalRoot: selection.ProjectRoot}
	var stalePath string
	for _, candidate := range []ActiveRuntime{
		base,
		// Keep the immediately-prior explicit setup-root layout readable during migration.
		{Directory: filepath.Join(selection.LegacyDir, "projects", projectpath.ProjectKey(selection.ProjectRoot)), CanonicalRoot: selection.ProjectRoot, Legacy: true},
		{Directory: selection.LegacyDir, CanonicalRoot: selection.ProjectRoot, Legacy: true},
	} {
		if candidate.Legacy && candidate.Directory == base.Directory {
			continue
		}
		pid, readErr := readPIDAt(candidate.Directory)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil || pid <= 0 {
			return ActiveRuntime{}, ErrDaemonOwnershipUnknown
		}
		if !processAlive(pid) {
			if candidate.Legacy {
				continue
			}
			stalePath = filepath.Join(candidate.Directory, "watch.pid")
			continue
		}
		switch daemonOwnershipForPID(selection.ProjectRoot, pid) {
		case ownershipOwned:
			candidate.PID = pid
			return candidate, nil
		case ownershipForeign:
			return ActiveRuntime{}, ErrForeignDaemonPID
		default:
			return ActiveRuntime{}, ErrDaemonOwnershipUnknown
		}
	}
	base.StalePIDPath = stalePath
	return base, nil
}

// PreserveStalePIDEvidence moves dead or malformed PID evidence aside under a
// transition lock before a replacement daemon publishes its PID.
func PreserveStalePIDEvidence(active ActiveRuntime) error {
	if active.StalePIDPath == "" {
		return nil
	}
	target := active.StalePIDPath + ".stale"
	for suffix := 2; ; suffix++ {
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			break
		}
		target = fmt.Sprintf("%s.stale-%d", active.StalePIDPath, suffix)
	}
	return os.Rename(active.StalePIDPath, target)
}

func readPIDAt(dir string) (int, error) {
	path := filepath.Join(dir, "watch.pid")
	if err := requireRegularRuntimeFile(path); err != nil {
		return 0, err
	}
	data, err := runtimefile.Read(path)
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}

func requireRegularRuntimeFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe runtime file %q", path)
	}
	return nil
}

// daemonOwnershipForPID classifies whether pid is this repo's watch daemon.
// It takes the PID explicitly (rather than re-reading watch.pid) so callers can
// validate the exact process they are about to act on, avoiding a TOCTOU race
// with a concurrent start/stop rewriting the pid file.
func daemonOwnershipForPID(root string, pid int) daemonOwnership {
	if pid <= 0 {
		return ownershipForeign
	}
	cmdline, err := processCommandLine(pid)
	if err != nil {
		return ownershipUnknown
	}
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return ownershipUnknown
	}

	absRoot := projectpath.CanonicalPath(root)
	const daemonMarker = " watch daemon "
	marker := strings.LastIndex(cmdline, daemonMarker)
	if marker >= 0 {
		candidate := strings.Trim(strings.TrimSpace(cmdline[marker+len(daemonMarker):]), `"`)
		candidate = projectpath.CanonicalPath(candidate)
		if candidate == absRoot {
			return ownershipOwned
		}
	}
	return ownershipForeign
}

// IsOwnedDaemon reports whether the PID file points to a codemap watch daemon
// for this repository root. It is true only when ownership is positively
// confirmed.
func IsOwnedDaemon(root string) bool {
	pid, err := ReadPID(root)
	if err != nil || pid <= 0 {
		return false
	}
	return daemonOwnershipForPID(root, pid) == ownershipOwned
}

// IsRunning checks if the daemon is running
func IsRunning(root string) bool {
	active, err := ResolveActiveRuntime(root)
	if err != nil || active.PID <= 0 {
		return false
	}
	// Liveness is checked in a platform-specific way: Signal(0) on Unix is
	// unsupported on Windows, so processAlive queries the OS directly there.
	return processAlive(active.PID)
}

// Stop requests shutdown of the daemon process and removes its PID file.
func Stop(root string) error {
	transition, err := acquireTransitionWithin(root, 2*time.Second)
	if err != nil {
		return err
	}
	defer transition.Release()
	active, err := ResolveActiveRuntime(root)
	if err != nil {
		return err
	}
	if active.PID <= 0 {
		return fmt.Errorf("no daemon running: %w", os.ErrNotExist)
	}
	proc, err := os.FindProcess(active.PID)
	if err != nil {
		return err
	}
	// terminateDaemon is platform-specific: SIGTERM on Unix; on Windows it
	// verifies the PID belongs to this repo's daemon (guarding against a reused
	// stale PID) before killing, returning ErrForeignDaemonPID otherwise.
	if err := terminateDaemon(active.CanonicalRoot, proc); err != nil {
		// Never remove the pid file on ErrForeignDaemonPID: the PID is alive
		// and unverified, so clearing it could orphan a real daemon.
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(active.PID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(active.PID) {
		return ErrDaemonExitTimeout
	}
	if err := removePIDIfMatches(active.Directory, active.PID); err != nil {
		return err
	}
	return nil
}

func removePIDIfMatches(dir string, pid int) error {
	current, err := readPIDAt(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || current != pid {
		return fmt.Errorf("watch PID changed during transition")
	}
	return os.Remove(filepath.Join(dir, "watch.pid"))
}

// RemoveProcessPID removes only the caller's still-current PID publication.
func RemoveProcessPID(root string, pid int) error {
	selection, err := projectpath.SelectRuntime(root)
	if err != nil {
		return err
	}
	return removePIDIfMatches(projectpath.ProjectRuntimeDir(selection.ProjectRoot), pid)
}
