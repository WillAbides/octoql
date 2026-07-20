package schema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAcquireExclusiveLockReusesStaleFile(t *testing.T) {
	t.Parallel()

	schemaPath := filepath.Join(t.TempDir(), "schema.graphql")
	stalePath, err := lockPath(schemaPath)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Dir(stalePath), 0o700)
	require.NoError(t, err)
	err = os.WriteFile(stalePath, []byte("stale"), 0o600)
	require.NoError(t, err)

	release, err := AcquireExclusiveLock(schemaPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		releaseErr := release()
		require.NoError(t, releaseErr)
	})
}

func TestAcquireExclusiveLockUsesPhysicalIdentityForMissingSchema(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	realDirectory := filepath.Join(directory, "real")
	aliasDirectory := filepath.Join(directory, "alias")
	err := os.Mkdir(realDirectory, 0o755)
	require.NoError(t, err)
	err = os.Symlink(realDirectory, aliasDirectory)
	require.NoError(t, err)

	release, err := AcquireExclusiveLock(filepath.Join(aliasDirectory, "schema.graphql"))
	require.NoError(t, err)
	t.Cleanup(func() {
		releaseErr := release()
		require.NoError(t, releaseErr)
	})

	_, err = AcquireExclusiveLock(filepath.Join(realDirectory, "schema.graphql"))
	require.Error(t, err)
}
