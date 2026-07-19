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
	err := os.Mkdir(realDir, 0o755)
	require.NoError(t, err)
	aliasDir := filepath.Join(tempDir, "alias")
	err = os.Symlink(realDir, aliasDir)
	if err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
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

func TestConfigValidateAndFillDefaults(t *testing.T) {
	t.Parallel()

	baseDir := filepath.Join("testdata", "queries", "subpackage")
	config := &Config{
		Schema:     StringList{"schema.graphql"},
		Operations: StringList{"operations.graphql"},
	}

	err := config.ValidateAndFillDefaults(baseDir)
	require.NoError(t, err)

	assert.Equal(t, StringList{filepath.Join(baseDir, "schema.graphql")}, config.Schema)
	assert.Equal(t, StringList{filepath.Join(baseDir, "operations.graphql")}, config.Operations)
	assert.Equal(t, filepath.Join(baseDir, "generated.go"), config.Generated)
	assert.Equal(t, "subpackage", config.Package)
	assert.Equal(t, "context.Context", config.ContextType)
	assert.Equal(t, TestHandlerTypesClient, config.TestHandlerTypes)
	require.NotNil(t, config.OmitUnreferencedImplementations)
	assert.True(t, *config.OmitUnreferencedImplementations)
}

func TestConfigValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    Config
		wantError string
	}{
		{
			name:      "invalid test handler type strategy",
			config:    Config{TestHandlerTypes: "invalid"},
			wantError: "test_handler.types must be one of",
		},
		{
			name:      "invalid optional mode",
			config:    Config{Optional: "invalid"},
			wantError: "optional must be one of",
		},
		{
			name:      "generic type required",
			config:    Config{Optional: "generic"},
			wantError: "optional_generic_type must be set",
		},
		{
			name:      "invalid package",
			config:    Config{Package: "invalid-package"},
			wantError: "invalid package in octoqlgen.yaml",
		},
		{
			name: "invalid casing",
			config: Config{
				Casing: Casing{Default: "invalid"},
			},
			wantError: "unknown casing algorithm",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.config.ValidateAndFillDefaults(filepath.Join("testdata", "queries", "subpackage"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantError)
		})
	}
}
