package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	findConfigDir    = "testdata/findConfig"
	validConfigDir   = "testdata/validConfig"
	invalidConfigDir = "testdata/invalidConfig"
)

func TestFindCfg(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	cases := map[string]struct {
		startDir    string
		expectedCfg string
		expectedErr error
	}{
		"yaml in parent directory": {
			startDir:    filepath.Join(cwd, findConfigDir, "parent", "child"),
			expectedCfg: filepath.Join(cwd, findConfigDir, "parent", "genqlient.yaml"),
		},
		"yaml in current directory": {
			startDir:    filepath.Join(cwd, findConfigDir, "current"),
			expectedCfg: filepath.Join(cwd, findConfigDir, "current", "genqlient.yaml"),
		},
		"no yaml": {
			startDir:    filepath.Join(cwd, findConfigDir, "none", "child"),
			expectedErr: os.ErrNotExist,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				require.NoError(t, os.Chdir(cwd), "Test cleanup failed")
			}()

			err = os.Chdir(tc.startDir)
			require.NoError(t, err)

			path, err := findCfg()
			assert.Equal(t, tc.expectedCfg, path)
			assert.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestFindCfgInDir(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	cases := map[string]struct {
		startDir string
		found    bool
	}{
		"yaml": {
			startDir: filepath.Join(cwd, findConfigDir, "filenames", "yaml"),
			found:    true,
		},
		"yml": {
			startDir: filepath.Join(cwd, findConfigDir, "filenames", "yml"),
			found:    true,
		},
		".yaml": {
			startDir: filepath.Join(cwd, findConfigDir, "filenames", "dotyaml"),
			found:    true,
		},
		".yml": {
			startDir: filepath.Join(cwd, findConfigDir, "filenames", "dotyml"),
			found:    true,
		},
		"none": {
			startDir: filepath.Join(cwd, findConfigDir, "filenames", "none"),
			found:    false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := findCfgInDir(tc.startDir)
			if tc.found {
				assert.NotEmpty(t, path)
			} else {
				assert.Empty(t, path)
			}
		})
	}
}

func TestAbsoluteAndRelativePathsInConfigFiles(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	config, err := ReadAndValidateConfig(
		filepath.Join(cwd, findConfigDir, "current", "genqlient.yaml"))
	require.NoError(t, err)

	require.Equal(t, 1, len(config.Schema))
	require.Equal(
		t,
		filepath.Join(cwd, findConfigDir, "current", "schema.graphql"),
		config.Schema[0],
	)
	require.Equal(t, 1, len(config.Operations))
	require.Equal(t, "/tmp/genqlient.graphql", config.Operations[0])
}

func testAllSnapshots(
	t *testing.T,
	dir string,
	testfunc func(t *testing.T, filename string),
) {
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		name := file.Name()
		if name[0] == '.' {
			continue // editor backup files, etc.
		}
		t.Run(name, func(t *testing.T) {
			filename := filepath.Join(dir, file.Name())
			testfunc(t, filename)
		})
	}
}

func TestValidConfigs(t *testing.T) {
	testAllSnapshots(t, validConfigDir, func(t *testing.T, filename string) {
		config, err := ReadAndValidateConfig(filename)
		require.NoError(t, err)
		switch filepath.Base(filename) {
		case "Empty.yml":
			snaps.MatchInlineSnapshot(t, config, snaps.Inline(`&generate.Config{
    Schema:              nil,
    Operations:          nil,
    Generated:           "testdata/validConfig/generated.go",
    Package:             "validConfig",
    ExportOperations:    "",
    ContextType:         "context.Context",
    ClientGetter:        "",
    Bindings:            {},
    PackageBindings:     nil,
    Casing:              generate.Casing{},
    Optional:            "",
    OptionalGenericType: "",
    StructReferences:    false,
    Extensions:          false,
    baseDir:             "testdata/validConfig",
    pkgPath:             "github.com/willabides/octoql/generate/testdata/validConfig",
}`))
		case "Lists.yaml":
			snaps.MatchInlineSnapshot(t, config, snaps.Inline(`&generate.Config{
    Schema:              {"testdata/validConfig/first_schema.graphql", "testdata/validConfig/second_schema.graphql"},
    Operations:          {"testdata/validConfig/first_operations.graphql", "testdata/validConfig/second_operations.graphql"},
    Generated:           "testdata/validConfig/generated.go",
    Package:             "validConfig",
    ExportOperations:    "",
    ContextType:         "context.Context",
    ClientGetter:        "",
    Bindings:            {},
    PackageBindings:     nil,
    Casing:              generate.Casing{},
    Optional:            "",
    OptionalGenericType: "",
    StructReferences:    false,
    Extensions:          false,
    baseDir:             "testdata/validConfig",
    pkgPath:             "github.com/willabides/octoql/generate/testdata/validConfig",
}`))
		case "Strings.yaml":
			snaps.MatchInlineSnapshot(t, config, snaps.Inline(`&generate.Config{
    Schema:              {"testdata/validConfig/schema.graphql"},
    Operations:          {"testdata/validConfig/operations.graphql"},
    Generated:           "testdata/validConfig/generated.go",
    Package:             "validConfig",
    ExportOperations:    "",
    ContextType:         "context.Context",
    ClientGetter:        "",
    Bindings:            {},
    PackageBindings:     nil,
    Casing:              generate.Casing{},
    Optional:            "",
    OptionalGenericType: "",
    StructReferences:    false,
    Extensions:          false,
    baseDir:             "testdata/validConfig",
    pkgPath:             "github.com/willabides/octoql/generate/testdata/validConfig",
}`))
		default:
			t.Fatalf("missing inline snapshot for %s", filename)
		}
	})
}

func TestInvalidConfigs(t *testing.T) {
	testAllSnapshots(t, invalidConfigDir, func(t *testing.T, filename string) {
		_, err := ReadAndValidateConfig(filename)
		require.Error(t, err)
		switch filepath.Base(filename) {
		case "InvalidCasing.yaml":
			snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("invalid config file testdata/invalidConfig/InvalidCasing.yaml: unknown casing algorithm: bogus"))
		case "InvalidOptional.yaml":
			snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("invalid config file testdata/invalidConfig/InvalidOptional.yaml: optional must be one of: 'value' (default), 'pointer', 'pointer_omitempty' or 'generic'"))
		case "InvalidPackage.yaml":
			snaps.MatchInlineSnapshot(t, err.Error(), snaps.Inline("invalid config file testdata/invalidConfig/InvalidPackage.yaml: invalid package in genqlient.yaml: 'bogus-package-name' is not a valid identifier"))
		default:
			t.Fatalf("missing inline snapshot for %s", filename)
		}
	})
}
