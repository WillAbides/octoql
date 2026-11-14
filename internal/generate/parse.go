package generate

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	_ "github.com/vektah/gqlparser/v2/validator/rules"
	"github.com/willabides/octoql/internal"
)

func getSchema(filename string) (*ast.Schema, error) {
	text, err := os.ReadFile(filename)
	if err != nil {
		return nil, errorf(nil, "unreadable schema file %v: %v", filename, err)
	}

	source := &ast.Source{
		Name:  filename,
		Input: string(text),
	}

	// Ideally here we'd just call gqlparser.LoadSchema. But the schema we are
	// given may or may not contain the builtin types String, Int, etc. (The
	// spec says it shouldn't, but introspection will return those types, and
	// some introspection-to-SDL tools aren't smart enough to remove them.) So
	// we inline LoadSchema and insert some checks.
	document, graphqlError := parser.ParseSchema(source)
	if graphqlError != nil {
		// Schema doesn't even parse.
		return nil, errorf(nil, "invalid schema: %v", graphqlError)
	}

	// Check if we have a builtin type. (String is an arbitrary choice.)
	hasBuiltins := false
	for _, def := range document.Definitions {
		if def.Name == "String" {
			hasBuiltins = true
			break
		}
	}

	if !hasBuiltins {
		// modified from parser.ParseSchemas
		var preludeAST *ast.SchemaDocument
		preludeAST, graphqlError = parser.ParseSchema(validator.Prelude)
		if graphqlError != nil {
			return nil, errorf(nil, "invalid prelude (probably a gqlparser bug): %v", graphqlError)
		}
		document.Merge(preludeAST)
	}

	err = addOctoqlgenDirectiveDefinition(document)
	if err != nil {
		return nil, err
	}

	schema, graphqlError := validator.ValidateSchemaDocument(document)
	if graphqlError != nil {
		return nil, errorf(nil, "invalid schema: %v", graphqlError)
	}

	return schema, nil
}

func getAndValidateQueries(
	basedir string,
	filenames []string,
	schema *ast.Schema,
) (*ast.QueryDocument, error) {
	queryDoc, err := getQueries(basedir, filenames)
	if err != nil {
		return nil, err
	}

	// Cf. gqlparser.LoadQuery
	graphqlErrors := validator.Validate(schema, queryDoc)
	if graphqlErrors != nil {
		return nil, errorf(nil, "query-spec does not match schema: %v", graphqlErrors)
	}

	return queryDoc, nil
}

func expandFilenames(globs []string) ([]string, error) {
	uniqFilenames := make(map[string]bool, len(globs))
	for _, glob := range globs {
		// SplitPattern in case the path is absolute or something; a valid path
		// isn't necessarily a valid glob-pattern.
		glob = filepath.Clean(glob)
		glob = filepath.ToSlash(glob)
		base, pattern := doublestar.SplitPattern(glob)
		matches, err := doublestar.Glob(os.DirFS(base), pattern, doublestar.WithFilesOnly())
		if err != nil {
			return nil, errorf(nil, "can't expand file-glob %v: %v", glob, err)
		}
		matches = slices.DeleteFunc(matches, func(match string) bool {
			return path.Base(match) == internal.DirectiveFilename
		})
		if len(matches) == 0 {
			return nil, errorf(nil, "%v did not match any files", glob)
		}
		for _, match := range matches {
			uniqFilenames[path.Join(base, match)] = true
		}
	}
	filenames := make([]string, 0, len(uniqFilenames))
	for filename := range uniqFilenames {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	return filenames, nil
}

func getQueries(
	basedir string,
	filenames []string,
) (*ast.QueryDocument, error) {
	// We merge all the queries into a single query-document, since operations
	// in one might reference fragments in another.
	//
	// TODO(benkraft): It might be better to merge just within a filename, so
	// that fragment-names don't need to be unique across files.  (Although
	// then we may have other problems; and query-names still need to be.)
	mergedQueryDoc := new(ast.QueryDocument)
	for _, filename := range filenames {
		text, err := os.ReadFile(filename)
		if err != nil {
			return nil, errorf(nil, "unreadable query-spec file %v: %v", filename, err)
		}

		switch filepath.Ext(filename) {
		case ".graphql", ".graphqls", ".gql":
		default:
			return nil, errorf(nil, "unknown file type: %v", filename)
		}

		relative, relErr := filepath.Rel(basedir, filename)
		if relErr == nil {
			filename = relative
		}
		queryDoc, graphqlError := parser.ParseQuery(&ast.Source{
			Name:  filename,
			Input: string(text),
		})
		if graphqlError != nil {
			return nil, errorf(nil, "invalid query-spec file %v: %v", filename, graphqlError)
		}
		mergedQueryDoc.Operations = append(mergedQueryDoc.Operations, queryDoc.Operations...)
		mergedQueryDoc.Fragments = append(mergedQueryDoc.Fragments, queryDoc.Fragments...)
	}

	return mergedQueryDoc, nil
}
