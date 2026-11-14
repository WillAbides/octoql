package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOutputPathsFilesystemAliases(t *testing.T) {
	tempDir := t.TempDir()
	realDir := filepath.Join(tempDir, "real")
	setupErr := os.Mkdir(realDir, 0o755)
	require.NoError(t, setupErr)
	aliasDir := filepath.Join(tempDir, "alias")
	setupErr = os.Symlink(realDir, aliasDir)
	if setupErr != nil {
		t.Skipf("symlinks are unavailable: %v", setupErr)
	}

	t.Run("symlinked parent", func(t *testing.T) {
		config := Config{
			Generated:            filepath.Join(realDir, "generated.go"),
			TestHandlerGenerated: filepath.Join(aliasDir, "generated.go"),
		}

		err := config.validateOutputPaths()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "output paths must be different")
	})

	t.Run("symlinked file", func(t *testing.T) {
		realFile := filepath.Join(realDir, "existing.go")
		err := os.WriteFile(realFile, []byte("package real\n"), 0o600)
		require.NoError(t, err)
		aliasFile := filepath.Join(tempDir, "existing-alias.go")
		err = os.Symlink(realFile, aliasFile)
		require.NoError(t, err)
		config := Config{
			Generated:            realFile,
			TestHandlerGenerated: aliasFile,
		}

		err = config.validateOutputPaths()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "output paths must be different")
	})

	t.Run("dangling symlink", func(t *testing.T) {
		targetFile := filepath.Join(realDir, "future.go")
		aliasFile := filepath.Join(tempDir, "future-alias.go")
		err := os.Symlink(targetFile, aliasFile)
		require.NoError(t, err)
		config := Config{
			Generated:            targetFile,
			TestHandlerGenerated: aliasFile,
		}

		err = config.validateOutputPaths()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "output paths must be different")
	})

	t.Run("dangling parent with relative target", func(t *testing.T) {
		aliasParent := filepath.Join(tempDir, "future-parent")
		err := os.Symlink(filepath.Join("real", "future"), aliasParent)
		require.NoError(t, err)
		config := Config{
			Generated:            filepath.Join(realDir, "future", "nested", "generated.go"),
			TestHandlerGenerated: filepath.Join(aliasParent, "nested", "generated.go"),
		}

		err = config.validateOutputPaths()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "output paths must be different")
	})

	t.Run("symlink cycle through missing parent", func(t *testing.T) {
		aliasFile := filepath.Join(tempDir, "cycle")
		err := os.Symlink("missing/../cycle", aliasFile)
		require.NoError(t, err)
		config := Config{
			Generated: filepath.Join(aliasFile, "generated.go"),
		}

		err = config.validateOutputPaths()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many symlinks")
	})

	t.Run("hard-linked file", func(t *testing.T) {
		realFile := filepath.Join(realDir, "hardlink-target.go")
		err := os.WriteFile(realFile, []byte("package real\n"), 0o600)
		require.NoError(t, err)
		aliasFile := filepath.Join(tempDir, "hardlink-alias.go")
		err = os.Link(realFile, aliasFile)
		if err != nil {
			t.Skipf("hard links are unavailable: %v", err)
		}
		config := Config{
			Generated:            realFile,
			TestHandlerGenerated: aliasFile,
		}

		err = config.validateOutputPaths()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "output paths must be different")
	})

	t.Run("case equivalent", func(t *testing.T) {
		config := Config{
			Generated:            filepath.Join(realDir, "CaseOutput.go"),
			TestHandlerGenerated: filepath.Join(realDir, "caseoutput.go"),
		}

		err := config.validateOutputPaths()

		if filesystemCaseInsensitive(realDir) {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "output paths must be different")
			return
		}
		require.NoError(t, err)
	})
}

func TestValidateOutputPathsProtectsInputs(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "octoqlgen.yaml")
	schemaPath := filepath.Join(directory, "schema.graphql")
	operationPath := filepath.Join(directory, "graphql", "viewer.graphql")
	setupErr := os.Mkdir(filepath.Dir(operationPath), 0o755)
	require.NoError(t, setupErr)
	for path, content := range map[string]string{
		configPath:    "schema: {}\n",
		schemaPath:    "type Query { viewer: String! }\n",
		operationPath: "query Viewer { viewer }\n",
	} {
		setupErr = os.WriteFile(path, []byte(content), 0o600)
		require.NoError(t, setupErr)
	}

	tests := []struct {
		name      string
		config    Config
		wantError string
	}{
		{
			name: "generated collides with config",
			config: Config{
				ConfigFile: configPath,
				Generated:  configPath,
			},
			wantError: "generated output path",
		},
		{
			name: "operation manifest collides with schema",
			config: Config{
				Schema:           schemaPath,
				Generated:        filepath.Join(directory, "generated.go"),
				ExportOperations: schemaPath,
			},
			wantError: "export_operations output path",
		},
		{
			name: "test handler collides with expanded operation",
			config: Config{
				Operations:           []string{filepath.Join(directory, "graphql", "*.graphql")},
				Generated:            filepath.Join(directory, "generated.go"),
				TestHandlerGenerated: operationPath,
			},
			wantError: "test_handler.generated output path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.config.validateOutputPaths()
			if err == nil {
				_, err = test.config.resolveOperationFiles()
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantError)
		})
	}
	t.Run("hard-linked schema alias excluded from operation glob", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		schemaFile := filepath.Join(dir, "schema.graphql")
		require.NoError(t, os.WriteFile(schemaFile, []byte("type Query { viewer: String! }\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "viewer.graphql"), []byte("query Viewer { viewer }\n"), 0o600))
		linkErr := os.Link(schemaFile, filepath.Join(dir, "schema-alias.graphql"))
		if linkErr != nil {
			t.Skipf("hard links are unavailable: %v", linkErr)
		}
		cfg := Config{
			Schema:     schemaFile,
			Operations: []string{filepath.Join(dir, "*.graphql")},
			Generated:  filepath.Join(dir, "generated.go"),
		}
		require.NoError(t, cfg.validateOutputPaths())
		filenames, err := cfg.resolveOperationFiles()
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(dir, "viewer.graphql")}, filenames)
	})
}
