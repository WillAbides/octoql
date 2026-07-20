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
	stalePath := lockPath(schemaPath)
	err := os.MkdirAll(filepath.Dir(stalePath), 0o700)
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
