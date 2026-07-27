package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"

	"codemap/internal/buildinfo"
)

type codexHookError struct {
	Message string `json:"message"`
	Path    string `json:"path"`
}

type codexHookMetadata struct {
	Command     string `json:"command"`
	Enabled     bool   `json:"enabled"`
	HandlerType string `json:"handlerType"`
	TrustStatus string `json:"trustStatus"`
}

type codexHooksListEntry struct {
	CWD    string              `json:"cwd"`
	Errors []codexHookError    `json:"errors"`
	Hooks  []codexHookMetadata `json:"hooks"`
}

var doctorCodexHooksTimeout = 5 * time.Second
var doctorCodexHooksProbe = probeDoctorCodexHooks

const doctorCodexHookMessageLimit = 256 * 1024

func validateCodexHookTrust(root string) error {
	entry, err := doctorCodexHooksProbe(root)
	if err != nil {
		return fmt.Errorf("codex hooks/list probe failed: %w; %s", err, codexHookTrustGuidance(root))
	}
	if len(entry.Errors) > 0 {
		return fmt.Errorf("codex reported %s (%s); %s", entry.Errors[0].Message, entry.Errors[0].Path, codexHookTrustGuidance(root))
	}

	found := make([]bool, len(recommendedCodexHooks))
	for _, hook := range entry.Hooks {
		if hook.HandlerType != "command" {
			continue
		}
		owned := -1
		for i, spec := range recommendedCodexHooks {
			if _, ok := migrateOwnedHookCommand(hook.Command, spec.Command); ok {
				owned = i
				break
			}
		}
		if owned < 0 {
			continue
		}
		found[owned] = true
		if !hook.Enabled {
			return fmt.Errorf("codemap hook %q is disabled; %s", recommendedCodexHooks[owned].Event, codexHookTrustGuidance(root))
		}
		if hook.TrustStatus != "trusted" && hook.TrustStatus != "managed" {
			return fmt.Errorf("codemap hook %q is %s; %s", recommendedCodexHooks[owned].Event, hook.TrustStatus, codexHookTrustGuidance(root))
		}
	}
	for i, present := range found {
		if !present {
			return fmt.Errorf("codemap hook %q is missing from Codex hooks/list; %s", recommendedCodexHooks[i].Event, codexHookTrustGuidance(root))
		}
	}
	return nil
}

func codexHookTrustGuidance(root string) string {
	return fmt.Sprintf("open the project in Codex CLI (`codex -C %s`) or Desktop, trust Codemap hooks from `/hooks` in CLI or Settings > Hooks in Desktop, then start a new Codex task/session", quoteHookExecutable(root, runtime.GOOS))
}

func probeDoctorCodexHooks(root string) (codexHooksListEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), doctorCodexHooksTimeout)
	defer cancel()
	cmd := newDoctorCommand(ctx, "codex", "-C", root, "app-server", "--stdio")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return codexHooksListEntry{}, fmt.Errorf("open app-server stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = stdout.Close()
		return codexHooksListEntry{}, fmt.Errorf("open app-server stdin: %w", err)
	}
	var stderr doctorBoundedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return codexHooksListEntry{}, fmt.Errorf("start app-server: %w", err)
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

	entry, exchangeErr := exchangeDoctorCodexHooks(stdout, stdin, root)
	if exchangeErr != nil {
		_ = cleanup(true)
		if ctx.Err() == context.DeadlineExceeded {
			return codexHooksListEntry{}, fmt.Errorf("timed out after %s%s", doctorCodexHooksTimeout, doctorStderrDetail(&stderr))
		}
		return codexHooksListEntry{}, fmt.Errorf("app-server protocol: %w%s", exchangeErr, doctorStderrDetail(&stderr))
	}
	if err := cleanup(false); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		if ctx.Err() == context.DeadlineExceeded {
			return codexHooksListEntry{}, fmt.Errorf("timed out after %s%s", doctorCodexHooksTimeout, doctorStderrDetail(&stderr))
		}
		return codexHooksListEntry{}, fmt.Errorf("wait for app-server: %w%s", err, doctorStderrDetail(&stderr))
	}
	return entry, nil
}

func exchangeDoctorCodexHooks(reader io.Reader, writer io.Writer, root string) (codexHooksListEntry, error) {
	encoder := json.NewEncoder(writer)
	responses := bufio.NewReaderSize(reader, 32*1024)
	initialize := map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "codemap-doctor", "version": buildinfo.Current()}, "capabilities": map[string]any{"experimentalApi": true}}}
	if err := encoder.Encode(initialize); err != nil {
		return codexHooksListEntry{}, fmt.Errorf("send initialize: %w", err)
	}
	if _, err := readDoctorCodexResponse(responses, 1); err != nil {
		return codexHooksListEntry{}, fmt.Errorf("initialize: %w", err)
	}
	if err := encoder.Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return codexHooksListEntry{}, fmt.Errorf("send initialized: %w", err)
	}
	if err := encoder.Encode(map[string]any{"id": 2, "method": "hooks/list", "params": map[string]any{"cwds": []string{root}}}); err != nil {
		return codexHooksListEntry{}, fmt.Errorf("send hooks/list: %w", err)
	}
	resultJSON, err := readDoctorCodexResponse(responses, 2)
	if err != nil {
		return codexHooksListEntry{}, fmt.Errorf("hooks/list: %w", err)
	}
	var result struct {
		Data []codexHooksListEntry `json:"data"`
	}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return codexHooksListEntry{}, fmt.Errorf("decode response: %w", err)
	}
	if len(result.Data) != 1 {
		return codexHooksListEntry{}, fmt.Errorf("returned %d working directories", len(result.Data))
	}
	return result.Data[0], nil
}

func readDoctorCodexResponse(reader *bufio.Reader, wantID int) (json.RawMessage, error) {
	for {
		line, err := readDoctorCodexJSONLine(reader)
		if err != nil {
			return nil, err
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(line, &response); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if response.ID != wantID {
			continue
		}
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		if len(response.Result) == 0 {
			return nil, fmt.Errorf("response %d has no result", wantID)
		}
		return response.Result, nil
	}
}

func readDoctorCodexJSONLine(reader *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, 4096)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > doctorCodexHookMessageLimit {
			return nil, fmt.Errorf("app-server JSON message exceeds %d bytes", doctorCodexHookMessageLimit)
		}
		line = append(line, fragment...)
		switch err {
		case nil:
			return line, nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) > 0 {
				return line, nil
			}
			return nil, io.EOF
		default:
			return nil, err
		}
	}
}
