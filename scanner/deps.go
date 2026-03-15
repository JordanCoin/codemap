package scanner

import (
	"os"
	"path/filepath"
	"strings"
)

// ReadExternalDeps reads manifest files (go.mod, requirements.txt, package.json)
func ReadExternalDeps(root string) map[string][]string {
	deps := make(map[string][]string)

	// Walk tree to find all manifest files
	filepath.Walk(root, func(path string, info os.FileInfo, _ error) error {
		if info == nil {
			return nil
		}
		if info.IsDir() {
			if IgnoredDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch info.Name() {
		case "go.mod":
			if c, err := os.ReadFile(path); err == nil {
				deps["go"] = append(deps["go"], parseGoMod(string(c))...)
			}
		case "requirements.txt":
			if c, err := os.ReadFile(path); err == nil {
				deps["python"] = append(deps["python"], parseRequirements(string(c))...)
			}
		case "package.json":
			if c, err := os.ReadFile(path); err == nil {
				deps["javascript"] = append(deps["javascript"], parsePackageJson(string(c))...)
			}
		case "Podfile":
			if c, err := os.ReadFile(path); err == nil {
				deps["swift"] = append(deps["swift"], parsePodfile(string(c))...)
			}
		case "Package.swift":
			if c, err := os.ReadFile(path); err == nil {
				deps["swift"] = append(deps["swift"], parsePackageSwift(string(c))...)
			}
		case "packages.config":
			if c, err := os.ReadFile(path); err == nil {
				deps["csharp"] = append(deps["csharp"], parsePackagesConfig(string(c))...)
			}
		default:
			// .csproj files have project-specific names, so they must be matched
			// by extension rather than a fixed case above.
			if strings.HasSuffix(info.Name(), ".csproj") {
				if c, err := os.ReadFile(path); err == nil {
					deps["csharp"] = append(deps["csharp"], parseCsproj(string(c))...)
				}
			}
		}
		return nil
	})

	for k, v := range deps {
		deps[k] = dedupe(v)
	}
	return deps
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
