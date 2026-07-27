package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	integrationOSExecutable = os.Executable
	integrationArgs0        = func() string { return os.Args[0] }
	integrationLookPath     = exec.LookPath
)

func resolveIntegrationExecutable() (string, error) {
	running, err := integrationOSExecutable()
	if err != nil {
		return "", fmt.Errorf("resolve running codemap executable: %w", err)
	}

	invoked := integrationArgs0()
	if invoked != "" && !filepath.IsAbs(invoked) {
		if resolved, lookErr := integrationLookPath(invoked); lookErr == nil {
			invoked = resolved
		} else {
			invoked = ""
		}
	}
	return resolveIntegrationExecutableFrom(running, invoked, runtime.GOOS)
}

func resolveIntegrationExecutableFrom(running, invoked, goos string) (string, error) {
	running, err := filepath.Abs(running)
	if err != nil {
		return "", fmt.Errorf("make running codemap executable absolute: %w", err)
	}
	runningInfo, err := validateIntegrationExecutable(running, goos)
	if err != nil {
		return "", err
	}

	if invoked == "" {
		return running, nil
	}
	invoked, err = filepath.Abs(invoked)
	if err != nil {
		return running, nil
	}
	invokedInfo, err := validateIntegrationExecutable(invoked, goos)
	if err == nil && os.SameFile(runningInfo, invokedInfo) {
		return invoked, nil
	}
	return running, nil
}

func validateIntegrationExecutable(path, goos string) (os.FileInfo, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("codemap executable path is not absolute: %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect codemap executable %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("codemap executable %q is not a regular file", path)
	}
	if goos != "windows" && info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("codemap executable %q is not executable", path)
	}
	return info, nil
}

func quoteHookExecutable(path, goos string) string {
	if goos == "windows" {
		return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
	}
	return `'` + strings.ReplaceAll(path, `'`, `'"'"'`) + `'`
}

func managedMCPArgs(version, integration string) []string {
	return []string{"mcp", "--configured-version", version, "--integration", integration}
}
