package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProjectConfig holds per-project defaults from .codemap/config.json.
// All fields are optional; zero values mean "no preference".
type ProjectConfig struct {
	Only    []string `json:"only,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	Depth   int      `json:"depth,omitempty"`
}

// ConfigPath returns the path to .codemap/config.json for the given root.
func ConfigPath(root string) string {
	return filepath.Join(root, ".codemap", "config.json")
}

// Load reads .codemap/config.json from root.
// Returns zero-value ProjectConfig if the file is missing.
// Logs a warning to stderr and returns zero-value if JSON is malformed.
func Load(root string) ProjectConfig {
	data, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		return ProjectConfig{}
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: malformed .codemap/config.json: %v\n", err)
		return ProjectConfig{}
	}
	return cfg
}
