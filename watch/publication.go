package watch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"codemap/internal/runtimefile"
	"codemap/limits"
)

const (
	publicationQuietWindow = 100 * time.Millisecond
	publicationRetryDelay  = 250 * time.Millisecond
	flushArtifactTTL       = 5 * time.Minute
	maxFlushArtifacts      = 256
)

type statePublisher struct {
	daemon     *Daemon
	path       string
	flushDir   string
	instance   string
	generation uint64
	dirty      bool
	deadline   time.Time
	pending    map[string]flushRequest
	seen       map[string]time.Time
}

func newDaemonInstance() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func newStatePublisher(d *Daemon, path, instance string) *statePublisher {
	return &statePublisher{daemon: d, path: path, flushDir: filepath.Join(filepath.Dir(path), "flush"), instance: instance, pending: map[string]flushRequest{}, seen: map[string]time.Time{}}
}

func (p *statePublisher) markDirty(now time.Time) {
	p.dirty = true
	p.deadline = now.Add(publicationQuietWindow)
}
func (p *statePublisher) nextDelay(now time.Time) (time.Duration, bool) {
	if !p.dirty && len(p.pending) == 0 {
		return 0, false
	}
	d := p.deadline.Sub(now)
	if d < 0 {
		d = 0
	}
	return d, true
}
func (p *statePublisher) due(now time.Time) bool {
	return (p.dirty || len(p.pending) > 0) && !p.deadline.After(now)
}

func (p *statePublisher) snapshot(generation uint64) State {
	d := p.daemon
	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()
	events := d.graph.Events
	if len(events) > limits.MaxStateRecentEvents {
		events = events[len(events)-limits.MaxStateRecentEvents:]
	}
	configuredFileCount := len(d.graph.ConfiguredFiles)
	if d.graph.ConfiguredFiles == nil {
		configuredFileCount = len(d.graph.Files)
	}
	root := canonicalRoot(d.root)
	graphState := d.graph.GraphState
	s := State{Root: root, CanonicalRoot: root, DaemonInstance: p.instance, Generation: generation, UpdatedAt: time.Now(), FileCount: len(d.graph.Files), ConfiguredFileCount: &configuredFileCount, Hubs: []string{}, Importers: map[string][]string{}, Imports: map[string][]string{}, RecentEvents: append([]Event(nil), events...), WorkingSet: d.graph.WorkingSet.Snapshot(50), Graph: &graphState}
	if d.graph.FileGraph != nil {
		s.Hubs = d.graph.FileGraph.HubFiles()
		s.Importers = d.graph.FileGraph.Importers
		s.Imports = d.graph.FileGraph.Imports
		s.Coverage = d.graph.FileGraph.Coverage
	}
	return s
}

func (p *statePublisher) publish() error {
	next := p.generation + 1
	data, err := json.MarshalIndent(p.snapshot(next), "", "  ")
	if err != nil {
		p.dirty = true
		p.deadline = time.Now().Add(publicationRetryDelay)
		return err
	}
	if err = runtimefile.WriteAtomic(p.path, data, 0o644); err != nil {
		p.dirty = true
		ackErr := p.failPending("publication_failed")
		p.deadline = time.Now().Add(publicationRetryDelay)
		return errors.Join(err, ackErr)
	}
	p.generation = next
	p.dirty = false
	p.deadline = time.Time{}
	var ackErr error
	for nonce, req := range p.pending {
		ackErr = errors.Join(ackErr, p.writeAck(req, flushAck{Version: flushProtocolVersion, CanonicalRoot: req.CanonicalRoot, DaemonInstance: req.DaemonInstance, Nonce: req.Nonce, ObservedGeneration: req.ObservedGeneration, PublishedGeneration: next, Timestamp: time.Now().UTC(), Success: true}))
		delete(p.pending, nonce)
	}
	return ackErr
}

func (p *statePublisher) failPending(code string) error {
	var firstErr error
	for nonce, req := range p.pending {
		if err := p.writeAck(req, flushAck{Version: flushProtocolVersion, CanonicalRoot: req.CanonicalRoot, DaemonInstance: req.DaemonInstance, Nonce: req.Nonce, ObservedGeneration: req.ObservedGeneration, Timestamp: time.Now().UTC(), ErrorCode: code}); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(p.pending, nonce)
	}
	return firstErr
}
func (p *statePublisher) writeAck(req flushRequest, ack flushAck) error {
	data, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	return runtimefile.WriteAtomic(filepath.Join(p.flushDir, "ack-"+req.Nonce+".json"), data, 0o600)
}

func (p *statePublisher) scanRequests(now time.Time) {
	failControl := func() {
		if err := p.failPending("control_unavailable"); err != nil {
			p.dirty = true
			p.deadline = now.Add(publicationRetryDelay)
		}
	}
	if err := ensureControlDirectory(p.flushDir); err != nil {
		failControl()
		return
	}
	entries, err := os.ReadDir(p.flushDir)
	if err != nil {
		failControl()
		return
	}
	p.pruneControlArtifacts(entries, now)
	entries, err = os.ReadDir(p.flushDir)
	if err != nil {
		failControl()
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || len(name) < 14 || name[:8] != "request-" || filepath.Ext(name) != ".json" {
			continue
		}
		data, err := runtimefile.Read(filepath.Join(p.flushDir, name))
		if err != nil {
			continue
		}
		var req flushRequest
		if json.Unmarshal(data, &req) != nil {
			continue
		}
		key := req.identity()
		if _, ok := p.seen[key]; ok {
			continue
		}
		if req.Version != flushProtocolVersion || req.CanonicalRoot != p.daemon.root || req.DaemonInstance != p.instance || "request-"+req.Nonce+".json" != name {
			continue
		}
		p.seen[key] = now
		p.pending[req.Nonce] = req
		p.markDirty(now)
	}
}

func (p *statePublisher) pruneControlArtifacts(entries []os.DirEntry, now time.Time) {
	cutoff := now.Add(-flushArtifactTTL)
	type artifact struct {
		path string
		mod  time.Time
	}
	artifacts := make([]artifact, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !(strings.HasPrefix(name, "request-") || strings.HasPrefix(name, "ack-")) || filepath.Ext(name) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(p.flushDir, name)
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
			continue
		}
		artifacts = append(artifacts, artifact{path: path, mod: info.ModTime()})
	}
	for key, seenAt := range p.seen {
		if seenAt.Before(cutoff) {
			delete(p.seen, key)
		}
	}
	if len(p.seen) > maxFlushArtifacts {
		type identity struct {
			key string
			at  time.Time
		}
		identities := make([]identity, 0, len(p.seen))
		for key, at := range p.seen {
			identities = append(identities, identity{key: key, at: at})
		}
		slices.SortFunc(identities, func(a, b identity) int { return a.at.Compare(b.at) })
		for _, item := range identities[:len(identities)-maxFlushArtifacts] {
			delete(p.seen, item.key)
		}
	}
	if len(artifacts) <= maxFlushArtifacts {
		return
	}
	slices.SortFunc(artifacts, func(a, b artifact) int { return a.mod.Compare(b.mod) })
	for _, item := range artifacts[:len(artifacts)-maxFlushArtifacts] {
		_ = os.Remove(item.path)
	}
}
