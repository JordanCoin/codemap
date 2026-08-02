package scanner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MCPManifestByteBudget bounds each manifest read performed for one MCP request.
const MCPManifestByteBudget int64 = 1 << 20

var errManifestBudgetExceeded = errors.New("manifest exceeds byte budget")

// ReadExternalDeps reads manifest files (go.mod, requirements.txt, package.json)
func ReadExternalDeps(root string) map[string][]string {
	deps, _ := ReadExternalDepsContext(context.Background(), root, 0)
	if deps == nil {
		return make(map[string][]string)
	}
	return deps
}

// ReadExternalDepsContext reads external dependencies with caller cancellation.
// A positive manifestByteBudget skips individual oversized manifests; zero keeps
// the legacy unbounded behavior used by CLI and blast-radius callers.
func ReadExternalDepsContext(ctx context.Context, root string, manifestByteBudget int64) (map[string][]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deps := make(map[string][]string)

	// Walk tree to find all manifest files
	err := filepath.Walk(root, func(path string, info os.FileInfo, _ error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if info == nil {
			return nil
		}
		if info.IsDir() {
			if IgnoredDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isDependencyManifest(info.Name()) {
			return nil
		}
		content, err := readManifestContext(ctx, path, info.Size(), manifestByteBudget)
		if errors.Is(err, errManifestBudgetExceeded) {
			return nil
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return nil
		}
		switch info.Name() {
		case "go.mod":
			deps["go"] = append(deps["go"], parseGoMod(string(content))...)
		case "requirements.txt":
			deps["python"] = append(deps["python"], parseRequirements(string(content))...)
		case "package.json":
			deps["javascript"] = append(deps["javascript"], parsePackageJson(string(content))...)
		case "Podfile":
			deps["swift"] = append(deps["swift"], parsePodfile(string(content))...)
		case "Package.swift":
			deps["swift"] = append(deps["swift"], parsePackageSwift(string(content))...)
		case "packages.config":
			deps["csharp"] = append(deps["csharp"], parsePackagesConfig(string(content))...)
		default:
			if strings.HasSuffix(info.Name(), ".csproj") {
				deps["csharp"] = append(deps["csharp"], parseCsproj(string(content))...)
			}
		}
		return nil
	})

	for k, v := range deps {
		deps[k] = dedupe(v)
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return deps, nil
}

func isDependencyManifest(name string) bool {
	switch name {
	case "go.mod", "requirements.txt", "package.json", "Podfile", "Package.swift", "packages.config":
		return true
	default:
		return strings.HasSuffix(name, ".csproj")
	}
}

func readManifestContext(ctx context.Context, path string, size, budget int64) ([]byte, error) {
	if budget > 0 && size > budget {
		return nil, errManifestBudgetExceeded
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := io.Reader(f)
	if budget > 0 {
		reader = io.LimitReader(f, budget+1)
	}
	data := make([]byte, 0, min(max(size, 0), budgetCapacity(budget)))
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := reader.Read(buf)
		data = append(data, buf[:n]...)
		if budget > 0 && int64(len(data)) > budget {
			return nil, errManifestBudgetExceeded
		}
		if errors.Is(readErr, io.EOF) {
			return data, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

func budgetCapacity(budget int64) int64 {
	if budget <= 0 {
		return 64 * 1024
	}
	return budget
}

func parseGoMod(c string) (deps []string) {
	inReq := false
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "require (") {
			inReq = true
		} else if inReq && line == ")" {
			inReq = false
		} else if inReq && line != "" && !strings.HasPrefix(line, "//") {
			if parts := strings.Fields(line); len(parts) >= 1 {
				deps = append(deps, parts[0])
			}
		}
	}
	return
}

func parseRequirements(c string) (deps []string) {
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, sep := range []string{"==", ">=", "<=", "~=", "<", ">", "[", ";", "#"} {
			if i := strings.Index(line, sep); i != -1 {
				line = line[:i]
			}
		}
		if line != "" {
			deps = append(deps, line)
		}
	}
	return
}

func parsePackageJson(c string) (deps []string) {
	inDeps := false
	for _, line := range strings.Split(c, "\n") {
		if strings.Contains(line, `"dependencies"`) || strings.Contains(line, `"devDependencies"`) {
			inDeps = true
		} else if inDeps && strings.Contains(line, "}") {
			inDeps = false
		} else if inDeps && strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			name := strings.Trim(strings.TrimSpace(parts[0]), `"`)
			if name != "" {
				deps = append(deps, name)
			}
		}
	}
	return
}

func parsePodfile(c string) (deps []string) {
	// Parse Podfile: pod 'Name' or pod 'Name', '~> 1.0'
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "pod ") {
			// Extract pod name from: pod 'Name' or pod 'Name', ...
			line = strings.TrimPrefix(line, "pod ")
			line = strings.Trim(line, "'\"")
			if i := strings.Index(line, "'"); i != -1 {
				line = line[:i]
			}
			if i := strings.Index(line, "\""); i != -1 {
				line = line[:i]
			}
			if i := strings.Index(line, ","); i != -1 {
				line = line[:i]
			}
			line = strings.Trim(line, "'\" ")
			if line != "" {
				deps = append(deps, line)
			}
		}
	}
	return
}

func parsePackageSwift(c string) (deps []string) {
	// Parse Package.swift: .package(url: "...", ...) or .package(name: "Name", ...)
	// Look for package names in .product(name: "Name", package: "Package")
	for _, line := range strings.Split(c, "\n") {
		// Match .package(url: "https://github.com/user/repo", ...)
		if strings.Contains(line, ".package(") && strings.Contains(line, "url:") {
			// Extract repo name from URL
			if i := strings.Index(line, "url:"); i != -1 {
				rest := line[i+4:]
				rest = strings.Trim(rest, " \"'")
				if j := strings.Index(rest, "\""); j != -1 {
					url := rest[:j]
					// Get repo name from URL
					parts := strings.Split(url, "/")
					if len(parts) > 0 {
						name := parts[len(parts)-1]
						name = strings.TrimSuffix(name, ".git")
						if name != "" {
							deps = append(deps, name)
						}
					}
				}
			}
		}
	}
	return
}

// parseCsproj extracts NuGet package names from a .csproj file.
// It handles <PackageReference Include="Name" ... /> elements.
func parseCsproj(c string) (deps []string) {
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "<PackageReference") {
			continue
		}
		// Match Include="PackageName" (double or single quotes)
		for _, attr := range []string{`Include="`, `Include='`} {
			if start := strings.Index(line, attr); start >= 0 {
				rest := line[start+len(attr):]
				quote := string(attr[len(attr)-1])
				if end := strings.Index(rest, quote); end >= 0 {
					name := rest[:end]
					if name != "" {
						deps = append(deps, name)
					}
				}
				break
			}
		}
	}
	return
}

// parsePackagesConfig extracts NuGet package names from a packages.config file.
// It handles <package id="Name" ... /> elements.
func parsePackagesConfig(c string) (deps []string) {
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "<package") {
			continue
		}
		// Match id="PackageName" (double or single quotes)
		for _, attr := range []string{`id="`, `id='`} {
			if start := strings.Index(line, attr); start >= 0 {
				rest := line[start+len(attr):]
				quote := string(attr[len(attr)-1])
				if end := strings.Index(rest, quote); end >= 0 {
					name := rest[:end]
					if name != "" {
						deps = append(deps, name)
					}
				}
				break
			}
		}
	}
	return
}
