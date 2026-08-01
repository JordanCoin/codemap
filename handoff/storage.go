package handoff

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"codemap/internal/projectpath"
	"codemap/internal/runtimefile"
)

const (
	latestFilename  = "handoff.latest.json"
	prefixFilename  = "handoff.prefix.json"
	deltaFilename   = "handoff.delta.json"
	metricsFilename = "handoff.metrics.log"
	maxMetricsLines = 500
)

// LatestPath returns the absolute location of the latest handoff artifact.
func LatestPath(root string) string {
	runtimeRoot := projectpath.ProjectRuntimeDir(root)
	absRoot, err := filepath.Abs(runtimeRoot)
	if err != nil {
		return filepath.Join(runtimeRoot, latestFilename)
	}
	return filepath.Join(absRoot, latestFilename)
}

// PrefixPath returns the absolute location of the prefix snapshot.
func PrefixPath(root string) string {
	runtimeRoot := projectpath.ProjectRuntimeDir(root)
	absRoot, err := filepath.Abs(runtimeRoot)
	if err != nil {
		return filepath.Join(runtimeRoot, prefixFilename)
	}
	return filepath.Join(absRoot, prefixFilename)
}

// DeltaPath returns the absolute location of the delta snapshot.
func DeltaPath(root string) string {
	runtimeRoot := projectpath.ProjectRuntimeDir(root)
	absRoot, err := filepath.Abs(runtimeRoot)
	if err != nil {
		return filepath.Join(runtimeRoot, deltaFilename)
	}
	return filepath.Join(absRoot, deltaFilename)
}

// MetricsPath returns the absolute location of the handoff metrics log.
func MetricsPath(root string) string {
	runtimeRoot := projectpath.ProjectRuntimeDir(root)
	absRoot, err := filepath.Abs(runtimeRoot)
	if err != nil {
		return filepath.Join(runtimeRoot, metricsFilename)
	}
	return filepath.Join(absRoot, metricsFilename)
}

// ReadLatest reads the latest handoff artifact if it exists.
// Returns (nil, nil) when no artifact is present.
func ReadLatest(root string) (*Artifact, error) {
	path, err := runtimePath(root, latestFilename)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return nil, err
	}
	normalizeArtifact(&artifact)

	return &artifact, nil
}

// WriteLatest writes an artifact atomically to .codemap/handoff.latest.json.
func WriteLatest(root string, artifact *Artifact) error {
	normalizeArtifact(artifact)

	runtimeDir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		return err
	}
	path := filepath.Join(runtimeDir, latestFilename)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	if err := writeJSONAtomic(path, artifact); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(runtimeDir, prefixFilename), artifact.Prefix); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(runtimeDir, deltaFilename), artifact.Delta); err != nil {
		return err
	}
	return appendMetricsAt(filepath.Join(runtimeDir, metricsFilename), artifact)
}

func runtimePath(root, name string) (string, error) {
	dir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return runtimefile.WriteAtomic(path, data, 0o644)
}

func appendMetrics(root string, artifact *Artifact) error {
	path, err := runtimePath(root, metricsFilename)
	if err != nil {
		return err
	}
	return appendMetricsAt(path, artifact)
}

