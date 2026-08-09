package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ValidateDataDir(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", ErrDataDirEmpty
	}
	if !filepath.IsAbs(value) {
		return "", ErrDataDirNotAbsolute
	}

	clean := filepath.Clean(value)
	if err := os.MkdirAll(clean, 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	return resolved, nil
}
