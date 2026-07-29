package generate

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		parseDataDir, []string{filepath.Join(parseDataDir, "*.graphql")})
	require.NoError(t, err)

	sortQueries(queries)

	assert.NotEmpty(t, queries.Operations)
	assert.NotEmpty(t, queries.Fragments)
}

// TestParseRejectsCommentDirective checks that a file written for the old
// comment syntax fails instead of generating with its options silently
// dropped.
//
// The comment-syntax directive below is the input under test, not a spelling
// that was missed when the directives became real.  Do not rewrite it.
func TestParseRejectsCommentDirective(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "legacy.graphql")
	err := os.WriteFile(filename, []byte(
		"# @octoqlgen(pointer: false)\nquery Legacy { field }\n"), 0o600)
	require.NoError(t, err)

	_, err = getQueries(dir, []string{filename})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a real directive now, not a comment")
	assert.Contains(t, err.Error(), "legacy.graphql:1")
}

// TestParseDistinguishesDirectivesFromProse checks that the comment-syntax
// check reads a whole directive name and not a prefix of one.
//
// Explaining @octoqlgenFor or @octoqlgenDefaults in a comment is a reasonable
// thing to do, especially while migrating, and those names begin with
// @octoqlgen.  As with the tests above, the comment-syntax directives here are
// the input under test.  Do not rewrite them.
//
// The old syntax handed the comment to the GraphQL parser, which ignores
// whitespace before the argument list, so `# @octoqlgen (pointer: false)` was a
// working directive and has to be rejected rather than read as prose.
func TestParseDistinguishesDirectivesFromProse(t *testing.T) {
	for comment, wantRejected := range map[string]bool{
		"@octoqlgen(pointer: false)":              true,
		"@octoqlgen":                              true,
		`@octoqlgenFor(field: "Q.f")`:             true,
		"@octoqlgenDefaults(pointer: false)":      true,
		"@octoqlgen applies to the node it is on": false,
		"@octoqlgenFor applies to the input type": false,
		"@octoqlgenDefaults covers every field":   false,
		"@octoqlgenesis is handled elsewhere":     false,

		// Spellings the old syntax accepted, which must not be read as prose.
		"@octoqlgen (pointer: false)":         true,
		`@octoqlgenFor (field: "Q.f")`:        true,
		"@octoqlgenDefaults  (pointer: true)": true,
		"@octoqlgen\t(pointer: false)":        true,

		// The cost of the rule above, accepted deliberately: prose whose first
		// word after the name is parenthesised is refused.  A wrong error is
		// better than an option that is silently dropped on migration.
		"@octoqlgen (deprecated) use a node option": true,
	} {
		t.Run(comment, func(t *testing.T) {
			dir := t.TempDir()
			filename := filepath.Join(dir, "operation.graphql")
			err := os.WriteFile(filename,
				[]byte("# "+comment+"\nquery Q { field }\n"), 0o600)
			require.NoError(t, err)

			_, err = getQueries(dir, []string{filename})

			if !wantRejected {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is a real directive now, not a comment")
		})
	}
}

// TestParseAllowsDirectiveInsideString checks that the comment-syntax check
// does not fire on string content that merely looks like a directive.
//
// The check lexes rather than scanning lines; a line-scanning implementation
// passes every other test in this package and fails only these two.  The
// comment-syntax directives below are string content under test, not spellings
// that were missed.  Do not rewrite them.
func TestParseAllowsDirectiveInsideString(t *testing.T) {
	for name, operation := range map[string]string{
		"string": "query Ok { field(arg: \"# @octoqlgen(pointer: false)\") }\n",
		"block string": "query Ok { field(arg: \"\"\"\n" +
			"# @octoqlgen(pointer: false)\n" +
			"\"\"\") }\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			filename := filepath.Join(dir, "ok.graphql")
			err := os.WriteFile(filename, []byte(operation), 0o600)
			require.NoError(t, err)

			_, err = getQueries(dir, []string{filename})

			assert.NoError(t, err)
		})
	}
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
		[]string{filepath.Join(parseErrorsDir, "*.graphql")})
	if err == nil {
		t.Errorf("expected error from getQueries")
		t.Logf("%#v", g)
	}
}
