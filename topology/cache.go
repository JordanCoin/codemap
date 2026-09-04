package topology

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"codemap/config"
	"codemap/scanner"
)

const CacheSchemaVersion = 1

type CacheIdentity struct {
	Filters          string `json:"filters"`
	Manifests        string `json:"manifests"`
	ConfiguredFiles  string `json:"configured_files"`
	ProviderVersions string `json:"provider_versions"`
}

type CacheEnvelope struct {
	Schema      int           `json:"schema"`
	GeneratedAt time.Time     `json:"generated_at"`
	Identity    CacheIdentity `json:"identity"`
	Graph       *Graph        `json:"graph"`
}

func CachePath(root string) string {
	return CachePathAt(filepath.Join(root, ".codemap"))
}

// CachePathAt returns the topology cache path inside cacheDir.
func CachePathAt(cacheDir string) string {
	return filepath.Join(cacheDir, "topology-state.json")
}

func BuildCacheIdentity(root string, files []scanner.FileInfo, manifests []string, providers []Provider) (CacheIdentity, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return CacheIdentity{}, err
	}
	cfg := config.Load(root)
	filterData, err := json.Marshal(cfg)
	if err != nil {
		return CacheIdentity{}, err
	}

	manifestPaths := append([]string(nil), manifests...)
	sort.Strings(manifestPaths)
	manifestHash := sha256.New()
	for _, manifest := range manifestPaths {
		rel, err := normalizeRepoPathFromRoot(absRoot, manifest)
		if err != nil {
			return CacheIdentity{}, fmt.Errorf("manifest %q: %w", manifest, err)
		}
		data, err := os.ReadFile(filepath.Join(absRoot, rel))
		if err != nil {
			return CacheIdentity{}, err
		}
		writeHashPart(manifestHash, filepath.ToSlash(rel))
		writeHashBytes(manifestHash, data)
	}

	type fileIdentity struct {
		path  string
		index int
	}
	fileParts := make([]fileIdentity, 0, len(files))
	for index, file := range files {
		rel, err := normalizeRepoPathFromRoot(absRoot, file.Path)
		if err != nil {
			return CacheIdentity{}, fmt.Errorf("configured file %q: %w", file.Path, err)
		}
		fileParts = append(fileParts, fileIdentity{path: filepath.ToSlash(rel), index: index})
	}
	sort.Slice(fileParts, func(i, j int) bool {
		if fileParts[i].path != fileParts[j].path {
			return fileParts[i].path < fileParts[j].path
		}
		left, right := files[fileParts[i].index], files[fileParts[j].index]
		if left.Size != right.Size {
			return left.Size < right.Size
		}
		if left.Ext != right.Ext {
			return left.Ext < right.Ext
		}
		if left.IsNew != right.IsNew {
			return !left.IsNew
		}
		if left.Added != right.Added {
			return left.Added < right.Added
		}
		return left.Removed < right.Removed
	})
	fileHash := sha256.New()
	record := make([]byte, 0, 128)
	for _, part := range fileParts {
		file := files[part.index]
		record = record[:0]
		record = append(record, part.path...)
		record = append(record, 0)
		record = strconv.AppendInt(record, file.Size, 10)
		record = append(record, 0)
		record = append(record, file.Ext...)
		record = append(record, 0)
		record = strconv.AppendBool(record, file.IsNew)
		record = append(record, 0)
		record = strconv.AppendInt(record, int64(file.Added), 10)
		record = append(record, 0)
		record = strconv.AppendInt(record, int64(file.Removed), 10)
		writeHashBytes(fileHash, record)
	}

	providerParts := make([]string, 0, len(providers))
	for _, provider := range sortedProviders(providers) {
		providerParts = append(providerParts, provider.Name()+"="+provider.Version())
	}

	return CacheIdentity{
		Filters:          hashStrings(string(filterData)),
		Manifests:        hex.EncodeToString(manifestHash.Sum(nil)),
		ConfiguredFiles:  hex.EncodeToString(fileHash.Sum(nil)),
		ProviderVersions: hashStrings(providerParts...),
	}, nil
}

func ReadCache(root string, expected CacheIdentity) (*Graph, bool) {
	return ReadCacheAt(filepath.Join(root, ".codemap"), root, expected)
}

// ReadCacheAt reads a cache from cacheDir and validates graph paths against root.
func ReadCacheAt(cacheDir, root string, expected CacheIdentity) (*Graph, bool) {
	data, err := os.ReadFile(CachePathAt(cacheDir))
	if err != nil {
		return nil, false
	}
	var envelope CacheEnvelope
	if json.Unmarshal(data, &envelope) != nil ||
		envelope.Schema != CacheSchemaVersion ||
		envelope.Identity != expected ||
		envelope.Graph == nil {
		return nil, false
	}
	graph, ok := canonicalizeCachedGraph(root, envelope.Graph)
	if !ok || !IsCacheable(graph) {
		return nil, false
	}
	return graph, true
}

func IsCacheable(graph *Graph) bool {
	if graph == nil || graph.Coverage.Status == CoverageUnavailable {
		return false
	}
	for _, issue := range graph.Coverage.Issues {
		if issue.Code == "provider-failed" {
			return false
		}
	}
	return true
}

func WriteCache(root string, envelope CacheEnvelope) error {
	return WriteCacheAt(filepath.Join(root, ".codemap"), envelope)
}

// WriteCacheAt atomically writes a topology cache inside cacheDir.
func WriteCacheAt(cacheDir string, envelope CacheEnvelope) error {
	if envelope.Schema != CacheSchemaVersion {
		return fmt.Errorf("topology cache schema %d is unsupported", envelope.Schema)
	}
	if envelope.Graph == nil {
		return errors.New("topology cache graph is required")
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(cacheDir, ".topology-state-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, CachePathAt(cacheDir)); err != nil {
		return err
	}
	if directory, err := os.Open(cacheDir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func canonicalizeCachedGraph(root string, cached *Graph) (*Graph, bool) {
	nodes := make([]Node, 0, len(cached.Nodes))
	for _, node := range cached.Nodes {
		nodes = append(nodes, node)
	}
	var edges []Edge
	for _, outgoing := range cached.Dependencies {
		edges = append(edges, outgoing...)
	}
	members := make(map[ID][]string, len(cached.Members))
	for id, paths := range cached.Members {
		members[id] = append([]string(nil), paths...)
	}
	canonical := MergeFragments(root, []Fragment{
		{
			Provider: "cache",
			Nodes:    nodes,
			Edges:    edges,
			Members:  members,
			Coverage: cached.Coverage,
		},
	})
	for _, issue := range canonical.Coverage.Issues {
		if strings.HasPrefix(issue.Code, "invalid-") ||
			strings.HasPrefix(issue.Code, "unknown-") ||
			issue.Code == "conflicting-node-id" ||
			issue.Code == "missing-node-id" {
			return nil, false
		}
	}
	return canonical, true
}

func hashStrings(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		writeHashPart(hash, part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeHashPart(hash interface{ Write([]byte) (int, error) }, part string) {
	_, _ = hash.Write([]byte(part))
	_, _ = hash.Write([]byte{0})
}

func writeHashBytes(hash interface{ Write([]byte) (int, error) }, part []byte) {
	_, _ = hash.Write(part)
	_, _ = hash.Write([]byte{0})
}
