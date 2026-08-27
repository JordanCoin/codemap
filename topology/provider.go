package topology

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"codemap/config"
	"codemap/internal/projectpath"
	"codemap/scanner"
)

const maxManifestWalkEntries = 100_000

type ManifestSelector struct {
	Names []string
}

type Inventory struct {
	Root      string
	Files     []scanner.FileInfo
	Manifests []string
	Config    config.ProjectConfig
}

type Provider interface {
	Name() string
	Version() string
	Languages() []string
	Manifests() ManifestSelector
	Build(context.Context, Inventory) (Fragment, error)
}

var (
	providerRegistryMu sync.RWMutex
	providerRegistry   []Provider
)

func RegisterProvider(provider Provider) {
	if provider == nil || strings.TrimSpace(provider.Name()) == "" {
		panic("topology: provider name is required")
	}
	providerRegistryMu.Lock()
	defer providerRegistryMu.Unlock()
	for _, existing := range providerRegistry {
		if existing.Name() == provider.Name() {
			panic("topology: duplicate provider " + provider.Name())
		}
	}
	providerRegistry = append(providerRegistry, provider)
}

func RegisteredProviders() []Provider {
	providerRegistryMu.RLock()
	defer providerRegistryMu.RUnlock()
	return sortedProviders(providerRegistry)
}

func IsManifestPath(path string) bool {
	base := filepath.Base(path)
	for _, provider := range RegisteredProviders() {
		for _, name := range provider.Manifests().Names {
			if base == name {
				return true
			}
		}
	}
	return false
}

func BuildProjectGraph(ctx context.Context, root string) (*ProjectGraph, error) {
	return BuildProjectGraphWithProviders(ctx, root, RegisteredProviders())
}

func BuildProjectGraphWithProviders(ctx context.Context, root string, providers []Provider) (*ProjectGraph, error) {
	// The file scan completes even when the caller already cancelled, so the
	// successful graph is still returned alongside the cancellation error and
	// the provider topology step is skipped.
	files, err := scanner.BuildFileGraph(context.WithoutCancel(ctx), root, scanner.ConfiguredFilters(root))
	if err != nil {
		return nil, err
	}
	project := &ProjectGraph{Files: files}
	if err := ctx.Err(); err != nil {
		return project, err
	}
	graph, identity, err := BuildGraphWithProviders(ctx, root, providers)
	project.Topology = graph
	project.TopologyIdentity = identity
	return project, err
}

func BuildGraph(ctx context.Context, root string) (*Graph, CacheIdentity, error) {
	return BuildGraphWithProviders(ctx, root, RegisteredProviders())
}

func BuildGraphWithProviders(ctx context.Context, root string, providers []Provider) (*Graph, CacheIdentity, error) {
	if err := ctx.Err(); err != nil {
		return nil, CacheIdentity{}, err
	}
	cfg := config.Load(root)
	selected := enabledProviders(sortedProviders(providers), cfg.Only)
	if len(selected) == 0 {
		return MergeFragments(root, nil), CacheIdentity{}, nil
	}

	cache := scanner.NewGitIgnoreCache(root)
	inventoryFiles, err := scanner.ScanConfiguredFiles(ctx, root, cache)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, CacheIdentity{}, err
		}
		return unavailableGraph("inventory-failed", err.Error()), CacheIdentity{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, CacheIdentity{}, err
	}
	inventoryFiles = filterInventoryFiles(inventoryFiles, selected)
	manifests, err := discoverManifests(ctx, root, selected, cfg)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, CacheIdentity{}, err
		}
		return unavailableGraph("manifest-discovery-failed", err.Error()), CacheIdentity{}, nil
	}
	identity, err := BuildCacheIdentity(root, inventoryFiles, manifests, selected)
	if err != nil {
		return unavailableGraph("cache-identity-failed", err.Error()), CacheIdentity{}, nil
	}
	runtimeDir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		return unavailableGraph("cache-path-failed", err.Error()), CacheIdentity{}, nil
	}
	if cached, ok := ReadCacheAt(runtimeDir, root, identity); ok {
		return cached, identity, nil
	}

	inventory := Inventory{
		Root:      root,
		Files:     inventoryFiles,
		Manifests: manifests,
		Config:    cfg,
	}
	fragments := make([]Fragment, 0, len(selected))
	for _, provider := range selected {
		if err := ctx.Err(); err != nil {
			return nil, identity, err
		}
		fragment, buildErr := provider.Build(ctx, inventory)
		if buildErr != nil {
			if errors.Is(buildErr, context.Canceled) || errors.Is(buildErr, context.DeadlineExceeded) {
				return nil, identity, buildErr
			}
			fragments = append(fragments, Fragment{
				Provider: provider.Name(),
				Coverage: Coverage{
					Status: CoverageUnavailable,
					Issues: []Issue{{
						Provider: provider.Name(),
						Code:     "provider-failed",
						Message:  buildErr.Error(),
					}},
				},
			})
			continue
		}
		if fragment.Provider == "" {
			fragment.Provider = provider.Name()
		}
		if fragment.Coverage.Status == "" {
			fragment.Coverage.Status = CoverageComplete
		}
		fragments = append(fragments, fragment)
	}
	return MergeFragments(root, fragments), identity, nil
}

