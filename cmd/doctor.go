package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codemap/config"
	"codemap/internal/buildinfo"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pelletier/go-toml/v2"
)

var doctorLookPath = exec.LookPath

var (
	doctorVersionTimeout         = 5 * time.Second
	doctorMCPTimeout             = 5 * time.Second
	doctorWaitDelay              = 100 * time.Millisecond
	doctorVersionProbe           = probeDoctorVersion
	doctorMCPProbe               = probeDoctorMCP
	doctorRuntimeGOOS            = runtime.GOOS
	doctorDesktopCodexCandidates = []string{
		"/Applications/ChatGPT.app/Contents/Resources/codex",
		"/Applications/Codex.app/Contents/Resources/codex",
	}
	doctorWindowsBundledCodexCLICandidates = discoverWindowsBundledCodexCLICandidates
	doctorRuntimeVersionProbe              = probeDoctorCodexRuntimeVersion
)

const doctorProbeOutputLimit = 8 * 1024

type doctorManagedLaunch struct {
	command           string
	args              []string
	configuredVersion string
	integration       string
}

// RunDoctor validates the selected local or global agent integrations without
// changing project or user configuration. Its return value is a process exit
// code: zero only when every integration selected for validation is usable.
func RunDoctor(args []string, defaultRoot string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agent := fs.String("agent", "", "Agent integration to check (claude or codex; default: installed agents)")
	global := fs.Bool("global", false, "Check user-scoped agent configuration")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Println("Usage: codemap doctor [--global] [--agent claude|codex] [path]")
		return 0
	} else if err != nil || fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "Usage: codemap doctor [--global] [--agent claude|codex] [path]")
		return 2
	}
	selected := strings.ToLower(strings.TrimSpace(*agent))
	if selected != "" && selected != "claude" && selected != "codex" {
		fmt.Fprintln(os.Stderr, "Error: --agent must be claude or codex")
		return 2
	}

	root := defaultRoot
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	}
	root, _, err := ResolveNearestGitRoot(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		return 1
	}
	root, err = ValidateProjectPath(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		return 1
	}

	failures := 0
	checkExecutable := func(label, name string, required bool) (bool, string) {
		if path, err := doctorLookPath(name); err == nil {
			fmt.Printf("OK   %s executable is installed\n", label)
			return true, path
		}
		if required {
			fmt.Printf("MISS %s executable is not installed\n", label)
			failures++
		} else {
			fmt.Printf("SKIP %s executable is not installed\n", label)
		}
		return false, ""
	}
	checkFile := func(label, path string, validate func(string) error) {
		if err := validate(path); err != nil {
			fmt.Printf("MISS %s: %s (%v)\n", label, path, err)
			failures++
			return
		}
		fmt.Printf("OK   %s: %s\n", label, path)
	}

	checkFile("project config", config.ConfigPath(root), validateJSONFile)
	claudeSettings, claudeSettingsErr := claudeSettingsPath(root, *global)
	claudeMCP, claudeMCPErr := claudeMCPPath(root, *global)
	codexHooks, codexHooksErr := codexHooksPath(root, *global)
	codexMCP, codexMCPErr := codexConfigPath(root, *global)
	// An active Codex plugin manifest supersedes the project config, but never
	// the user config, which remains the fallback scope below.
	codexMCPScopes := doctorScopesFor(root, *global, codexConfigPath)
	if !*global && codexMCPScopes[0].err == nil && !codexProjectMCPOverridesPlugin(codexMCPScopes[0].path) {
		if pluginMCP, ok := activeCodexPluginMCPPath(); ok {
			codexMCP = pluginMCP
			codexMCPScopes[0] = doctorScope{name: "plugin", path: pluginMCP, validate: validateCodexPluginMCP}
		}
	}
	claudeConfigured := doctorAnyConfigured(claudeSettings, claudeSettingsErr, claudeMCP, claudeMCPErr)
	codexConfigured := doctorAnyConfigured(codexHooks, codexHooksErr, codexMCP, codexMCPErr)
	anyConfigured := claudeConfigured || codexConfigured

	claudeAvailable := false
	codexAvailable := false
	codexDesktopAvailable := false
	if selected == "" || selected == "claude" {
		claudeAvailable, _ = checkExecutable("Claude", "claude", selected == "claude")
	}
	if selected == "" || selected == "codex" {
		var codexCLIPath string
		codexAvailable, codexCLIPath = checkExecutable("Codex", "codex", selected == "codex")
		codexDesktopAvailable = reportCodexRuntimeVersions(codexCLIPath)
	}
	if selected == "" && !claudeAvailable && !codexAvailable && !codexDesktopAvailable && !claudeConfigured && !codexConfigured {
		fmt.Println("MISS no supported coding agent is installed or configured")
		failures++
	}

	if selected == "claude" || (selected == "" && (claudeConfigured || (!anyConfigured && claudeAvailable))) {
		checkScopedFile("Claude hooks", doctorScopesFor(root, *global, claudeSettingsPath), func(path string) error {
			return validateHooks(path, recommendedClaudeHooks)
		}, &failures)
		checkScopedFile("Claude MCP", claudeMCPScopes(root, *global), validateClaudeMCP, &failures)
	}
	if selected == "codex" || (selected == "" && (codexConfigured || (!anyConfigured && (codexAvailable || codexDesktopAvailable)))) {
		checkScopedFile("Codex hooks", doctorScopesFor(root, *global, codexHooksPath), func(path string) error {
			return validateHooks(path, recommendedCodexHooks)
		}, &failures)
		if codexAvailable && codexHooksErr == nil && validateHooks(codexHooks, recommendedCodexHooks) == nil {
			if err := validateCodexHookTrust(root); err != nil {
				fmt.Printf("MISS Codex hook trust: %v\n", err)
				failures++
			} else {
				fmt.Println("OK   Codex hook trust: all Codemap hooks are enabled and runnable")
			}
		}
		checkScopedFile("Codex MCP", codexMCPScopes, validateCodexMCP, &failures)
	}

	if failures > 0 {
		fmt.Printf("\n%d check(s) need attention.\n", failures)
		return 1
	}
	fmt.Println("\nCodemap integration prerequisites are valid.")
	return 0
}

