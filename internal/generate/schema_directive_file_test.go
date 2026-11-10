package generate

import (
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

func TestDirectiveFilesAreCurrent(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")

	for _, path := range []string{
		"cmd/octoqlgen/internal/schema/githubapi/" + directive.FileName,
		"example/" + directive.FileName,
		"internal/generate/testdata/queries/" + directive.FileName,
		"internal/generatefeatures/githubdefaults/" + directive.FileName,
		"internal/generatefeatures/nocontext/" + directive.FileName,
		"internal/handlertest/" + directive.FileName,
		"internal/integration/" + directive.FileName,
	} {
		data, err := os.ReadFile(filepath.Join(repositoryRoot, path))
		require.NoError(t, err)
		assert.Equal(t, directive.FileContents, string(data),
			"%s is out of date, copy directive.FileContents over it", path)
	}
}
