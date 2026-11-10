package generate

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/internal"

	"github.com/vektah/gqlparser/v2/ast"
)

var (
	parseDataDir       = "testdata/parsing"
	parseErrorsDir     = "testdata/parsing-errors"
	expandFilenamesDir = "testdata/expandFilenames"
)

func sortQueries(queryDoc *ast.QueryDocument) {
	sort.Slice(queryDoc.Operations, func(i, j int) bool {
		return queryDoc.Operations[i].Name < queryDoc.Operations[j].Name
	})
	sort.Slice(queryDoc.Fragments, func(i, j int) bool {
		return queryDoc.Fragments[i].Name < queryDoc.Fragments[j].Name
	})
}

func TestParse(t *testing.T) {
	queries, err := getQueries(
		parseDataDir, []string{filepath.Join(parseDataDir, "*.graphql")}, nil)
	require.NoError(t, err)

	sortQueries(queries)

	assert.NotEmpty(t, queries.Operations)
	assert.NotEmpty(t, queries.Fragments)
}

func filepathJoinAll(a string, bs []string) []string {
	ret := make([]string, len(bs))
	for i, b := range bs {
		ret[i] = filepath.Join(a, b)
	}
	return ret
}

func TestExpandFilenames(t *testing.T) {
	tests := []struct {
		name  string
		globs []string
		files []string
		err   bool
	}{
		{"SingleFile", []string{"a/b/c"}, []string{"a/b/c"}, false},
		{"OneStar", []string{"a/*/c"}, []string{"a/b/c"}, false},
		{"StarExt", []string{"a/b/*"}, []string{"a/b/c", "a/b/c.d"}, false},
		{"TwoStar", []string{"**/c"}, []string{"a/b/c"}, false},
		{"TwoStarSuffix", []string{"**/*"}, []string{"a/b/c", "a/b/c.d"}, false},
		{"Repeated", []string{"a/b/c", "a/b/*"}, []string{"a/b/c", "a/b/c.d"}, false},
		{"Empty", []string{"bogus/*"}, nil, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files, err := expandFilenames(filepathJoinAll(expandFilenamesDir, test.globs))
			if test.err && err == nil {
				t.Errorf("got %v, wanted error", files)
			} else if !test.err && err != nil {
				t.Errorf("got error %v, wanted %v", err, test.files)
			} else {
				assert.ElementsMatch(t, filepathJoinAll(expandFilenamesDir, test.files), files)
			}
		})
	}
}

// TestParseErrors tests that query-extraction produces appropriate errors if
// your query is invalid.
func TestParseErrors(t *testing.T) {
	g, err := getQueries(
		parseErrorsDir,
		[]string{filepath.Join(parseErrorsDir, "*.graphql")}, nil)
	if err == nil {
		t.Errorf("expected error from getQueries")
		t.Logf("%#v", g)
	}
}

func TestOperationGlobSkipsTheCompanionDirectiveFile(t *testing.T) {
	dir := t.TempDir()
	schemaDir := filepath.Join(dir, "schema")
	require.NoError(t, os.MkdirAll(schemaDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(schemaDir, "schema.graphqls"),
		[]byte("type Query { field: String }\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(schemaDir, internal.DirectiveFilename),
		[]byte(internal.DirectiveFileContents), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "op.graphql"),
		[]byte("query Q { field }\n"), 0o600))

	document, err := getQueries(dir, []string{dir + "/**/*.graphql"}, nil)

	require.NoError(t, err)
	require.Len(t, document.Operations, 1)
	assert.Equal(t, "Q", document.Operations[0].Name)
}

func TestOperationGlobMatchingOnlyTheCompanionIsAnError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, internal.DirectiveFilename),
		[]byte(internal.DirectiveFileContents), 0o600))

	_, err := getQueries(dir, []string{dir + "/*.graphql"}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not match any files")
}
