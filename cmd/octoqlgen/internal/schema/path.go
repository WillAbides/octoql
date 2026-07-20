package schema

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ResolveSchemaPath returns a stable physical schema destination. It follows
// symlinked parents, including when the schema file has not been materialized.
// A schema file cannot itself be a symlink because atomic publication would
// replace the link rather than its target.
func ResolveSchemaPath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving schema path: %w", err)
	}
	absolutePath = filepath.Clean(absolutePath)
	info, err := os.Lstat(absolutePath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("schema path must not be a symlink")
		}
		resolvedPath, resolveErr := filepath.EvalSymlinks(absolutePath)
		if resolveErr != nil {
			return "", fmt.Errorf("resolving schema path: %w", resolveErr)
		}
		return filepath.Clean(resolvedPath), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolving schema path: %w", err)
	}
	return resolveMissingSchemaPath(absolutePath)
}

func resolveMissingSchemaPath(path string) (string, error) {
	missing := []string{}
	current := path
	for {
		resolvedPath, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolvedPath = filepath.Join(resolvedPath, missing[index])
			}
			return filepath.Clean(resolvedPath), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolving schema path: %w", err)
		}
		info, statErr := os.Lstat(current)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("schema path has a dangling symlink")
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("resolving schema path: %w", statErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolving schema path %q: no existing parent", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