func reportCodexRuntimeVersions(cliPath string) bool {
	var desktopPaths []string
	switch doctorRuntimeGOOS {
	case "darwin":
		desktopPaths = validatedDoctorDesktopCodexCandidates(doctorDesktopCodexCandidates, "darwin")
	case "windows":
		desktopPaths = validatedDoctorDesktopCodexCandidates(doctorWindowsBundledCodexCLICandidates(), "windows")
	default:
		return false
	}
	if len(desktopPaths) == 0 {
		return false
	}

	if cliPath != "" {
		cliVersion, err := doctorRuntimeVersionProbe(cliPath)
		if err != nil {
			fmt.Printf("WARN Codex CLI runtime: %s (%v)\n", cliPath, err)
		} else {
			fmt.Printf("OK   Codex CLI runtime: %s (%s)\n", cliPath, cliVersion)
		}
	}
	for _, desktopPath := range desktopPaths {
		if doctorRuntimeGOOS == "windows" {
			appPath := filepath.Join(filepath.Dir(filepath.Dir(desktopPath)), "ChatGPT.exe")
			if _, err := validateIntegrationExecutable(appPath, "windows"); err == nil {
				fmt.Printf("OK   Codex Desktop app: %s\n", appPath)
			}
		}
		desktopVersion, err := doctorRuntimeVersionProbe(desktopPath)
		if err != nil {
			fmt.Printf("WARN Codex Desktop runtime: %s (%v)\n", desktopPath, err)
			continue
		}
		fmt.Printf("OK   Codex Desktop runtime: %s (%s)\n", desktopPath, desktopVersion)
	}
	return true
}

