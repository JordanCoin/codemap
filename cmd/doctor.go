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
	doctorRuntimeVersionProbe = probeDoctorCodexRuntimeVersion
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
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 {
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
	root, err := filepath.Abs(root)
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

	checkFile("project config", filepath.Join(root, ".codemap", "config.json"), validateJSONFile)
	claudeSettings, claudeSettingsErr := claudeSettingsPath(root, *global)
	claudeMCP, claudeMCPErr := claudeMCPPath(root, *global)
	codexHooks, codexHooksErr := codexHooksPath(root, *global)
	codexMCP, codexMCPErr := codexConfigPath(root, *global)
	validateEffectiveCodexMCP := validateCodexMCP
	if !*global && codexMCPErr == nil && !codexProjectMCPOverridesPlugin(codexMCP) {
		if pluginMCP, ok := activeCodexPluginMCPPath(); ok {
			codexMCP = pluginMCP
			validateEffectiveCodexMCP = validateCodexPluginMCP
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
		checkResolvedFile("Claude hooks", claudeSettings, claudeSettingsErr, func(path string) error {
			return validateHooks(path, recommendedClaudeHooks)
		}, checkFile, &failures)
		checkResolvedFile("Claude MCP", claudeMCP, claudeMCPErr, validateClaudeMCP, checkFile, &failures)
	}
	if selected == "codex" || (selected == "" && (codexConfigured || (!anyConfigured && (codexAvailable || codexDesktopAvailable)))) {
		checkResolvedFile("Codex hooks", codexHooks, codexHooksErr, func(path string) error {
			return validateHooks(path, recommendedCodexHooks)
		}, checkFile, &failures)
		if codexAvailable && codexHooksErr == nil && validateHooks(codexHooks, recommendedCodexHooks) == nil {
			if err := validateCodexHookTrust(root); err != nil {
				fmt.Printf("MISS Codex hook trust: %v\n", err)
				failures++
			} else {
				fmt.Println("OK   Codex hook trust: all Codemap hooks are enabled and runnable")
			}
		}
		checkResolvedFile("Codex MCP", codexMCP, codexMCPErr, validateEffectiveCodexMCP, checkFile, &failures)
	}

	if failures > 0 {
		fmt.Printf("\n%d check(s) need attention.\n", failures)
		return 1
	}
	fmt.Println("\nCodemap integration prerequisites are valid.")
	return 0
}

func reportCodexRuntimeVersions(cliPath string) bool {
	if doctorRuntimeGOOS != "darwin" {
		return false
	}
	desktopPaths := make([]string, 0, len(doctorDesktopCodexCandidates))
	for _, candidate := range doctorDesktopCodexCandidates {
		if _, err := validateIntegrationExecutable(candidate, "darwin"); err == nil {
			desktopPaths = append(desktopPaths, candidate)
		}
	}
	if len(desktopPaths) == 0 {
		return false
	}

	cliVersion := ""
	var cliErr error
	if cliPath != "" {
		cliVersion, cliErr = doctorRuntimeVersionProbe(cliPath)
		if cliErr != nil {
			fmt.Printf("WARN Codex CLI runtime: %s (%v)\n", cliPath, cliErr)
		} else {
			fmt.Printf("OK   Codex CLI runtime: %s (%s)\n", cliPath, cliVersion)
		}
	}
	for _, desktopPath := range desktopPaths {
		desktopVersion, err := doctorRuntimeVersionProbe(desktopPath)
		if err != nil {
			fmt.Printf("WARN Codex Desktop runtime: %s (%v)\n", desktopPath, err)
			continue
		}
		fmt.Printf("OK   Codex Desktop runtime: %s (%s)\n", desktopPath, desktopVersion)
	}
	return true
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

func checkResolvedFile(label, path string, pathErr error, validate func(string) error, check func(string, string, func(string) error), failures *int) {
	if pathErr != nil {
		fmt.Printf("MISS %s: could not resolve path (%v)\n", label, pathErr)
		(*failures)++
		return
	}
	check(label, path, validate)
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
		if !hasHookSpec(hooks[spec.Event], spec) {
			return fmt.Errorf("missing %s hook %q", spec.Event, spec.Command)
		}
	}
	return nil
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
	launch := doctorManagedLaunch{command: command, args: args, integration: doctorManagedIntegration(args)}
	if command == "codemap" && stringSlicesEqual(args, []string{"mcp"}) {
		return launch, fmt.Errorf("legacy PATH-relative codemap MCP definition is stale")
	}
	if len(args) != 5 || args[0] != "mcp" || args[1] != "--configured-version" || args[2] == "" || args[3] != "--integration" {
		return launch, fmt.Errorf("unrecognized codemap MCP arguments")
	}
	launch.configuredVersion = args[2]
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
