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
	physicalPath, err := ResolveSchemaIdentity(absolutePath)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolutePath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("schema path must not be a symlink")
		}
		return physicalPath, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolving schema path: %w", err)
	}
	return physicalPath, nil
}

// ResolveSchemaIdentity returns a stable physical path without following the
// final component. It is safe for locating locks and recovery journals even if
// a symlink appears after an update has started.
func ResolveSchemaIdentity(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving schema path: %w", err)
	}
	absolutePath = filepath.Clean(absolutePath)
	missing := []string{}
	current := filepath.Dir(absolutePath)
	for {
		resolvedPath, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolvedPath = filepath.Join(resolvedPath, missing[index])
			}
			return filepath.Join(filepath.Clean(resolvedPath), filepath.Base(absolutePath)), nil
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
			return "", fmt.Errorf("resolving schema path %q: no existing parent", absolutePath)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
