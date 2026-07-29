package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The watch daemon sees every write on disk — including git merges, checkouts,
// and generated files — so daemon state alone cannot say what an agent
// actually edited. The post-edit hook is the only place that knows a write
// came from an agent tool call, so it appends a record here and the display
// layer partitions "this agent edited" from "changed on disk".
const (
	agentEditsFile     = "agent_edits.jsonl"
	agentEditsMaxBytes = 256 * 1024
	agentEditRecency   = 48 * time.Hour
)

type agentEditRecord struct {
	Path    string    `json:"path"`
	Session string    `json:"session,omitempty"`
	At      time.Time `json:"at"`
}

type agentEdits struct {
	paths     map[string]bool            // repo-relative slash paths, any session
	bySession map[string]map[string]bool // session id -> paths
}

func agentEditsPath(root string) string {
	return filepath.Join(root, ".codemap", agentEditsFile)
}

// normalizeAgentEditPath converts hook-provided paths (often absolute, with
// OS-native separators) to the repo-relative slash form used across codemap.
func normalizeAgentEditPath(root, path string) string {
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
	}
	return filepath.ToSlash(path)
}

// recordAgentEdit appends one edit record. Appends are line-buffered and
// O_APPEND so concurrent short-lived hook processes interleave safely on
// every supported platform.
func recordAgentEdit(root, sessionID, path string, now time.Time) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		return err
	}
	record := agentEditRecord{
		Path:    normalizeAgentEditPath(root, path),
		Session: strings.TrimSpace(sessionID),
		At:      now.UTC(),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	logPath := agentEditsPath(root)
	compactAgentEdits(logPath, now)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// compactAgentEdits bounds the log by rewriting it with only recent records
// once it grows past the cap. Best-effort: a concurrently appended record can
// be lost during the rewrite, which is acceptable for display hints.
func compactAgentEdits(logPath string, now time.Time) {
	info, err := os.Stat(logPath)
	if err != nil || info.Size() <= agentEditsMaxBytes {
		return
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return
	}
	cutoff := now.Add(-agentEditRecency)
	var kept bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var record agentEditRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		if record.At.Before(cutoff) {
			continue
		}
		kept.Write(scanner.Bytes())
		kept.WriteByte('\n')
	}
	_ = writeFileAtomic(logPath, kept.Bytes(), 0o644)
}

// loadAgentEdits reads records at or after `since`. Missing or malformed logs
// yield an empty (never nil) result.
func loadAgentEdits(root string, since time.Time) agentEdits {
	edits := agentEdits{
		paths:     make(map[string]bool),
		bySession: make(map[string]map[string]bool),
	}
	data, err := os.ReadFile(agentEditsPath(root))
	if err != nil {
		return edits
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var record agentEditRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		if record.At.Before(since) {
			continue
		}
		edits.paths[record.Path] = true
		if record.Session != "" {
			if edits.bySession[record.Session] == nil {
				edits.bySession[record.Session] = make(map[string]bool)
			}
			edits.bySession[record.Session][record.Path] = true
		}
	}
	return edits
}
