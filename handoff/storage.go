package handoff

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const latestFilename = "handoff.latest.json"

// LatestPath returns the absolute location of the latest handoff artifact.
func LatestPath(root string) string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.Join(root, ".codemap", latestFilename)
	}
	return filepath.Join(absRoot, ".codemap", latestFilename)
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

	if artifact.SchemaVersion == 0 {
		artifact.SchemaVersion = SchemaVersion
	}
	if artifact.ChangedFiles == nil {
		artifact.ChangedFiles = []string{}
	}
	if artifact.RiskFiles == nil {
		artifact.RiskFiles = []RiskFile{}
	}
	if artifact.RecentEvents == nil {
		artifact.RecentEvents = []EventSummary{}
	}
	if artifact.NextSteps == nil {
		artifact.NextSteps = []string{}
	}
	if artifact.OpenQuestions == nil {
		artifact.OpenQuestions = []string{}
	}

	return &artifact, nil
}

// WriteLatest writes an artifact atomically to .codemap/handoff.latest.json.
func WriteLatest(root string, artifact *Artifact) error {
	path := LatestPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
