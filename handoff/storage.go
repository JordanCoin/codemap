package handoff

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	latestFilename  = "handoff.latest.json"
	prefixFilename  = "handoff.prefix.json"
	deltaFilename   = "handoff.delta.json"
	metricsFilename = "handoff.metrics.log"
)

// LatestPath returns the absolute location of the latest handoff artifact.
func LatestPath(root string) string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.Join(root, ".codemap", latestFilename)
	}
	return filepath.Join(absRoot, ".codemap", latestFilename)
}

// PrefixPath returns the absolute location of the prefix snapshot.
func PrefixPath(root string) string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.Join(root, ".codemap", prefixFilename)
	}
	return filepath.Join(absRoot, ".codemap", prefixFilename)
}

// DeltaPath returns the absolute location of the delta snapshot.
func DeltaPath(root string) string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.Join(root, ".codemap", deltaFilename)
	}
	return filepath.Join(absRoot, ".codemap", deltaFilename)
}

// MetricsPath returns the absolute location of the handoff metrics log.
func MetricsPath(root string) string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.Join(root, ".codemap", metricsFilename)
	}
	return filepath.Join(absRoot, ".codemap", metricsFilename)
}

// ReadLatest reads the latest handoff artifact if it exists.
// Returns (nil, nil) when no artifact is present.
func ReadLatest(root string) (*Artifact, error) {
	path := LatestPath(root)
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

	path := LatestPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	if err := writeJSONAtomic(path, artifact); err != nil {
		return err
	}
	if err := writeJSONAtomic(PrefixPath(root), artifact.Prefix); err != nil {
		return err
	}
	if err := writeJSONAtomic(DeltaPath(root), artifact.Delta); err != nil {
		return err
	}
	return appendMetrics(root, artifact)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func appendMetrics(root string, artifact *Artifact) error {
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

	f, err := os.OpenFile(MetricsPath(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func normalizeArtifact(artifact *Artifact) {
	if artifact.SchemaVersion == 0 {
		artifact.SchemaVersion = SchemaVersion
	}

	if artifact.Prefix.Hubs == nil {
		artifact.Prefix.Hubs = []HubSummary{}
	}

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

	artifact.Delta.Changed = nonNilStubs(artifact.Delta.Changed)
	artifact.Delta.RiskFiles = nonNilRiskFiles(artifact.Delta.RiskFiles)
	artifact.Delta.RecentEvents = nonNilEvents(artifact.Delta.RecentEvents)
	artifact.Delta.NextSteps = nonNilStrings(artifact.Delta.NextSteps)
	artifact.Delta.OpenQuestions = nonNilStrings(artifact.Delta.OpenQuestions)

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