func appendMetricsAt(path string, artifact *Artifact) error {
	entry := struct {
		GeneratedAt  string       `json:"generated_at"`
		Branch       string       `json:"branch"`
		BaseRef      string       `json:"base_ref"`
		PrefixHash   string       `json:"prefix_hash"`
		DeltaHash    string       `json:"delta_hash"`
		CombinedHash string       `json:"combined_hash"`
		Metrics      CacheMetrics `json:"metrics"`
	}{
		GeneratedAt:  artifact.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"),
		Branch:       artifact.Branch,
		BaseRef:      artifact.BaseRef,
		PrefixHash:   artifact.PrefixHash,
		DeltaHash:    artifact.DeltaHash,
		CombinedHash: artifact.CombinedHash,
		Metrics:      artifact.Metrics,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	f, err := runtimefile.OpenAppend(path, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return capMetricsLogAt(path, maxMetricsLines)
}

func normalizeArtifact(artifact *Artifact) {
	ensureSchemaVersion(artifact)
	promoteLegacyFieldsIntoDelta(artifact)
	ensureNonNilSnapshotFields(artifact)
	mirrorDeltaToLegacy(artifact)
	backfillHashes(artifact)
}

func ensureSchemaVersion(artifact *Artifact) {
	if artifact.SchemaVersion == 0 {
		artifact.SchemaVersion = SchemaVersion
	}
}

func promoteLegacyFieldsIntoDelta(artifact *Artifact) {
	if artifact.Delta.Changed == nil && len(artifact.ChangedFiles) > 0 {
		artifact.Delta.Changed = make([]FileStub, 0, len(artifact.ChangedFiles))
		for _, path := range artifact.ChangedFiles {
			artifact.Delta.Changed = append(artifact.Delta.Changed, FileStub{Path: path})
		}
	}
	if artifact.Delta.RiskFiles == nil {
		artifact.Delta.RiskFiles = append([]RiskFile{}, artifact.RiskFiles...)
	}
	if artifact.Delta.RecentEvents == nil {
		artifact.Delta.RecentEvents = append([]EventSummary{}, artifact.RecentEvents...)
	}
	if artifact.Delta.NextSteps == nil {
		artifact.Delta.NextSteps = append([]string{}, artifact.NextSteps...)
	}
	if artifact.Delta.OpenQuestions == nil {
		artifact.Delta.OpenQuestions = append([]string{}, artifact.OpenQuestions...)
	}
}

func ensureNonNilSnapshotFields(artifact *Artifact) {
	artifact.Prefix.Hubs = nonNilHubs(artifact.Prefix.Hubs)
	artifact.Delta.Changed = nonNilStubs(artifact.Delta.Changed)
	artifact.Delta.RiskFiles = nonNilRiskFiles(artifact.Delta.RiskFiles)
	artifact.Delta.RecentEvents = nonNilEvents(artifact.Delta.RecentEvents)
	artifact.Delta.NextSteps = nonNilStrings(artifact.Delta.NextSteps)
	artifact.Delta.OpenQuestions = nonNilStrings(artifact.Delta.OpenQuestions)
}

func mirrorDeltaToLegacy(artifact *Artifact) {
	if artifact.ChangedFiles == nil {
		artifact.ChangedFiles = stubPaths(artifact.Delta.Changed)
	}
	if artifact.RiskFiles == nil {
		artifact.RiskFiles = append([]RiskFile{}, artifact.Delta.RiskFiles...)
	}
	if artifact.RecentEvents == nil {
		artifact.RecentEvents = append([]EventSummary{}, artifact.Delta.RecentEvents...)
	}
	if artifact.NextSteps == nil {
		artifact.NextSteps = append([]string{}, artifact.Delta.NextSteps...)
	}
	if artifact.OpenQuestions == nil {
		artifact.OpenQuestions = append([]string{}, artifact.Delta.OpenQuestions...)
	}
}

func backfillHashes(artifact *Artifact) {
	if artifact.PrefixHash == "" {
		if hash, _, err := hashCanonical(artifact.Prefix); err == nil {
			artifact.PrefixHash = hash
		}
	}
	if artifact.DeltaHash == "" {
		if hash, _, err := hashCanonical(artifact.Delta); err == nil {
			artifact.DeltaHash = hash
		}
	}
	if artifact.CombinedHash == "" {
		artifact.CombinedHash = hashFromStrings(artifact.PrefixHash, artifact.DeltaHash)
	}
}

func capMetricsLogAt(path string, maxLines int) error {
	if maxLines <= 0 {
		return nil
	}

	data, err := runtimefile.Read(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}

	lines := bytes.Split(data, []byte("\n"))
	if len(lines) <= maxLines {
		return nil
	}

	trimmed := bytes.Join(lines[len(lines)-maxLines:], []byte("\n"))
	trimmed = append(trimmed, '\n')
	return runtimefile.WriteAtomic(path, trimmed, 0o644)
}
