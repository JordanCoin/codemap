package watch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"codemap/internal/runtimefile"
)

const flushProtocolVersion = 1

var ErrFlushUnsupported = errors.New("flush_unsupported")

func ensureControlDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe control directory %q", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe control directory %q", path)
	}
	return nil
}

type flushRequest struct {
	Version            int       `json:"version"`
	CanonicalRoot      string    `json:"canonical_root"`
	DaemonInstance     string    `json:"daemon_instance"`
	Nonce              string    `json:"nonce"`
	ObservedGeneration uint64    `json:"observed_generation"`
	Timestamp          time.Time `json:"timestamp"`
}
type flushAck struct {
	Version             int       `json:"version"`
	CanonicalRoot       string    `json:"canonical_root"`
	DaemonInstance      string    `json:"daemon_instance"`
	Nonce               string    `json:"nonce"`
	ObservedGeneration  uint64    `json:"observed_generation"`
	PublishedGeneration uint64    `json:"published_generation"`
	Timestamp           time.Time `json:"timestamp"`
	Success             bool      `json:"success"`
	ErrorCode           string    `json:"error_code,omitempty"`
}

func (r flushRequest) identity() string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%d", r.Version, r.CanonicalRoot, r.DaemonInstance, r.Nonce, r.ObservedGeneration)
}
func validateFlushAck(r flushRequest, a flushAck) error {
	if !a.Success {
		return fmt.Errorf("flush failed: %s", a.ErrorCode)
	}
	if a.Version != r.Version || a.CanonicalRoot != r.CanonicalRoot || a.DaemonInstance != r.DaemonInstance || a.Nonce != r.Nonce || a.ObservedGeneration != r.ObservedGeneration || a.PublishedGeneration <= r.ObservedGeneration {
		return errors.New("invalid flush acknowledgement")
	}
	return nil
}

func readStateFromActive(active ActiveRuntime) (*State, error) {
	data, err := runtimefile.Read(filepath.Join(active.Directory, "state.json"))
	if err != nil {
		return nil, err
	}
	var s State
	if err = json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func FlushState(ctx context.Context, root string) (*State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	active, err := ResolveActiveRuntime(root)
	if err != nil {
		return nil, err
	}
	if active.Legacy {
		return nil, ErrFlushUnsupported
	}
	state, err := readStateFromActive(active)
	if err != nil {
		return nil, err
	}
	if state.DaemonInstance == "" || state.CanonicalRoot != active.CanonicalRoot {
		return nil, ErrFlushUnsupported
	}
	var b [16]byte
	if _, err = rand.Read(b[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(b[:])
	req := flushRequest{Version: flushProtocolVersion, CanonicalRoot: active.CanonicalRoot, DaemonInstance: state.DaemonInstance, Nonce: nonce, ObservedGeneration: state.Generation, Timestamp: time.Now().UTC()}
	data, _ := json.Marshal(req)
	dir := filepath.Join(active.Directory, "flush")
	if err = ensureControlDirectory(dir); err != nil {
		return nil, err
	}
	requestPath := filepath.Join(dir, "request-"+nonce+".json")
	ackPath := filepath.Join(dir, "ack-"+nonce+".json")
	defer os.Remove(requestPath)
	defer os.Remove(ackPath)
	if err = runtimefile.WriteAtomic(requestPath, data, 0o600); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			ackData, readErr := runtimefile.Read(ackPath)
			if readErr != nil {
				continue
			}
			var ack flushAck
			if json.Unmarshal(ackData, &ack) != nil {
				continue
			}
			if err = validateFlushAck(req, ack); err != nil {
				return nil, err
			}
			current, readErr := readStateFromActive(active)
			if readErr != nil {
				return nil, readErr
			}
			if current.CanonicalRoot != req.CanonicalRoot || current.DaemonInstance != req.DaemonInstance || current.Generation < ack.PublishedGeneration {
				return nil, errors.New("acknowledged state identity changed")
			}
			return current, nil
		}
	}
}