func validatedDoctorDesktopCodexCandidates(candidates []string, goos string) []string {
	desktopPaths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, err := validateIntegrationExecutable(candidate, goos); err == nil {
			desktopPaths = append(desktopPaths, candidate)
		}
	}
	return desktopPaths
}

func discoverWindowsBundledCodexCLICandidates() []string {
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		return nil
	}
	pattern := filepath.Join(programFiles, "WindowsApps", "OpenAI.Codex_*", "app", "resources", "codex.exe")
	candidates, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return candidates
}

func activeCodexPluginMCPPath() (string, bool) {
	out, err := runCodexPluginCommand("", "plugin", "list", "--json")
	if err != nil {
		return "", false
	}
	var list codexPluginList
	if err := json.Unmarshal(out, &list); err != nil {
		return "", false
	}
	for _, plugin := range list.Installed {
		if strings.HasPrefix(plugin.PluginID, "codemap@") && plugin.Installed && plugin.Enabled && filepath.IsAbs(plugin.Source.Path) {
			return filepath.Join(plugin.Source.Path, ".mcp.json"), true
		}
	}
	return "", false
}

func codexProjectMCPOverridesPlugin(path string) bool {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	payload := map[string]any{}
	if err := toml.Unmarshal(data, &payload); err != nil {
		return true
	}
	servers, _ := payload["mcp_servers"].(map[string]any)
	_, exists := servers["codemap"]
	return exists
}

func doctorAnyConfigured(firstPath string, firstErr error, secondPath string, secondErr error) bool {
	for _, candidate := range []struct {
		path string
		err  error
	}{{firstPath, firstErr}, {secondPath, secondErr}} {
		if candidate.err == nil {
			if _, statErr := os.Stat(candidate.path); statErr == nil {
				return true
			}
		}
	}
	return false
}

// doctorScope is one configuration location a check may be satisfied from.
// validate overrides the check's default validator when a scope stores the
// same setting in a different format, as the Codex plugin manifest does.
type doctorScope struct {
	name     string
	path     string
	err      error
	validate func(string) error
}

// checkScopedFile validates a check against each scope in order and reports the
// first that satisfies it, naming the scope that did.
//
// Agents merge user-level configuration into every project, so a hook or MCP
// server defined in the user scope genuinely applies to this project. Reporting
// it MISS because the project file does not repeat it describes the file layout
// rather than the effective configuration. When every scope fails, the failure
// reported is chosen by the rule described below rather than by scope order.
func checkScopedFile(label string, scopes []doctorScope, validate func(string) error, failures *int) {
	// When every scope fails, prefer reporting one that exists but is wired up
	// wrong over one whose file is simply absent: the former is the user's
	// actual problem, the latter is the normal state of an unused scope.
	var misconfigured, absent *doctorScope
	var misconfiguredErr, absentErr error
	remember := func(scope *doctorScope, err error) {
		if errors.Is(err, os.ErrNotExist) {
			if absent == nil {
				absent, absentErr = scope, err
			}
			return
		}
		if misconfigured == nil {
			misconfigured, misconfiguredErr = scope, err
		}
	}

	for index := range scopes {
		scope := scopes[index]
		if scope.err != nil {
			remember(&scopes[index], fmt.Errorf("could not resolve path (%w)", scope.err))
			continue
		}
		validateScope := validate
		if scope.validate != nil {
			validateScope = scope.validate
		}
		if err := validateScope(scope.path); err != nil {
			remember(&scopes[index], err)
			continue
		}
		fmt.Printf("OK   %s: %s (%s scope)\n", label, scope.path, scope.name)
		return
	}

	(*failures)++
	switch {
	case misconfigured != nil:
		fmt.Printf("MISS %s: %s (%v)\n", label, misconfigured.path, misconfiguredErr)
	case absent != nil:
		fmt.Printf("MISS %s: %s (%v)\n", label, absent.path, absentErr)
	default:
		fmt.Printf("MISS %s: no configuration scope available\n", label)
	}
}

