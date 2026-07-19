package generate

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
