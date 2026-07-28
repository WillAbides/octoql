package generate

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCollisionCompileCorpus(t *testing.T) {
	tests := []struct {
		name                string
		schema              string
		operations          string
		casing              Casing
		keepImplementations bool
		wantGenerationErr   string
		wantCompileErrs     []string
		wantTypes           string
	}{
		{
			name: "baseline",
			schema: `
type Query {
  viewer: Viewer!
}
type Viewer {
  login: String!
}
`,
			operations: `
query Viewer {
  viewer {
    login
  }
}
`,
		},
		{
			// Both selected fields normalize to the Go identifier Foo. Expected to
			// compile once selection-set field collision fixes land.
			name: "case-distinct fields",
			schema: `
type Query {
  foo: String!
  Foo: String!
}
`,
			operations: `
query Fields {
  foo
  Foo
}
`,
			wantCompileErrs: []string{
				"Foo redeclared",
				"other declaration of Foo",
			},
		},
		{
			// Fragment A and the type generated for sibling field a share one Go
			// identifier. Expected to compile once fragment and field collision fixes land.
			name: "fragment and sibling field",
			schema: `
type Query {
  a: String!
  b: String!
}
`,
			operations: `
query Fields {
  a
  ...A
}
fragment A on Query {
  b
}
`,
			wantCompileErrs: []string{
				"A redeclared",
				"other declaration of A",
			},
		},
		{
			// Field getFoo collides with the getter generated for field foo. Expected
			// to compile once field and getter collision fixes land.
			name: "field and generated getter",
			schema: `
type Query {
  foo: String!
  getFoo: String!
}
`,
			operations: `
query Fields {
  foo
  getFoo
}
`,
			wantCompileErrs: []string{
				"field and method with the same name GetFoo",
			},
		},
		{
			// The named fragment's derived implementation type silently overwrites the
			// directly selected Impl type, dropping its id field. The package still
			// compiles, so this case asserts rendered declarations rather than a build
			// error. Expected declarations change once derived-name collision fixes land.
			name: "fragment replaces derived type",
			schema: `
type Query {
  impl: Impl!
  i: I!
}
interface I {
  login: String!
}
type Impl implements I {
  id: String!
  login: String!
}
`,
			operations: `
query F {
  impl {
    id
  }
  i {
    ...F
  }
}
fragment F on I {
  login
}
`,
			keepImplementations: true,
			wantTypes: `
type FImpl struct {
	Login string ` + "`json:\"login\"`" + `
}
`,
		},
		{
			name: "underscore and camel fields",
			schema: `
type Query {
  foo_bar: String!
  fooBar: String!
}
`,
			operations: `
query Fields {
  foo_bar
  fooBar
}
`,
			casing: Casing{Default: CasingAutoCamelCase},
			wantCompileErrs: []string{
				"FooBar redeclared",
				"other declaration of FooBar",
			},
		},
		{
			name: "fragment matching GraphQL type",
			schema: `
type Query {
  foo: Foo!
}
type Foo {
  id: String!
}
`,
			operations: `
query Q {
  foo {
    ...Foo
  }
}
fragment Foo on Foo {
  id
}
`,
		},
		{
			name: "field and JSON methods",
			schema: `
type Query {
  marshalJSON: String!
  unmarshalJSON: String!
  value: String!
}
`,
			operations: `
query Methods {
  marshalJSON
  unmarshalJSON
  ...Fields
}
fragment Fields on Query {
  value
}
`,
			wantCompileErrs: []string{
				"field and method with the same name MarshalJSON",
				"field and method with the same name UnmarshalJSON",
			},
		},
		{
			name: "enum values with same Go name",
			schema: `
enum State {
  FIRST_VALUE
  first_value
}
type Query {
  state: State!
}
`,
			operations: `
query State {
  state
}
`,
			wantGenerationErr: "have conflicting Go name",
		},
		{
			name: "input fields and operation variables",
			schema: `
input Variables {
  value: String!
}
type Query {
  value(input: Variables!): String!
}
`,
			operations: `
query Variables($input: Variables!) {
  value(input: $input)
}
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := probeGenerated(t, test.schema, test.operations, test.casing, test.keepImplementations)
			if test.wantGenerationErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantGenerationErr)
				return
			}
			require.NoError(t, err)

			if test.wantTypes != "" {
				assert.Equal(t, strings.TrimSpace(test.wantTypes), renderedTypeDeclarations(t, source, "FImpl"))
			}

			output, compileErr := probeCompile(t, source)
			if len(test.wantCompileErrs) == 0 {
				require.NoError(t, compileErr, output)
				return
			}
			require.Error(t, compileErr)
			for _, wantCompileErr := range test.wantCompileErrs {
				assert.Contains(t, output, wantCompileErr)
			}
		})
	}
}

// probeGenerated intentionally does not configure bindings: compile probes must
// only import the standard library, since user bindings are unavailable outside
// their source module.
func probeGenerated(
	t *testing.T,
	schema, operations string,
	casing Casing,
	keepImplementations bool,
) ([]byte, error) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n\ngo 1.26.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "generated.go"), []byte("package probe\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "operations.graphql"), []byte(operations), 0o600))

	var omitUnreferencedImplementations *bool
	if keepImplementations {
		omit := false
		omitUnreferencedImplementations = &omit
	}
	config := &Config{
		Schema:                          StringList{"schema.graphql"},
		Operations:                      StringList{"operations.graphql"},
		Generated:                       "generated.go",
		Package:                         "probe",
		ContextType:                     "-",
		Casing:                          casing,
		OmitUnreferencedImplementations: omitUnreferencedImplementations,
	}
	err := config.ValidateAndFillDefaults(dir)
	if err != nil {
		return nil, err
	}
	outputs, err := Generate(config)
	if err != nil {
		return nil, err
	}
	return outputs[config.Generated], nil
}

func probeCompile(t *testing.T, source []byte) (string, error) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n\ngo 1.26.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "generated.go"), source, 0o600))

	cmd := exec.CommandContext(t.Context(), "go", "build", "./...")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func renderedTypeDeclarations(t *testing.T, source []byte, names ...string) string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, 0)
	require.NoError(t, err)
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	var declarations bytes.Buffer
	for _, declaration := range file.Decls {
		genericDeclaration, ok := declaration.(*ast.GenDecl)
		if !ok || genericDeclaration.Tok != token.TYPE {
			continue
		}
		for _, specification := range genericDeclaration.Specs {
			typeSpecification, ok := specification.(*ast.TypeSpec)
			if !ok || !wanted[typeSpecification.Name.Name] {
				continue
			}
			err = format.Node(&declarations, token.NewFileSet(), &ast.GenDecl{
				Tok:   token.TYPE,
				Specs: []ast.Spec{typeSpecification},
			})
			require.NoError(t, err)
		}
	}
	return strings.TrimSpace(declarations.String())
}
