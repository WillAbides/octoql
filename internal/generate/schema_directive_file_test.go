package generate

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/willabides/octoql/internal/directive"
)

// TestOperationsResolveAgainstOnDiskSchema simulates what an editor's GraphQL
// language server does: read the schema files from disk as they are, then
// validate an operation against them.
//
// The generator declares @octoqlgen itself and never needs the declaration to
// be on disk, so a missing one is invisible to every other test here.  An
// editor has no such luxury and reports each use of the directive as unknown,
// which is why octoqlgen writes the declaration beside the schemas it
// materializes and why these fixture directories carry it too.
func TestOperationsResolveAgainstOnDiskSchema(t *testing.T) {
	schemaFilename := filepath.Join(dataDir, "schema.graphql")
	directiveFilename := filepath.Join(dataDir, directive.FileName)

	sources := make([]*ast.Source, 0, 2)
	for _, filename := range []string{schemaFilename, directiveFilename} {
		text, err := os.ReadFile(filename)
		require.NoError(t, err, "%s should exist so editors can resolve @%s",
			filename, directive.Name)
		sources = append(sources, &ast.Source{Name: filename, Input: string(text)})
	}

	schema, schemaErr := gqlparser.LoadSchema(sources...)
	require.Nil(t, schemaErr)

	operationFilenames, err := filepath.Glob(filepath.Join(dataDir, "*.graphql"))
	require.NoError(t, err)

	checked := 0
	for _, operationFilename := range operationFilenames {
		if operationFilename == schemaFilename || operationFilename == directiveFilename {
			continue
		}
		text, readErr := os.ReadFile(operationFilename)
		require.NoError(t, readErr)
		document, parseErr := parser.ParseQuery(&ast.Source{
			Name:  operationFilename,
			Input: string(text),
		})
		require.Nil(t, parseErr)

		t.Run(filepath.Base(operationFilename), func(t *testing.T) {
			for _, diagnostic := range validator.ValidateWithRules(schema, document, nil) {
				t.Errorf("an editor would report: %s", diagnostic.Message)
			}
		})
		checked++
	}
	assert.Positive(t, checked)
}

// TestDirectiveFilesAreCurrent catches a checked-in declaration drifting from
// the one octoqlgen writes.
func TestDirectiveFilesAreCurrent(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")

	checked := 0
	err := filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != directive.FileName {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		assert.Equal(t, directive.FileContents, string(data),
			"%s is out of date; copy directive.FileContents over it", path)
		checked++
		return nil
	})

	require.NoError(t, err)
	assert.Positive(t, checked, "expected checked-in @octoqlgen declarations")
}
