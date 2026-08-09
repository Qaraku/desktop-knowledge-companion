package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type processLock struct {
	path string
}

func acquireProcessLock(root string) (*processLock, error) {
	path := filepath.Join(root, "core.lock")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrInstanceLocked
		}
		return nil, fmt.Errorf("create process lock: %w", err)
	}

	payload, err := json.Marshal(struct {
		PID       int       `json:"pid"`
		StartedAt time.Time `json:"started_at"`
	}{PID: os.Getpid(), StartedAt: time.Now().UTC()})
	if err == nil {
		_, err = file.Write(append(payload, '\n'))
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("write process lock: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close process lock: %w", closeErr)
	}
	return &processLock{path: path}, nil
}

func (lock *processLock) release() error {
	if lock == nil {
		return nil
	}
	if err := os.Remove(lock.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove process lock: %w", err)
	}
	return nil
}
