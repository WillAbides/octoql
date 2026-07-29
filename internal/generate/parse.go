package generate

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/lexer"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	_ "github.com/vektah/gqlparser/v2/validator/rules"
)

func getSchema(globs StringList) (*ast.Schema, error) {
	filenames, err := expandFilenames(globs)
	if err != nil {
		return nil, err
	}

	sources := make([]*ast.Source, len(filenames))
	for i, filename := range filenames {
		text, readErr := os.ReadFile(filename)
		if readErr != nil {
			return nil, errorf(nil, "unreadable schema file %v: %v", filename, readErr)
		}
		sources[i] = &ast.Source{Name: filename, Input: string(text)}
	}

	// Ideally here we'd just call gqlparser.LoadSchema. But the schema we are
	// given may or may not contain the builtin types String, Int, etc. (The
	// spec says it shouldn't, but introspection will return those types, and
	// some introspection-to-SDL tools aren't smart enough to remove them.) So
	// we inline LoadSchema and insert some checks.
	document, graphqlError := parser.ParseSchemas(sources...)
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

func getAndValidateQueries(basedir string, filenames StringList, schema *ast.Schema) (*ast.QueryDocument, error) {
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
	return filenames, nil
}

func getQueries(basedir string, globs StringList) (*ast.QueryDocument, error) {
	// We merge all the queries into a single query-document, since operations
	// in one might reference fragments in another.
	//
	// TODO(benkraft): It might be better to merge just within a filename, so
	// that fragment-names don't need to be unique across files.  (Although
	// then we may have other problems; and query-names still need to be.)
	mergedQueryDoc := new(ast.QueryDocument)
	addQueryDoc := func(queryDoc *ast.QueryDocument) {
		mergedQueryDoc.Operations = append(mergedQueryDoc.Operations, queryDoc.Operations...)
		mergedQueryDoc.Fragments = append(mergedQueryDoc.Fragments, queryDoc.Fragments...)
	}

	filenames, err := expandFilenames(globs)
	if err != nil {
		return nil, err
	}

	for _, filename := range filenames {
		text, err := os.ReadFile(filename)
		if err != nil {
			return nil, errorf(nil, "unreadable query-spec file %v: %v", filename, err)
		}

		switch filepath.Ext(filename) {
		case ".graphql", ".graphqls", ".gql":
			queryDoc, err := getQueriesFromString(string(text), basedir, filename)
			if err != nil {
				return nil, err
			}

			addQueryDoc(queryDoc)

		default:
			return nil, errorf(nil, "unknown file type: %v", filename)
		}
	}

	return mergedQueryDoc, nil
}

func getQueriesFromString(text string, basedir, filename string) (*ast.QueryDocument, error) {
	// make path relative to the config-directory
	relname, err := filepath.Rel(basedir, filename)
	if err == nil {
		filename = relname
	}

	source := &ast.Source{Name: filename, Input: text}

	err = rejectCommentDirectives(source)
	if err != nil {
		return nil, err
	}

	// Cf. gqlparser.LoadQuery
	document, graphqlError := parser.ParseQuery(source)
	if graphqlError != nil { // ParseQuery returns type *graphql.Error, yuck
		return nil, errorf(nil, "invalid query-spec file %v: %v", filename, graphqlError)
	}

	return document, nil
}

// rejectCommentDirectives fails on the comment form @octoqlgen options used to
// take.
//
// Ignoring these silently would drop the options they carry, which is how a
// field annotated `pointer: true` could quietly become a non-pointer that
// decodes null as the Go zero value.  Failing is the only safe reading of a
// file written for the old syntax.
//
// It lexes rather than scanning lines so that a `#` inside a string or block
// string is not mistaken for a comment.
func rejectCommentDirectives(source *ast.Source) error {
	lex := lexer.New(source)
	for {
		token, err := lex.ReadToken()
		if err != nil {
			// Let the parser report syntax errors; it produces better messages.
			return nil
		}
		if token.Kind == lexer.EOF {
			return nil
		}
		if token.Kind != lexer.Comment {
			continue
		}
		comment := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token.Value), "#"))
		name := commentDirectiveName(comment)
		if name == "" {
			continue
		}
		return errorf(&token.Pos,
			"@%s is a real directive now, not a comment; attach it to the node "+
				"it applies to, as in `myField @%s(pointer: false)`",
			name, octoqlgenDirectiveName)
	}
}

// commentDirectiveName returns the octoqlgen directive a comment is written in
// the old form of, or "" if the comment merely mentions one.
//
// The name has to be followed by "(" or by nothing, because prose about a
// directive is ordinary comment text and must not be rejected.  A bare prefix
// test would reject `# @octoqlgenFor applies to the input type below`, and also
// any word that merely starts the same way.
//
// Whitespace before "(" is not a boundary.  The old syntax parsed the comment
// as GraphQL, which ignores it, so `# @octoqlgen (pointer: false)` carried a
// real option and has to be refused rather than silently dropped.  That refuses
// prose whose first word after the name is parenthesised, which is the cheaper
// mistake of the two.
func commentDirectiveName(comment string) string {
	for _, name := range []string{octoqlgenDefaultsName, octoqlgenForName, octoqlgenDirectiveName} {
		rest, ok := strings.CutPrefix(comment, "@"+name)
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" || strings.HasPrefix(rest, "(") {
			return name
		}
	}
	return ""
}
