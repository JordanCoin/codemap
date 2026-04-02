package cmd

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codemap/watch"
)

const (
	sockdMagic           = 0x534f434b
	sockdVersion         = 1
	sockdHeaderLen       = 12
	sockdMaxPayloadLen   = 16 * 1024 * 1024
	frameKindRequest     = 1
	frameKindResponse    = 2
	frameKindError       = 5
	frameKindShutdown    = 6
	frameKindShutdownAck = 7

	codemapdDialTimeout  = 150 * time.Millisecond
	codemapdQueryTimeout = 500 * time.Millisecond
)

type codemapdQuery struct {
	Kind  string `json:"kind"`
	Limit int    `json:"limit,omitempty"`
}

type codemapdHealth struct {
	Status string `json:"status"`
}

type codemapdHubInfoResponse struct {
	UpdatedAt time.Time           `json:"updated_at"`
	FileCount int                 `json:"file_count"`
	Hubs      []string            `json:"hubs"`
	Importers map[string][]string `json:"importers"`
	Imports   map[string][]string `json:"imports"`
}

type codemapdWorkingSetResponse struct {
	UpdatedAt  time.Time         `json:"updated_at"`
	WorkingSet *watch.WorkingSet `json:"working_set,omitempty"`
}

type codemapdRecentEventsResponse struct {
	UpdatedAt    time.Time     `json:"updated_at"`
	RecentEvents []watch.Event `json:"recent_events"`
}

type codemapdProjectStatsResponse struct {
	UpdatedAt time.Time `json:"updated_at"`
	FileCount int       `json:"file_count"`
	Hubs      []string  `json:"hubs"`
}

func loadWorkingSet(root string) *watch.WorkingSet {
	if workingSet, err := querySocketWorkingSet(root); err == nil && workingSet != nil {
		return workingSet
	}

	state := watch.ReadState(root)
	if state == nil {
		return nil
	}
	return state.WorkingSet
}

func loadRecentEvents(root string, limit int) []watch.Event {
	if events, err := querySocketRecentEvents(root, limit); err == nil {
		return events
	}

	state := watch.ReadState(root)
	if state == nil {
		return nil
	}
	if limit <= 0 || len(state.RecentEvents) <= limit {
		return state.RecentEvents
	}
	return state.RecentEvents[len(state.RecentEvents)-limit:]
}

func loadProjectStats(root string) (*codemapdProjectStatsResponse, bool) {
	if stats, err := querySocketProjectStats(root); err == nil && stats != nil {
		return stats, true
	}

	state := watch.ReadState(root)
	if state == nil {
		return nil, false
	}
	return &codemapdProjectStatsResponse{
		UpdatedAt: state.UpdatedAt,
		FileCount: state.FileCount,
		Hubs:      append([]string(nil), state.Hubs...),
	}, true
}

func querySocketHubInfo(root string) (*hubInfo, error) {
	var response codemapdHubInfoResponse
	if err := queryCodemapd(root, codemapdQuery{Kind: "hub_info"}, &response); err != nil {
		return nil, err
	}
	if len(response.Importers) == 0 && len(response.Imports) == 0 && len(response.Hubs) == 0 {
		return nil, nil
	}
	return &hubInfo{
		Hubs:      response.Hubs,
		Importers: response.Importers,
		Imports:   response.Imports,
	}, nil
}

func querySocketWorkingSet(root string) (*watch.WorkingSet, error) {
	var response codemapdWorkingSetResponse
	if err := queryCodemapd(root, codemapdQuery{Kind: "working_set"}, &response); err != nil {
		return nil, err
	}
	return response.WorkingSet, nil
}

func querySocketRecentEvents(root string, limit int) ([]watch.Event, error) {
	var response codemapdRecentEventsResponse
	if err := queryCodemapd(root, codemapdQuery{Kind: "recent_events", Limit: limit}, &response); err != nil {
		return nil, err
	}
	return response.RecentEvents, nil
}

