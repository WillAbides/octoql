package schema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverPendingUpdateUsesPhysicalSchemaPath(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	realDirectory := filepath.Join(directory, "real")
	aliasDirectory := filepath.Join(directory, "alias")
	err := os.Mkdir(realDirectory, 0o755)
	require.NoError(t, err)
	err = os.Symlink(realDirectory, aliasDirectory)
	require.NoError(t, err)

	configPath := filepath.Join(directory, "octoqlgen.yaml")
	originalConfig := []byte("schema:\n  path: alias/schema.graphql\n")
	originalSchema := []byte("type Query { original: String }\n")
	updatedSchema := []byte("type Query { updated: String }\n")
	realSchemaPath := filepath.Join(realDirectory, "schema.graphql")
	aliasSchemaPath := filepath.Join(aliasDirectory, "schema.graphql")
	err = os.WriteFile(configPath, originalConfig, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(realSchemaPath, originalSchema, 0o600)
	require.NoError(t, err)
	_, err = BeginUpdate(aliasSchemaPath, configPath)
	require.NoError(t, err)
	err = os.WriteFile(realSchemaPath, updatedSchema, 0o600)
	require.NoError(t, err)

	err = RecoverPendingUpdate(realSchemaPath)
	require.NoError(t, err)

	configData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, originalConfig, configData)
	schemaData, err := os.ReadFile(realSchemaPath)
	require.NoError(t, err)
	assert.Equal(t, originalSchema, schemaData)
}
