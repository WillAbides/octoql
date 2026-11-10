package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const githubGenerationDefaultsDir = "testdata/github-generation-defaults"

func TestGitHubScalarDefaultsAndOverrides(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	schema := `
scalar Base64String
scalar BigInt
scalar CustomPropertyValue
scalar Date
scalar DateTime
scalar GitObjectID
scalar GitRefname
scalar GitSSHRemote
scalar GitTimestamp
scalar HTML
scalar PreciseDateTime
scalar URI
scalar X509Certificate

type Query {
  base64String: Base64String!
  bigInt: BigInt!
  customPropertyValue: CustomPropertyValue!
  date: Date!
  dateTime: DateTime!
  gitObjectID: GitObjectID!
  gitRefname: GitRefname!
  gitSSHRemote: GitSSHRemote!
  gitTimestamp: GitTimestamp!
  html: HTML!
  preciseDateTime: PreciseDateTime!
  uri: URI!
  x509Certificate: X509Certificate!
}
`
	operation := `
query AllScalars {
  base64String
  bigInt
  customPropertyValue
  date
  dateTime
  gitObjectID
  gitRefname
  gitSSHRemote
  gitTimestamp
  html
  preciseDateTime
  uri
  x509Certificate
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "operation.graphql"), []byte(operation), 0o600))

	defaultsConfig := &Config{
		Schema:      []string{filepath.Join(dir, "schema.graphql")},
		Operations:  []string{filepath.Join(dir, "operation.graphql")},
		Generated:   filepath.Join(dir, "generated.go"),
		Package:     "scalar",
		ContextType: "-",
	}
	defaultsGenerated, err := Generate(defaultsConfig)
	require.NoError(t, err)
	defaultsSource := string(defaultsGenerated[defaultsConfig.Generated])

	for _, want := range []string{
		`"encoding/json"`,
		`"time"`,
		"Base64String string",
		"BigInt string",
		"CustomPropertyValue json.RawMessage",
		"Date string",
		"DateTime time.Time",
		"GitObjectID string",
		"GitRefname string",
		"GitSSHRemote string",
		"GitTimestamp time.Time",
		"Html string",
		"PreciseDateTime time.Time",
		"Uri string",
		"X509Certificate string",
	} {
		assertDeclares(t, defaultsSource, want)
	}

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

	filesToBuild := map[string][]byte{
		"defaults": defaultsGenerated[defaultsConfig.Generated],
	}
	for _, test := range overrideTests {
		t.Run(test.name, func(t *testing.T) {
			source := generateScalarOperation(t, test.scalar, test.binding)
			assert.Contains(t, source, test.wantField)
			if test.wantImport != "" {
				assert.Contains(t, source, test.wantImport)
			}
			filesToBuild[test.name] = []byte(source)
		})
	}
	require.NoError(t, buildGoFiles(filesToBuild))
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

	dir := t.TempDir()
	schema := `interface Node { id: ID! }
type OctoqlOther implements Node { id: ID!, value: String! }
type User implements Node { id: ID!, login: String! }
type Query { node: Node }
`
	operation := `query CatchAllName {
  node {
    id
    ... on OctoqlOther {
      value
    }
  }
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "operation.graphql"), []byte(operation), 0o600))
	config := &Config{
		Schema:      []string{filepath.Join(dir, "schema.graphql")},
		Operations:  []string{filepath.Join(dir, "operation.graphql")},
		Generated:   filepath.Join(dir, "generated.go"),
		Package:     "collision",
		ContextType: "-",
	}
	generated, err := Generate(config)
	require.NoError(t, err)
	source := generated[config.Generated]
	assert.Contains(t, string(source), "type CatchAllNameNodeOctoqlOther struct")
	assert.Contains(t, string(source), "type CatchAllNameNode2OctoqlOther struct")
	require.NoError(t, buildGoFile("catch_all_collision", source))
}

func generateGitHubAbstractSelections(t *testing.T, omit *bool) string {
	config := &Config{
		Schema:                          []string{filepath.Join(dataDir, "schema.graphql")},
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

func generateScalarOperation(t *testing.T, scalar string, binding *TypeBinding) string {
	dir := t.TempDir()
	schema := fmt.Sprintf("scalar %s\ntype Query { value: %s! }\n", scalar, scalar)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphql"), []byte(schema), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "operation.graphql"),
		[]byte("query ScalarValue { value }\n"),
		0o600,
	))
	bindings := map[string]*TypeBinding{}
	if binding != nil {
		bindings[scalar] = binding
	}
	config := &Config{
		Schema:      []string{filepath.Join(dir, "schema.graphql")},
		Operations:  []string{filepath.Join(dir, "operation.graphql")},
		Generated:   filepath.Join(dir, "generated.go"),
		Package:     "scalar",
		ContextType: "-",
		Bindings:    bindings,
	}
	generated, err := Generate(config)
	require.NoError(t, err)
	return string(generated[config.Generated])
}