// claudeMCPScopes returns the three places Claude Code loads MCP servers from,
// in the order it resolves them: the committed project .mcp.json, the local
// per-project entry inside the user-level ~/.claude.json (what `claude mcp add`
// writes by default), and that file's top-level user-wide servers.
func claudeMCPScopes(root string, global bool) []doctorScope {
	scopes := doctorScopesFor(root, global, claudeMCPPath)
	if global {
		return scopes
	}
	userPath, userErr := claudeMCPPath(root, true)
	local := doctorScope{name: "local", path: userPath, err: userErr, validate: validateClaudeLocalMCP(root)}
	return []doctorScope{scopes[0], local, scopes[1]}
}

// doctorScopesFor returns the scopes a check should consult. With --global the
// user scope is the explicit subject of the check, so project files must not
// satisfy it; otherwise the project scope is preferred and the user scope acts
// as the fallback that reflects what the agent actually loads.
func doctorScopesFor(root string, global bool, resolve func(string, bool) (string, error)) []doctorScope {
	userPath, userErr := resolve(root, true)
	userScope := doctorScope{name: "user", path: userPath, err: userErr}
	if global {
		return []doctorScope{userScope}
	}
	projectPath, projectErr := resolve(root, false)
	return []doctorScope{{name: "project", path: projectPath, err: projectErr}, userScope}
}

func validateJSONFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func validateHooks(path string, specs []claudeHookSpec) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	raw, ok := root["hooks"]
	if !ok {
		return fmt.Errorf("missing hooks object")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	hooks := map[string][]claudeHookEntry{}
	if err := json.Unmarshal(encoded, &hooks); err != nil {
		return fmt.Errorf("invalid hooks: %w", err)
	}
	for _, spec := range specs {
		if !hookSpecSatisfied(hooks[spec.Event], spec) {
			return fmt.Errorf("missing %s hook %q", spec.Event, spec.Command)
		}
	}
	return nil
}

// hookSpecSatisfied reports whether entries already run the codemap subcommand
// that spec describes.
//
// This deliberately differs from hasHookSpec, which demands byte-identity
// because setup uses it to decide which entries it owns and may rewrite. Doctor
// answers a weaker question — "is this hook wired up?" — so it accepts any
// spelling that runs the same thing: PATH-resolved or absolute executable,
// quoted or bare, with or without the --integration ownership tag. Requiring
// the canonical spelling here reports working installations as broken.
func hookSpecSatisfied(entries []claudeHookEntry, spec claudeHookSpec) bool {
	requiredMatcher := strings.TrimSpace(spec.Matcher)
	for _, entry := range entries {
		if requiredMatcher != "" && !strings.EqualFold(strings.TrimSpace(entry.Matcher), requiredMatcher) {
			continue
		}
		for _, hook := range entry.Hooks {
			if !strings.EqualFold(strings.TrimSpace(hook.Type), "command") {
				continue
			}
			if hookCommandSatisfies(hook.Command, spec.Command) {
				return true
			}
		}
	}
	return false
}

// hookCommandSatisfies reports whether existing invokes the same codemap
// subcommand as target.
func hookCommandSatisfies(existing, target string) bool {
	existingExecutable, existingArgs, ok := splitHookCommand(existing)
	if !ok {
		return false
	}
	_, targetArgs, ok := splitHookCommand(target)
	if !ok {
		return false
	}
	return existingArgs == targetArgs && hookExecutableIsCodemap(existingExecutable)
}

// splitHookCommand separates a hook command into its executable and the codemap
// argument list beginning at "hook", with the --integration tag removed.
//
// Dropping --integration is safe because it never changes behavior: main.go
// parses it, checks it agrees with the agent, then discards it. --agent does
// change behavior and is preserved, so a Claude hook cannot satisfy a Codex spec.
func splitHookCommand(command string) (executable, args string, ok bool) {
	command = strings.TrimSpace(command)
	index := strings.Index(command, " hook ")
	if index < 0 {
		return "", "", false
	}
	executable, ok = unquoteHookExecutable(strings.TrimSpace(command[:index]))
	if !ok {
		return "", "", false
	}
	fields := strings.Fields(command[index:])
	kept := fields[:0]
	for _, field := range fields {
		if strings.HasPrefix(field, "--integration=") {
			continue
		}
		kept = append(kept, field)
	}
	return executable, strings.Join(kept, " "), true
}

