package pluginbundle

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	CodemapPluginName           = "codemap"
	defaultMarketplaceName      = "codemap"
	defaultMarketplaceTitle     = "Codemap"
	defaultInstallationPolicy   = "AVAILABLE"
	defaultAuthenticationPolicy = "ON_INSTALL"
	defaultCategory             = "Coding"
)

//go:embed all:codemap
var codemapBundle embed.FS

type InstallOptions struct {
	HomeDir         string
	PluginPath      string
	MarketplacePath string
}

type InstallResult struct {
	PluginPath         string
	MarketplacePath    string
	FilesWritten       int
	FilesUnchanged     int
	CreatedMarketplace bool
	UpdatedMarketplace bool
}

func InstallCodemapPlugin(opts InstallOptions) (InstallResult, error) {
	if opts.HomeDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return InstallResult{}, fmt.Errorf("resolve home directory: %w", err)
		}
		opts.HomeDir = homeDir
	}
	if opts.PluginPath == "" {
		opts.PluginPath = filepath.Join(opts.HomeDir, "plugins", CodemapPluginName)
	}
	if opts.MarketplacePath == "" {
		opts.MarketplacePath = filepath.Join(opts.HomeDir, ".agents", "plugins", "marketplace.json")
	}

	result := InstallResult{
		PluginPath:      opts.PluginPath,
		MarketplacePath: opts.MarketplacePath,
	}

	bundleRoot, err := fs.Sub(codemapBundle, CodemapPluginName)
	if err != nil {
		return result, fmt.Errorf("load embedded plugin bundle: %w", err)
	}
	if err := writeBundle(bundleRoot, opts.PluginPath, &result); err != nil {
		return result, err
	}

	created, updated, err := ensureMarketplaceEntry(opts.MarketplacePath)
	if err != nil {
		return result, err
	}
	result.CreatedMarketplace = created
	result.UpdatedMarketplace = updated

	return result, nil
}

func writeBundle(bundleRoot fs.FS, targetRoot string, result *InstallResult) error {
	return fs.WalkDir(bundleRoot, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return os.MkdirAll(targetRoot, 0o755)
		}

		targetPath := filepath.Join(targetRoot, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}

		data, err := fs.ReadFile(bundleRoot, path)
		if err != nil {
			return fmt.Errorf("read embedded file %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("create parent directory for %s: %w", targetPath, err)
		}

		mode := fileModeFor(path)
		if existing, err := os.ReadFile(targetPath); err == nil && bytes.Equal(existing, data) {
			if info, statErr := os.Stat(targetPath); statErr == nil && info.Mode().Perm() != mode {
				if chmodErr := os.Chmod(targetPath, mode); chmodErr != nil {
					return fmt.Errorf("chmod %s: %w", targetPath, chmodErr)
				}
				result.FilesWritten++
				return nil
			}
			result.FilesUnchanged++
			return nil
		}

		if err := os.WriteFile(targetPath, data, mode); err != nil {
			return fmt.Errorf("write %s: %w", targetPath, err)
		}
		result.FilesWritten++
		return nil
	})
}

func fileModeFor(path string) os.FileMode {
	if strings.HasSuffix(path, ".sh") {
		return 0o755
	}
	return 0o644
}

func ensureMarketplaceEntry(marketplacePath string) (created bool, updated bool, err error) {
	payload := map[string]any{}
	data, readErr := os.ReadFile(marketplacePath)
	switch {
	case readErr == nil:
		if err := json.Unmarshal(data, &payload); err != nil {
			return false, false, fmt.Errorf("parse %s: %w", marketplacePath, err)
		}
	case os.IsNotExist(readErr):
		created = true
		payload["name"] = defaultMarketplaceName
		payload["interface"] = map[string]any{"displayName": defaultMarketplaceTitle}
		payload["plugins"] = []any{}
	default:
		return false, false, fmt.Errorf("read %s: %w", marketplacePath, readErr)
	}

	if _, ok := payload["name"]; !ok {
		payload["name"] = defaultMarketplaceName
		updated = true
	}

	switch iface := payload["interface"].(type) {
	case nil:
		payload["interface"] = map[string]any{"displayName": defaultMarketplaceTitle}
		updated = true
	case map[string]any:
		if created {
			if _, ok := iface["displayName"]; !ok {
				iface["displayName"] = defaultMarketplaceTitle
				updated = true
			}
		}
	default:
		return created, updated, fmt.Errorf("%s field 'interface' must be an object", marketplacePath)
	}

	pluginsValue, ok := payload["plugins"]
	if !ok {
		payload["plugins"] = []any{}
		pluginsValue = payload["plugins"]
		updated = true
	}

	pluginsList, ok := pluginsValue.([]any)
	if !ok {
		return created, updated, fmt.Errorf("%s field 'plugins' must be an array", marketplacePath)
	}

	entry := map[string]any{
		"name": CodemapPluginName,
		"source": map[string]any{
			"source": "local",
			"path":   "./plugins/codemap",
		},
		"policy": map[string]any{
			"installation":   defaultInstallationPolicy,
			"authentication": defaultAuthenticationPolicy,
		},
		"category": defaultCategory,
	}

	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return created, updated, fmt.Errorf("encode marketplace entry: %w", err)
	}

	found := false
	for i, existing := range pluginsList {
		existingMap, ok := existing.(map[string]any)
		if !ok {
			continue
		}
		if existingMap["name"] != CodemapPluginName {
			continue
		}
		found = true
		existingJSON, marshalErr := json.Marshal(existingMap)
		if marshalErr != nil || !bytes.Equal(existingJSON, entryJSON) {
			pluginsList[i] = entry
			updated = true
		}
		break
	}
	if !found {
		pluginsList = append(pluginsList, entry)
		payload["plugins"] = pluginsList
		updated = true
	}

	if !created && !updated {
		return created, updated, nil
	}

	if err := os.MkdirAll(filepath.Dir(marketplacePath), 0o755); err != nil {
		return created, updated, fmt.Errorf("create marketplace directory: %w", err)
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return created, updated, fmt.Errorf("encode %s: %w", marketplacePath, err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(marketplacePath, out, 0o644); err != nil {
		return created, updated, fmt.Errorf("write %s: %w", marketplacePath, err)
	}
	return created, updated, nil
}
