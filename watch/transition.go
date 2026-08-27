package watch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"codemap/internal/projectpath"
)

var ErrTransitionLocked = errors.New("watch daemon transition already in progress")

type transitionRecord struct {
	PID   int    `json:"pid"`
	Token string `json:"token"`
}

func acquireTransitionWithin(root string, timeout time.Duration) (*Transition, error) {
	deadline := time.Now().Add(timeout)
	for {
		transition, err := AcquireTransition(root)
		if err == nil {
			return transition, nil
		}
		if !errors.Is(err, ErrTransitionLocked) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type Transition struct {
	path  string
	token string
}

func AcquireTransition(root string) (*Transition, error) {
	selection, err := projectpath.SelectRuntime(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(selection.RuntimeDir, 0o755); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(selection.RuntimeDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("unsafe runtime directory %q", selection.RuntimeDir)
	}
	path := filepath.Join(selection.RuntimeDir, "watch.transition")
	for attempt := 0; attempt < 2; attempt++ {
		tokenBytes := make([]byte, 16)
		if _, err := rand.Read(tokenBytes); err != nil {
			return nil, err
		}
		record := transitionRecord{PID: os.Getpid(), Token: hex.EncodeToString(tokenBytes)}
		payload, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, err := file.Write(payload); err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, err
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			return &Transition{path: path, token: record.Token}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		existing, readErr := readTransition(path)
		if readErr != nil || processAlive(existing.PID) {
			return nil, ErrTransitionLocked
		}
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, ErrTransitionLocked
		}
	}
	return nil, ErrTransitionLocked
}

func (t *Transition) Release() error {
	if t == nil || t.path == "" {
		return nil
	}
	record, err := readTransition(t.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.Token != t.token {
		return fmt.Errorf("transition lock ownership changed")
	}
	return os.Remove(t.path)
}

func readTransition(path string) (transitionRecord, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return transitionRecord{}, err
	}
	if !info.Mode().IsRegular() {
		return transitionRecord{}, fmt.Errorf("unsafe transition lock %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return transitionRecord{}, err
	}
	var record transitionRecord
	if err := json.Unmarshal(data, &record); err != nil || record.PID <= 0 || record.Token == "" {
		return transitionRecord{}, fmt.Errorf("invalid transition lock %q", path)
	}
	return record, nil
}
