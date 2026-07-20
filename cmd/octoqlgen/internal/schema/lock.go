package schema

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AcquireSharedLock prevents a schema update from publishing while a schema is
// being materialized.
func AcquireSharedLock(path string) (func() error, error) {
	return acquireLock(path, false)
}

// AcquireExclusiveLock prevents materialization and competing updates from
// observing a schema update in progress.
func AcquireExclusiveLock(path string) (func() error, error) {
	return acquireLock(path, true)
}

func acquireLock(path string, exclusive bool) (func() error, error) {
	filename := lockPath(path)
	err := os.MkdirAll(filepath.Dir(filename), 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating schema update lock directory: %w", err)
	}
	lockFile, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening schema update lock: %w", err)
	}

	unlock, err := lockOSFile(lockFile, exclusive)
	if err != nil {
		closeErr := lockFile.Close()
		if isLockContention(err) {
			return nil, errors.New("another schema update is already in progress")
		}
		return nil, fmt.Errorf("acquiring schema update lock: %w", errors.Join(err, closeErr))
	}

	return func() error {
		unlockErr := unlock()
		closeErr := lockFile.Close()
		return errors.Join(unlockErr, closeErr)
	}, nil
}

func lockPath(path string) string {
	absolutePath, err := filepath.Abs(path)
	if err == nil {
		path = absolutePath
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = resolvedPath
	}
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return filepath.Join(os.TempDir(), "octoqlgen-schema-locks", fmt.Sprintf("%x", sum))
}