func discoverManifests(ctx context.Context, root string, providers []Provider, cfg config.ProjectConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, provider := range providers {
		for _, name := range provider.Manifests().Names {
			name = strings.TrimSpace(name)
			if name != "" {
				names[name] = true
			}
		}
	}
	if len(names) == 0 {
		return nil, nil
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	ignoreCache := scanner.NewGitIgnoreCache(absRoot)
	entries := 0
	var manifests []string
	err = filepath.WalkDir(absRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxManifestWalkEntries {
			return fmt.Errorf("manifest walk exceeded %d entries", maxManifestWalkEntries)
		}
		if path == absRoot {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".codemap" || ignoreCache.ShouldIgnore(path) ||
				!scanner.MatchesFilters(filepath.ToSlash(rel), "", nil, cfg.Exclude) {
				return filepath.SkipDir
			}
			ignoreCache.EnsureDir(path)
			return nil
		}
		if !names[entry.Name()] || ignoreCache.ShouldIgnore(path) ||
			!scanner.MatchesFilters(filepath.ToSlash(rel), filepath.Ext(rel), nil, cfg.Exclude) {
			return nil
		}
		manifests = append(manifests, filepath.Clean(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(manifests)
	return manifests, nil
}

func enabledProviders(providers []Provider, only []string) []Provider {
	if len(only) == 0 {
		return providers
	}
	allowed := make(map[string]bool, len(only))
	for _, language := range only {
		language = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(language)), ".")
		if language != "" {
			allowed[language] = true
		}
	}
	var enabled []Provider
	for _, provider := range providers {
		for _, language := range provider.Languages() {
			language = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(language)), ".")
			if allowed[language] {
				enabled = append(enabled, provider)
				break
			}
		}
	}
	return enabled
}

func filterInventoryFiles(files []scanner.FileInfo, providers []Provider) []scanner.FileInfo {
	languages := make(map[string]bool)
	for _, provider := range providers {
		for _, language := range provider.Languages() {
			language = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(language)), ".")
			if language != "" {
				languages[language] = true
			}
		}
	}
	filtered := make([]scanner.FileInfo, 0, len(files))
	for _, file := range files {
		language := strings.ToLower(scanner.DetectLanguage(file.Path))
		extension := strings.TrimPrefix(strings.ToLower(file.Ext), ".")
		if languages[language] || languages[extension] {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

func sortedProviders(providers []Provider) []Provider {
	result := append([]Provider(nil), providers...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name() != result[j].Name() {
			return result[i].Name() < result[j].Name()
		}
		return result[i].Version() < result[j].Version()
	})
	return result
}

func unavailableGraph(code, message string) *Graph {
	graph := MergeFragments("", nil)
	graph.Coverage.Issues = []Issue{{Code: code, Message: message}}
	return graph
}
