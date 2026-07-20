package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	transaction, err := BeginUpdate(aliasSchemaPath, configPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		rollbackErr := transaction.Rollback()
		require.NoError(t, rollbackErr)
	})
	assert.NotEqual(t, filepath.Dir(realSchemaPath), filepath.Dir(transaction.journalPath))
	journalInfo, err := os.Lstat(transaction.journalPath)
	require.NoError(t, err)
	assert.Zero(t, journalInfo.Mode()&os.ModeSymlink)
	if runtime.GOOS != "windows" {
		assert.Zero(t, journalInfo.Mode().Perm()&0o077)
	}
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

func TestJournalStateDirectoryDoesNotUseConfigHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG_CONFIG_HOME is not used on Windows")
	}

	before, err := journalStateDirectory()
	require.NoError(t, err)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	after, err := journalStateDirectory()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestRecoverPendingUpdateReplacesLateSchemaSymlink(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	targetPath := filepath.Join(directory, "schema-target.graphql")
	originalConfig := []byte("schema:\n  path: schema.graphql\n")
	originalSchema := []byte("type Query { original: String }\n")
	targetSchema := []byte("type Query { target: String }\n")
	err := os.WriteFile(configPath, originalConfig, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, originalSchema, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(targetPath, targetSchema, 0o600)
	require.NoError(t, err)
	_, err = BeginUpdate(schemaPath, configPath)
	require.NoError(t, err)
	err = os.Remove(schemaPath)
	require.NoError(t, err)
	err = os.Symlink(targetPath, schemaPath)
	require.NoError(t, err)

	err = RecoverPendingUpdate(schemaPath)
	require.NoError(t, err)

	schemaData, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Equal(t, originalSchema, schemaData)
	info, err := os.Lstat(schemaPath)
	require.NoError(t, err)
	assert.Zero(t, info.Mode()&os.ModeSymlink)
}

func TestRecoverPendingUpdateRemovesOriginallyMissingSchema(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	err := os.WriteFile(configPath, []byte("schema:\n  path: schema.graphql\n"), 0o600)
	require.NoError(t, err)
	transaction, err := BeginUpdate(schemaPath, configPath)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, []byte("type Query { viewer: String }\n"), 0o600)
	require.NoError(t, err)

	err = RecoverPendingUpdate(schemaPath)
	require.NoError(t, err)
	_, err = os.Lstat(schemaPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Lstat(transaction.journalPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRecoverPendingUpdatePreservesChangedConfigAfterPublication(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	originalConfig := []byte("schema:\n  path: schema.graphql\n  sha256: original\n")
	updatedConfig := []byte("schema:\n  path: schema.graphql\n  sha256: updated\n")
	replacementConfig := []byte("schema:\n  path: replacement.graphql\n  sha256: replacement\n")
	originalSchema := []byte("type Query { original: String }\n")
	updatedSchema := []byte("type Query { updated: String }\n")
	err := os.WriteFile(configPath, originalConfig, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, originalSchema, 0o600)
	require.NoError(t, err)
	transaction, err := BeginUpdate(schemaPath, configPath)
	require.NoError(t, err)
	err = transaction.MarkSchemaPublished()
	require.NoError(t, err)
	err = transaction.BeginConfigPublication(updatedConfig)
	require.NoError(t, err)
	err = os.WriteFile(configPath, updatedConfig, 0o600)
	require.NoError(t, err)
	err = transaction.MarkConfigPublished()
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, updatedSchema, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(configPath, replacementConfig, 0o600)
	require.NoError(t, err)

	err = RecoverPendingUpdate(schemaPath)
	require.NoError(t, err)

	configData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, replacementConfig, configData)
	schemaData, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Equal(t, originalSchema, schemaData)
}

func TestRecoverPendingUpdateRejectsLegacySchemaDirectoryJournal(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	victimPath := filepath.Join(directory, "victim.yaml")
	originalConfig := []byte("schema:\n  path: schema.graphql\n")
	originalVictim := []byte("victim: original\n")
	forgedVictim := []byte("victim: forged\n")
	err := os.WriteFile(configPath, originalConfig, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, []byte("type Query { viewer: String }\n"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(victimPath, originalVictim, 0o600)
	require.NoError(t, err)
	forgedJournal, err := json.Marshal(updateJournal{
		ConfigPath: victimPath,
		OriginalConfig: journalSnapshot{
			Data:   forgedVictim,
			Exists: true,
			Mode:   0o600,
		},
		Phase:      journalPhasePending,
		SchemaPath: schemaPath,
	})
	require.NoError(t, err)
	err = os.WriteFile(schemaPath+".schema-update.pending", forgedJournal, 0o600)
	require.NoError(t, err)

	err = RecoverPendingUpdate(schemaPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "legacy schema update journal")

	victimData, err := os.ReadFile(victimPath)
	require.NoError(t, err)
	assert.Equal(t, originalVictim, victimData)
}

func TestRecoverPendingUpdateRejectsSymlinkJournal(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	targetPath := filepath.Join(directory, "target")
	err := os.WriteFile(configPath, []byte("schema:\n  path: schema.graphql\n"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, []byte("type Query { viewer: String }\n"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(targetPath, []byte("unrelated"), 0o600)
	require.NoError(t, err)
	transaction, err := BeginUpdate(schemaPath, configPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		removeErr := os.Remove(transaction.journalPath)
		require.NoError(t, removeErr)
	})
	err = os.Remove(transaction.journalPath)
	require.NoError(t, err)
	err = os.Symlink(targetPath, transaction.journalPath)
	require.NoError(t, err)

	err = RecoverPendingUpdate(schemaPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a regular file")
	targetData, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("unrelated"), targetData)
}

func TestRecoverPendingUpdateRejectsNonregularJournal(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	err := os.WriteFile(configPath, []byte("schema:\n  path: schema.graphql\n"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(schemaPath, []byte("type Query { viewer: String }\n"), 0o600)
	require.NoError(t, err)
	transaction, err := BeginUpdate(schemaPath, configPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		removeErr := os.RemoveAll(transaction.journalPath)
		require.NoError(t, removeErr)
	})
	err = os.Remove(transaction.journalPath)
	require.NoError(t, err)
	err = os.Mkdir(transaction.journalPath, 0o700)
	require.NoError(t, err)

	err = RecoverPendingUpdate(schemaPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a regular file")
}