// hookExecutableIsCodemap reports whether a hook executable names a codemap
// binary, accepting POSIX and Windows path separators, drive-letter paths, and
// the .exe suffix.
func hookExecutableIsCodemap(path string) bool {
	normalized := strings.ReplaceAll(path, `\`, "/")
	name := strings.ToLower(normalized[strings.LastIndex(normalized, "/")+1:])
	return name == "codemap" || name == "codemap.exe"
}

func validateClaudeMCP(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	servers, ok := payload["mcpServers"].(map[string]any)
	if !ok {
		return fmt.Errorf("field 'mcpServers' must be an object")
	}
	server, ok := servers["codemap"]
	if !ok {
		return fmt.Errorf("missing codemap MCP server; repair with `codemap setup --agent claude`")
	}
	return validateDoctorMCPServer(server, "claude")
}

// validateClaudeLocalMCP validates the server registered for one project inside
// the user-level ~/.claude.json, which is where `claude mcp add` writes by
// default. The entry lives under projects[<root>].mcpServers rather than the
// top-level mcpServers object that validateClaudeMCP reads.
func validateClaudeLocalMCP(projectRoot string) func(string) error {
	return func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		payload := map[string]any{}
		if err := json.Unmarshal(data, &payload); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		projects, ok := payload["projects"].(map[string]any)
		if !ok {
			return fmt.Errorf("no project-scoped MCP servers registered")
		}
		project, ok := projects[projectRoot].(map[string]any)
		if !ok {
			return fmt.Errorf("no MCP servers registered for %s", projectRoot)
		}
		servers, ok := project["mcpServers"].(map[string]any)
		if !ok {
			return fmt.Errorf("no MCP servers registered for %s", projectRoot)
		}
		server, ok := servers["codemap"]
		if !ok {
			return fmt.Errorf("missing codemap MCP server; repair with `codemap setup --agent claude`")
		}
		return validateDoctorMCPServer(server, "claude")
	}
}

func validateCodexMCP(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	payload := map[string]any{}
	if err := toml.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("invalid TOML: %w", err)
	}
	servers, ok := payload["mcp_servers"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing mcp_servers table")
	}
	server, ok := servers["codemap"]
	if !ok {
		return fmt.Errorf("missing codemap MCP server; repair with `codemap setup --agent codex`")
	}
	return validateDoctorMCPServer(server, "codex")
}

func validateCodexPluginMCP(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	servers, ok := payload["mcpServers"].(map[string]any)
	if !ok {
		return fmt.Errorf("field 'mcpServers' must be an object")
	}
	server, ok := servers["codemap"]
	if !ok {
		return fmt.Errorf("missing codemap MCP server; repair with `codemap plugin install`")
	}
	return validateDoctorMCPServer(server, "codex")
}

func validateDoctorMCPServer(raw any, agent string) error {
	launch, err := parseDoctorManagedLaunch(raw, agent)
	if err != nil {
		return fmt.Errorf("%w; repair with `%s`", err, doctorRepairCommand(agent, launch.integration))
	}
	if _, err := validateDoctorExecutable(launch.command, runtime.GOOS); err != nil {
		return fmt.Errorf("%w; repair with `%s`", err, doctorRepairCommand(agent, launch.integration))
	}
	if err := doctorVersionProbe(launch); err != nil {
		return fmt.Errorf("version probe failed for %q: %w; repair with `%s`", launch.command, err, doctorRepairCommand(agent, launch.integration))
	}
	if err := doctorMCPProbe(launch); err != nil {
		return fmt.Errorf("MCP initialize probe failed for %q: %w; repair with `%s`", launch.command, err, doctorRepairCommand(agent, launch.integration))
	}
	return nil
}

func parseDoctorManagedLaunch(raw any, agent string) (doctorManagedLaunch, error) {
	server, ok := raw.(map[string]any)
	if !ok {
		return doctorManagedLaunch{}, fmt.Errorf("codemap MCP server must be an object")
	}
	command, ok := server["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return doctorManagedLaunch{}, fmt.Errorf("codemap MCP command must be a non-empty string")
	}
	args := mcpServerArgs(server)
	_, managedArgs, err := ParseGlobalRootOptions(args)
	if err != nil {
		return doctorManagedLaunch{}, fmt.Errorf("invalid codemap root arguments: %w", err)
	}
	launch := doctorManagedLaunch{command: command, args: args, integration: doctorManagedIntegration(managedArgs)}
	if command == "codemap" && stringSlicesEqual(args, []string{"mcp"}) {
		return launch, fmt.Errorf("legacy PATH-relative codemap MCP definition is stale")
	}
	if len(managedArgs) != 5 || managedArgs[0] != "mcp" || managedArgs[1] != "--configured-version" || managedArgs[2] == "" || managedArgs[3] != "--integration" {
		return launch, fmt.Errorf("unrecognized codemap MCP arguments")
	}
	launch.configuredVersion = managedArgs[2]
	if !filepath.IsAbs(command) {
		return launch, fmt.Errorf("codemap MCP command is not absolute: %q", command)
	}
	switch agent {
	case "claude":
		if launch.integration != "claude-setup" {
			return launch, fmt.Errorf("unrecognized Claude integration %q", launch.integration)
		}
	case "codex":
		if launch.integration != "codex-setup" && launch.integration != "codex-plugin" {
			return launch, fmt.Errorf("unrecognized Codex integration %q", launch.integration)
		}
	default:
		return launch, fmt.Errorf("unsupported agent %q", agent)
	}
	return launch, nil
}

func doctorManagedIntegration(args []string) string {
	if len(args) >= 5 && args[0] == "mcp" && args[1] == "--configured-version" && args[3] == "--integration" {
		switch args[4] {
		case "claude-setup", "codex-setup", "codex-plugin":
			return args[4]
		}
	}
	return ""
}

func validateDoctorExecutable(path, goos string) (os.FileInfo, error) {
	info, err := validateIntegrationExecutable(path, goos)
	if err != nil {
		return nil, err
	}
	if goos == "windows" {
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".exe" && ext != ".com" {
			return nil, fmt.Errorf("codemap executable %q does not have a Windows executable extension", path)
		}
	}
	return info, nil
}

func doctorRepairCommand(agent, integration string) string {
	if agent == "codex" && integration == "codex-plugin" {
		return "codemap plugin install"
	}
	return "codemap setup --agent " + agent
}

func probeDoctorVersion(launch doctorManagedLaunch) error {
	version, err := probeDoctorExecutableVersion(launch.command, "codemap")
	if err != nil {
		return err
	}
	if !buildinfo.Equal(version, launch.configuredVersion) {
		return fmt.Errorf("configured version %q does not match executable version %q", launch.configuredVersion, version)
	}
	return nil
}

func probeDoctorCodexRuntimeVersion(command string) (string, error) {
	return probeDoctorExecutableVersion(command, "codex-cli")
}

func probeDoctorExecutableVersion(command, product string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), doctorVersionTimeout)
	defer cancel()
	cmd := newDoctorCommand(ctx, command, "--version")
	var stdout, stderr doctorBoundedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}
	err := cmd.Wait()
	_ = killDoctorProcess(cmd)
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("timed out after %s%s", doctorVersionTimeout, doctorStderrDetail(&stderr))
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return "", fmt.Errorf("timed out waiting for process output after %s%s", doctorWaitDelay, doctorStderrDetail(&stderr))
	}
	if err != nil {
		return "", fmt.Errorf("%v%s", err, doctorStderrDetail(&stderr))
	}
	fields := strings.Fields(stdout.String())
	if len(fields) != 2 || fields[0] != product {
		return "", fmt.Errorf("unexpected stdout %q", strings.TrimSpace(stdout.String()))
	}
	return fields[1], nil
}

func probeDoctorMCP(launch doctorManagedLaunch) error {
	ctx, cancel := context.WithTimeout(context.Background(), doctorMCPTimeout)
	defer cancel()
	cmd := newDoctorCommand(ctx, launch.command, launch.args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = stdout.Close()
		return fmt.Errorf("open stdin: %w", err)
	}
	var stderr doctorBoundedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("start: %w", err)
	}
	waited := false
	cleanup := func(force bool) error {
		_ = stdin.Close()
		_ = stdout.Close()
		if force {
			_ = killDoctorProcess(cmd)
		}
		if waited {
			return nil
		}
		waited = true
		err := cmd.Wait()
		_ = killDoctorProcess(cmd)
		return err
	}
	defer func() {
		if !waited {
			_ = cleanup(true)
		}
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "codemap-doctor", Version: buildinfo.Current()}, nil)
	reader := &doctorBoundedReader{reader: stdout, remaining: doctorProbeOutputLimit}
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: reader, Writer: stdin}, nil)
	if err != nil {
		_ = cleanup(true)
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s%s", doctorMCPTimeout, doctorStderrDetail(&stderr))
		}
		return fmt.Errorf("%w%s", err, doctorStderrDetail(&stderr))
	}
	if err := session.Close(); err != nil {
		_ = cleanup(true)
		return fmt.Errorf("close session: %w%s", err, doctorStderrDetail(&stderr))
	}
	if err := cleanup(false); err != nil {
		if ctx.Err() == context.DeadlineExceeded || errors.Is(err, exec.ErrWaitDelay) {
			return fmt.Errorf("timed out after %s%s", doctorMCPTimeout, doctorStderrDetail(&stderr))
		}
		return fmt.Errorf("wait: %w%s", err, doctorStderrDetail(&stderr))
	}
	return nil
}

func newDoctorCommand(ctx context.Context, command string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.WaitDelay = doctorWaitDelay
	configureDoctorProcess(cmd)
	return cmd
}

type doctorBoundedBuffer struct {
	head  []byte
	tail  []byte
	total int
}

func (b *doctorBoundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.total += n
	headLimit := doctorProbeOutputLimit / 2
	if len(b.head) < headLimit {
		keep := headLimit - len(b.head)
		if keep > len(p) {
			keep = len(p)
		}
		b.head = append(b.head, p[:keep]...)
		p = p[keep:]
	}
	if len(p) > 0 {
		tailLimit := doctorProbeOutputLimit - headLimit
		if len(p) >= tailLimit {
			b.tail = append(b.tail[:0], p[len(p)-tailLimit:]...)
		} else {
			overflow := len(b.tail) + len(p) - tailLimit
			if overflow > 0 {
				copy(b.tail, b.tail[overflow:])
				b.tail = b.tail[:len(b.tail)-overflow]
			}
			b.tail = append(b.tail, p...)
		}
	}
	return n, nil
}

func (b *doctorBoundedBuffer) String() string {
	if b.total <= len(b.head)+len(b.tail) {
		return string(append(append([]byte(nil), b.head...), b.tail...))
	}
	return string(b.head) + fmt.Sprintf("\n... %d bytes of output truncated ...\n", b.total-len(b.head)-len(b.tail)) + string(b.tail)
}

func doctorStderrDetail(stderr *doctorBoundedBuffer) string {
	text := strings.TrimSpace(stderr.String())
	if text == "" {
		return ""
	}
	return ": stderr: " + text
}

type doctorBoundedReader struct {
	reader    io.ReadCloser
	remaining int
}

func (r *doctorBoundedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, fmt.Errorf("MCP stdout exceeded %d bytes", doctorProbeOutputLimit)
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= n
	return n, err
}

func (r *doctorBoundedReader) Close() error { return r.reader.Close() }
