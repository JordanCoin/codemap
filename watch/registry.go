package watch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codemap/internal/projectpath"
	"codemap/internal/runtimefile"
)

// Daemon registry (#155 defect 2, #151): every published daemon pid is also
// recorded under the user's codemap directory, so `codemap watch status --all`
// and `codemap watch stop --all` find daemons across projects without reading
// the process table. The per-project pid file stays authoritative; the
// registry is best effort and self-pruning.
//
// ponytail: daemons started by binaries older than this never registered and
// stay invisible here; add a `ps` sweep if that proves to matter.

// daemonRegistryEnv overrides the registry location (tests, sandboxes).
const daemonRegistryEnv = "CODEMAP_DAEMON_REGISTRY"

// DaemonInfo is one registered daemon.
type DaemonInfo struct {
	PID     int       `json:"pid"`
	Root    string    `json:"root"`
	Started time.Time `json:"-"`
}

type registryEntry struct {
	DaemonInfo
	path string
}

func daemonRegistryDir() (string, error) {
	if dir := os.Getenv(daemonRegistryEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codemap", "daemons"), nil
}

// registerDaemon records pid for root. Failures are ignored: a daemon must
// start even when the home directory is unwritable.
func registerDaemon(root string, pid int) {
	dir, err := daemonRegistryDir()
	if err != nil {
		return
	}
	// Create .codemap/daemons but never a missing home above it.
	if _, err := os.Stat(filepath.Dir(filepath.Dir(dir))); err != nil {
		return
	}
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := os.Mkdir(d, 0o700); err != nil && !os.IsExist(err) {
			return
		}
	}
	payload, err := json.Marshal(DaemonInfo{PID: pid, Root: canonicalRoot(root)})
	if err != nil {
		return
	}
	_ = runtimefile.WriteAtomic(filepath.Join(dir, projectpath.ProjectKey(root)+".json"), payload, 0o600)
}

// registeredPID returns the pid the registry records for root, or 0.
func registeredPID(root string) int {
	dir, err := daemonRegistryDir()
	if err != nil {
		return 0
	}
	data, err := runtimefile.Read(filepath.Join(dir, projectpath.ProjectKey(root)+".json"))
	if err != nil {
		return 0
	}
	var info DaemonInfo
	if json.Unmarshal(data, &info) != nil || info.PID <= 0 || canonicalRoot(info.Root) != canonicalRoot(root) {
		return 0
	}
	return info.PID
}

// unregisterDaemon drops every entry naming pid.
func unregisterDaemon(pid int) {
	for _, entry := range readDaemonRegistry() {
		if entry.PID == pid {
			_ = os.Remove(entry.path)
		}
	}
}

// readDaemonRegistry returns every well-formed entry and deletes malformed ones.
func readDaemonRegistry() []registryEntry {
	dir, err := daemonRegistryDir()
	if err != nil {
		return nil
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var entries []registryEntry
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, item.Name())
		info, err := item.Info()
		if err != nil {
			continue
		}
		data, err := runtimefile.Read(path)
		if err != nil {
			continue
		}
		var entry registryEntry
		if json.Unmarshal(data, &entry.DaemonInfo) != nil || entry.PID <= 0 || entry.Root == "" {
			_ = os.Remove(path)
			continue
		}
		entry.Started = info.ModTime()
		entry.path = path
		entries = append(entries, entry)
	}
	return entries
}

// ListDaemons returns the daemons this user started that are still alive,
// sorted by root. Entries whose process is gone, or is a live process that is
// not a codemap daemon for its root, are pruned. Entries whose ownership
// cannot be verified are kept and reported as recorded.
func ListDaemons() []DaemonInfo {
	var live []DaemonInfo
	for _, entry := range readDaemonRegistry() {
		if !processAlive(entry.PID) || daemonOwnershipForPID(entry.Root, entry.PID) == ownershipForeign {
			_ = os.Remove(entry.path)
			continue
		}
		live = append(live, entry.DaemonInfo)
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Root < live[j].Root })
	return live
}

// StopDaemon terminates one registered daemon after the ownership check and
// waits for it to exit. The daemon removes its own pid file on the way out;
// this only drops the registry entry.
func StopDaemon(d DaemonInfo) error {
	proc, err := os.FindProcess(d.PID)
	if err != nil {
		return err
	}
	if err := terminateDaemon(d.Root, proc); err != nil {
		if errors.Is(err, ErrForeignDaemonPID) {
			unregisterDaemon(d.PID)
		}
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(d.PID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(d.PID) {
		return ErrDaemonExitTimeout
	}
	unregisterDaemon(d.PID)
	return nil
}
