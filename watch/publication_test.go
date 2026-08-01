package watch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStatePublisherCoalescesBurstIntoOneGeneration(t *testing.T) {
	root := t.TempDir()
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.watcher.Close()

	p := newStatePublisher(d, filepath.Join(root, "state.json"), "instance-a")
	now := time.Now()
	for range 20 {
		p.markDirty(now)
	}
	if p.generation != 0 {
		t.Fatalf("generation advanced before publication: %d", p.generation)
	}
	if err := p.publish(); err != nil {
		t.Fatal(err)
	}
	if p.generation != 1 {
		t.Fatalf("generation = %d, want 1", p.generation)
	}
	state := readStateAt(t, filepath.Join(root, "state.json"))
	if state.DaemonInstance != "instance-a" || state.Generation != 1 || state.CanonicalRoot != root {
		t.Fatalf("published identity = %#v", state)
	}
}

func TestStatePublisherReplacementAlwaysDecodes(t *testing.T) {
	root := t.TempDir()
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.watcher.Close()
	path := filepath.Join(root, "state.json")
	p := newStatePublisher(d, path, "instance-a")
	if err := p.publish(); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 500 {
			data, err := os.ReadFile(path)
			if err != nil {
				errCh <- err
				return
			}
			var state State
			if err := json.Unmarshal(data, &state); err != nil {
				errCh <- err
				return
			}
		}
	}()
	for range 50 {
		p.markDirty(time.Now())
		if err := p.publish(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}

func TestFlushRequestValidationRequiresExactGenerationIdentity(t *testing.T) {
	req := flushRequest{Version: flushProtocolVersion, CanonicalRoot: "/repo", DaemonInstance: "daemon-a", Nonce: "nonce-a", ObservedGeneration: 4}
	ack := flushAck{Version: flushProtocolVersion, CanonicalRoot: "/repo", DaemonInstance: "daemon-a", Nonce: "nonce-a", ObservedGeneration: 4, PublishedGeneration: 5, Success: true}
	if err := validateFlushAck(req, ack); err != nil {
		t.Fatal(err)
	}
	ack.PublishedGeneration = 4
	if err := validateFlushAck(req, ack); err == nil {
		t.Fatal("accepted same-generation acknowledgement")
	}
	ack.PublishedGeneration = 5
	ack.DaemonInstance = "daemon-b"
	if err := validateFlushAck(req, ack); err == nil {
		t.Fatal("accepted acknowledgement from replacement daemon")
	}
}

func TestFlushRequestWaitsForQuietWindowAndAcknowledgesNextGeneration(t *testing.T) {
	root := t.TempDir()
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.watcher.Close()
	p := newStatePublisher(d, filepath.Join(root, "state.json"), "instance-a")
	p.flushDir = filepath.Join(root, "flush")
	if err := os.MkdirAll(p.flushDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.publish(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	req := flushRequest{Version: flushProtocolVersion, CanonicalRoot: root, DaemonInstance: "instance-a", Nonce: "nonce-a", ObservedGeneration: 1, Timestamp: now}
	data, _ := json.Marshal(req)
	if err := os.WriteFile(filepath.Join(p.flushDir, "request-nonce-a.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	p.scanRequests(now)
	if p.due(now.Add(publicationQuietWindow - time.Millisecond)) {
		t.Fatal("request acknowledged before quiet window")
	}
	p.markDirty(now.Add(50 * time.Millisecond))
	if p.due(now.Add(publicationQuietWindow)) {
		t.Fatal("later event did not reset quiet window")
	}
	if !p.due(now.Add(151 * time.Millisecond)) {
		t.Fatal("request never became due")
	}
	if err := p.publish(); err != nil {
		t.Fatal(err)
	}
	ackData, err := os.ReadFile(filepath.Join(p.flushDir, "ack-nonce-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ack flushAck
	if err := json.Unmarshal(ackData, &ack); err != nil {
		t.Fatal(err)
	}
	if err := validateFlushAck(req, ack); err != nil {
		t.Fatal(err)
	}
	if ack.PublishedGeneration != 2 {
		t.Fatalf("published generation = %d, want 2", ack.PublishedGeneration)
	}
}

func TestFlushStateHonorsExpiredContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := FlushState(ctx, t.TempDir())
	if err == nil {
		t.Fatal("FlushState succeeded with expired context")
	}
}

func TestControlDirectoryRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	path := filepath.Join(root, "flush")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := ensureControlDirectory(path); err == nil {
		t.Fatal("accepted symlink control directory")
	}
}

func TestDaemonProcessesFlushRequestThroughWatchedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	req := flushRequest{Version: flushProtocolVersion, CanonicalRoot: d.root, DaemonInstance: d.publisher.instance, Nonce: "watched", ObservedGeneration: d.publisher.generation, Timestamp: time.Now()}
	data, _ := json.Marshal(req)
	if err := os.WriteFile(filepath.Join(d.publisher.flushDir, "request-watched.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		ackData, readErr := os.ReadFile(filepath.Join(d.publisher.flushDir, "ack-watched.json"))
		if readErr == nil {
			var ack flushAck
			if err := json.Unmarshal(ackData, &ack); err != nil {
				t.Fatal(err)
			}
			if err := validateFlushAck(req, ack); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not acknowledge flush request")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readStateAt(t *testing.T, path string) State {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