func querySocketProjectStats(root string) (*codemapdProjectStatsResponse, error) {
	var response codemapdProjectStatsResponse
	if err := queryCodemapd(root, codemapdQuery{Kind: "project_stats"}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func queryCodemapd(root string, request any, out any) error {
	if runtime.GOOS == "windows" {
		return errors.New("codemapd is not available on windows")
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}

	conn, err := net.DialTimeout("unix", codemapdSocketPath(root), codemapdDialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(codemapdQueryTimeout))

	if err := writeSockdFrame(conn, frameKindRequest, payload); err != nil {
		return err
	}

	kind, responsePayload, err := readSockdFrame(conn)
	if err != nil {
		return err
	}

	switch kind {
	case frameKindResponse:
		if out == nil {
			return nil
		}
		return json.Unmarshal(responsePayload, out)
	case frameKindError:
		return errors.New(strings.TrimSpace(string(responsePayload)))
	default:
		return fmt.Errorf("unexpected codemapd frame kind: %d", kind)
	}
}

func startSocketDaemon(root string) {
	if runtime.GOOS == "windows" || codemapdHealthy(root) {
		return
	}

	binaryPath, ok := resolveCodemapdBinary()
	if !ok {
		return
	}

	cmd := hookExecCommand(binaryPath, "--root", root)
	nullFile, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer nullFile.Close()

	cmd.Stdout = nullFile
	cmd.Stderr = nullFile
	cmd.Stdin = nullFile
	if err := cmd.Start(); err != nil {
		return
	}

	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		if codemapdHealthy(root) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func stopSocketDaemon(root string) {
	if runtime.GOOS == "windows" {
		return
	}

	conn, err := net.DialTimeout("unix", codemapdSocketPath(root), codemapdDialTimeout)
	if err != nil {
		return
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(codemapdQueryTimeout))
	if err := writeSockdFrame(conn, frameKindShutdown, nil); err != nil {
		return
	}

	kind, _, err := readSockdFrame(conn)
	if err != nil {
		return
	}
	if kind != frameKindShutdownAck {
		return
	}
}

func codemapdHealthy(root string) bool {
	var response codemapdHealth
	if err := queryCodemapd(root, codemapdQuery{Kind: "health"}, &response); err != nil {
		return false
	}
	return response.Status == "ok"
}

func resolveCodemapdBinary() (string, bool) {
	if runtime.GOOS == "windows" {
		return "", false
	}

	if envPath := strings.TrimSpace(os.Getenv("CODEMAPD_BIN")); envPath != "" {
		if info, err := os.Stat(envPath); err == nil && !info.IsDir() {
			return envPath, true
		}
	}

	exe, err := hookExecutablePath()
	if err != nil {
		return "", false
	}

	candidate := filepath.Join(filepath.Dir(exe), "codemapd")
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	return candidate, true
}

func codemapdSocketPath(root string) string {
	return filepath.Join(root, ".codemap", "codemapd.sock")
}

func writeSockdFrame(w io.Writer, kind byte, payload []byte) error {
	if len(payload) > sockdMaxPayloadLen {
		return fmt.Errorf("codemapd payload too large: %d", len(payload))
	}

	var header [sockdHeaderLen]byte
	binary.BigEndian.PutUint32(header[0:4], sockdMagic)
	header[4] = sockdVersion
	header[5] = kind
	binary.BigEndian.PutUint16(header[6:8], 0)
	binary.BigEndian.PutUint32(header[8:12], uint32(len(payload)))

	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return writeAll(w, payload)
}

func readSockdFrame(r io.Reader) (byte, []byte, error) {
	var header [sockdHeaderLen]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	if magic := binary.BigEndian.Uint32(header[0:4]); magic != sockdMagic {
		return 0, nil, fmt.Errorf("invalid codemapd frame magic: 0x%x", magic)
	}
	if version := header[4]; version != sockdVersion {
		return 0, nil, fmt.Errorf("unsupported codemapd frame version: %d", version)
	}

	payloadLen := binary.BigEndian.Uint32(header[8:12])
	if payloadLen > sockdMaxPayloadLen {
		return 0, nil, fmt.Errorf("codemapd frame payload too large: %d", payloadLen)
	}

	payload := make([]byte, int(payloadLen))
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}

	return header[5], payload, nil
}

func writeAll(w io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := w.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[n:]
	}
	return nil
}
