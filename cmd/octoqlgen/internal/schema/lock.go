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

// SameLockIdentity reports whether two paths are serialized by the same lock.
func SameLockIdentity(left, right string) bool {
	leftPath, err := ResolveSchemaPath(left)
	if err != nil {
		return false
	}
	rightPath, err := ResolveSchemaPath(right)
	if err != nil {
		return false
	}
	return leftPath == rightPath
}

func acquireLock(path string, exclusive bool) (func() error, error) {
	filename, err := lockPath(path)
	if err != nil {
		return nil, err
	}
	err = os.MkdirAll(filepath.Dir(filename), 0o700)
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

func lockPath(path string) (string, error) {
	resolvedPath, err := ResolveSchemaPath(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(resolvedPath))
	return filepath.Join(os.TempDir(), "octoqlgen-schema-locks", fmt.Sprintf("%x", sum)), nil
}
