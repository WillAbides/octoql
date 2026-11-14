package generate

import (
	"fmt"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const githubGenerationDefaultsDir = "testdata/github-generation-defaults"

func TestGitHubScalarBindingOverrides(t *testing.T) {
	t.Parallel()

	overrideTests := []struct {
		binding    *TypeBinding
		name       string
		scalar     string
		wantField  string
		wantImport string
	}{
		{
			name:      "temporal scalar",
			scalar:    "DateTime",
			binding:   &TypeBinding{Type: "string"},
			wantField: "Value string",
		},
		{
			name:       "textual scalar",
			scalar:     "URI",
			binding:    &TypeBinding{Type: "net/url.URL"},
			wantField:  "Value url.URL",
			wantImport: `"net/url"`,
		},
		{
			name:      "opaque JSON scalar",
			scalar:    "CustomPropertyValue",
			binding:   &TypeBinding{Type: "string"},
			wantField: "Value string",
		},
	}

	filesToBuild := make(map[string][]byte, len(overrideTests))
	for _, test := range overrideTests {
		t.Run(test.name, func(t *testing.T) {
			source, genErr := generateTestSource(t, &testGenerateSpec{
				schema:      fmt.Sprintf("scalar %s\ntype Query { value: %s! }\n", test.scalar, test.scalar),
				operations:  "query ScalarValue { value }\n",
				packageName: "scalar",
				bindings: map[string]*TypeBinding{
					test.scalar: test.binding,
				},
			})
			require.NoError(t, genErr)
			assert.Contains(t, source, test.wantField)
			if test.wantImport != "" {
				assert.Contains(t, source, test.wantImport)
			}
			filesToBuild[test.name] = []byte(source)
		})
	}
	compileStandaloneGo(t, filesToBuild)
}

func TestOmitUnreferencedImplementations(t *testing.T) {
	t.Parallel()

	defaultSource := generateGitHubAbstractSelections(t, nil)
	omit := false
	optOutSource := generateGitHubAbstractSelections(t, &omit)

	for _, want := range []string{
		"type GitHubAbstractSelectionsNodeRepository struct",
		"type SharedActorFieldsUser struct",
		"type ConcreteUserFields struct",
		"type GitHubAbstractSelectionsNodeOctoqlOther struct",
		"type SharedActorFieldsOctoqlOther struct",
		"type GitHubAbstractSelectionsLatestRepositoryEventTimelineItemOctoqlOther struct",
		"type AllActorImplementationsActorOctoqlOther struct",
	} {
		assert.Containsf(t, defaultSource, want, "missing %s", want)
	}
	for _, unwanted := range []string{
		"type GitHubAbstractSelectionsNodeIssue struct",
		"type SharedActorFieldsBot struct",
		"GenqlientOther",
	} {
		assert.NotContains(t, defaultSource, unwanted)
	}

	for _, restored := range []string{
		"type GitHubAbstractSelectionsNodeIssue struct",
		"type SharedActorFieldsBot struct",
	} {
		assert.Containsf(t, optOutSource, restored, "missing %s", restored)
	}
	assert.Regexp(t, `type \w*Search\w*Nodes\w*Repository struct`, defaultSource)
	assert.Regexp(t, `type \w*Search\w*Nodes\w*Issue struct`, optOutSource)
	assert.NotRegexp(t, `type \w*Search\w*Nodes\w*Issue struct`, defaultSource)
	assert.NotRegexp(t, `type \w*LatestRepositoryEvent\w*RepositoryEvent struct`, defaultSource)
	assert.Contains(t, defaultSource, "SharedNodeFieldsOctoqlOther")
	assert.NotContains(t, optOutSource, "OctoqlOther")

	for _, referenced := range []string{
		"type AllActorImplementationsActorUser struct",
		"type AllActorImplementationsActorOrganization struct",
		"type AllActorImplementationsActorBot struct",
		"type AllActorImplementationsActorEnterpriseUserAccount struct",
	} {
		assert.Contains(t, defaultSource, referenced)
	}
	for _, referenced := range []string{
		"type GitHubAbstractSelectionsInlineActorNodeUser struct",
		"type GitHubAbstractSelectionsInlineActorNodeOrganization struct",
		"type GitHubAbstractSelectionsInlineActorNodeBot struct",
		"type GitHubAbstractSelectionsInlineActorNodeEnterpriseUserAccount struct",
		"type GitHubAbstractSelectionsNamedActorNodeUser struct",
		"type GitHubAbstractSelectionsNamedActorNodeOrganization struct",
		"type GitHubAbstractSelectionsNestedActorNodeUser struct",
		"type GitHubAbstractSelectionsNestedActorNodeOrganization struct",
		"type GitHubAbstractSelectionsNestedActorNodeEnterpriseUserAccount struct",
		"type GitHubAbstractSelectionsUnionNodeIssue struct",
	} {
		assert.Containsf(t, defaultSource, referenced, "missing %s", referenced)
	}
	assert.NotContains(t, defaultSource, "type GitHubAbstractSelectionsInlineActorNodeRepository struct")
	assert.NotContains(t, defaultSource, "type GitHubAbstractSelectionsNestedActorNodeBot struct")
	assert.NotContains(t, defaultSource, "type GitHubAbstractSelectionsUnionNodeRepository struct")

	catchAllPattern := regexp.MustCompile(`type (\w+OctoqlOther) struct`)
	matches := catchAllPattern.FindAllStringSubmatch(defaultSource, -1)
	require.NotEmpty(t, matches)
	names := make(map[string]bool, len(matches))
	for _, match := range matches {
		require.Falsef(t, names[match[1]], "duplicate catch-all %s", match[1])
		names[match[1]] = true
	}
}

func TestCatchAllNameCollision(t *testing.T) {
	t.Parallel()

	source, err := generateTestSource(t, &testGenerateSpec{
		schema: `interface Node { id: ID! }
type OctoqlOther implements Node { id: ID!, value: String! }
type User implements Node { id: ID!, login: String! }
type Query { node: Node }
`,
		operations: `query CatchAllName {
  node {
    id
    ... on OctoqlOther {
      value
    }
  }
}
`,
		packageName: "collision",
	})
	require.NoError(t, err)
	assert.Contains(t, source, "type CatchAllNameNodeOctoqlOther struct")
	assert.Contains(t, source, "type CatchAllNameNode2OctoqlOther struct")
	compileStandaloneGo(t, map[string][]byte{"catch_all_collision": []byte(source)})
}

func generateGitHubAbstractSelections(t *testing.T, omit *bool) string {
	config := &Config{
		Schema:                          filepath.Join(dataDir, "schema.graphql"),
		Operations:                      []string{filepath.Join(githubGenerationDefaultsDir, "operations.graphql")},
		Generated:                       "generated.go",
		Package:                         "githubdefaults",
		ContextType:                     "-",
		OmitUnreferencedImplementations: omit,
	}
	generated, err := Generate(config)
	require.NoError(t, err)
	return string(generated[config.Generated])
}
