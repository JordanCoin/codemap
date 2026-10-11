package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Stop read dependencyCancel and waited on dependencyWorkerWG while the
// event loop could still be inside the lazy worker start, so a worker could
// be started after Stop returned and outlive the daemon (#217).
func TestStopRacesLazyWorkerStart(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	d.watcher.Close()
	d.watcher.Events = make(chan fsnotify.Event, 8)
	d.watcher.Errors = make(chan error)
	if err := os.MkdirAll(d.runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	d.eventLoopWG.Add(1)
	go func() {
		defer d.eventLoopWG.Done()
		d.eventLoop()
	}()
	d.watcher.Events <- fsnotify.Event{Name: filepath.Join(root, "a.go"), Op: fsnotify.Create}
	time.Sleep(time.Millisecond)
	d.Stop()
}
