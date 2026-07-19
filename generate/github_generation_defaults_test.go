// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package generate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const githubGenerationDefaultsDir = "testdata/github-generation-defaults"

func TestGitHubScalarDefaults(t *testing.T) {
	expected := map[string]string{
		"Base64String":        "string",
		"BigInt":              "string",
		"CustomPropertyValue": "encoding/json.RawMessage",
		"Date":                "string",
		"DateTime":            "time.Time",
		"GitObjectID":         "string",
		"GitRefname":          "string",
		"GitSSHRemote":        "string",
		"GitTimestamp":        "time.Time",
		"HTML":                "string",
		"PreciseDateTime":     "time.Time",
		"URI":                 "string",
		"X509Certificate":     "string",
	}

	assert.Equal(t, expected, githubScalarTypes)

	for graphQLName, goType := range expected {
		t.Run(graphQLName, func(t *testing.T) {
			source := generateScalarOperation(t, graphQLName, nil)
			fieldType := goType
			if goType == "encoding/json.RawMessage" {
				fieldType = "json.RawMessage"
			}
			assert.Contains(t, source, "Value "+fieldType)
			if goType == "time.Time" {
				assert.Contains(t, source, `"time"`)
			}
			if goType == "encoding/json.RawMessage" {
				assert.Contains(t, source, `"encoding/json"`)
			}
		})
	}
}

func TestGitHubScalarOverrides(t *testing.T) {
	tests := []struct {
		binding        *TypeBinding
		name           string
		scalar         string
		wantField      string
		wantImport     string
		unwantedImport string
	}{
		{
			name:           "temporal scalar",
			scalar:         "DateTime",
			binding:        &TypeBinding{Type: "string"},
			wantField:      "Value string",
			unwantedImport: `"time"`,
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

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := generateScalarOperation(t, test.scalar, test.binding)
			assert.Contains(t, source, test.wantField)
			if test.wantImport != "" {
				assert.Contains(t, source, test.wantImport)
			}
			if test.unwantedImport != "" {
				assert.NotContains(t, source, test.unwantedImport)
			}
			require.NoError(t, buildGoFile("github_scalar_override", []byte(source)))
		})
	}
}

func TestCustomPropertyValueUnmarshal(t *testing.T) {
	source := generateScalarOperation(t, "CustomPropertyValue", nil)
	testSource := []byte(`package scalar

import (
	"encoding/json"
	"testing"
)

func TestCustomPropertyValue(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "string", body: ` + "`" + `{"value":"enabled"}` + "`" + `, want: ` + "`" + `"enabled"` + "`" + `},
		{name: "string array", body: ` + "`" + `{"value":["red","blue"]}` + "`" + `, want: ` + "`" + `["red","blue"]` + "`" + `},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response ScalarValueResponse
			if err := json.Unmarshal([]byte(test.body), &response); err != nil {
				t.Fatal(err)
			}
			if string(response.Value) != test.want {
				t.Fatalf("value = %s, want %s", response.Value, test.want)
			}
		})
	}
}
`)
	runGeneratedPackageTests(t, []byte(source), testSource)
}

func TestOmitUnreferencedImplementations(t *testing.T) {
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

func TestOmittedImplementationUnmarshal(t *testing.T) {
	source := generateGitHubAbstractSelections(t, nil)
	testSource := []byte(`package githubdefaults

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeAbstracts(t *testing.T) {
	tests := []struct {
		name string
		body string
		check func(*testing.T, RuntimeAbstractsResponse)
	}{
		{
			name: "referenced implementation",
			body: ` + "`" + `{"node":{"__typename":"Repository","id":"R1","url":"https://example.test/r","nameWithOwner":"octo/repo"},"nestedNodes":[],"actorNode":{"__typename":"Organization","login":"octo-org"}}` + "`" + `,
			check: func(t *testing.T, response RuntimeAbstractsResponse) {
				repository, ok := response.Node.(*RuntimeAbstractsNodeRepository)
				if !ok {
					t.Fatalf("node has type %T", response.Node)
				}
				if repository.NameWithOwner != "octo/repo" {
					t.Fatalf("nameWithOwner = %q", repository.NameWithOwner)
				}
				organization, ok := response.ActorNode.(*RuntimeAbstractsActorNodeOrganization)
				if !ok {
					t.Fatalf("actor node has type %T", response.ActorNode)
				}
				if organization.Login != "octo-org" {
					t.Fatalf("login = %q", organization.Login)
				}
			},
		},
		{
			name: "omitted implementation shared fields",
			body: ` + "`" + `{"node":{"__typename":"Issue","id":"I1","url":"https://example.test/i"},"nestedNodes":[]}` + "`" + `,
			check: func(t *testing.T, response RuntimeAbstractsResponse) {
				other, ok := response.Node.(*RuntimeAbstractsNodeOctoqlOther)
				if !ok {
					t.Fatalf("node has type %T", response.Node)
				}
				if other.GetTypename() != "Issue" || other.GetId() != "I1" || other.GetUrl() != "https://example.test/i" {
					t.Fatalf("catch-all fields = %q, %q, %q", other.GetTypename(), other.GetId(), other.GetUrl())
				}
			},
		},
		{
			name: "future typename",
			body: ` + "`" + `{"node":{"__typename":"FutureNode","id":"F1","url":"https://example.test/f"},"nestedNodes":[]}` + "`" + `,
			check: func(t *testing.T, response RuntimeAbstractsResponse) {
				other, ok := response.Node.(*RuntimeAbstractsNodeOctoqlOther)
				if !ok || other.GetTypename() != "FutureNode" {
					t.Fatalf("future node = %#v (%T)", response.Node, response.Node)
				}
			},
		},
		{
			name: "nulls and lists",
			body: ` + "`" + `{"node":null,"nestedNodes":[[[null,{"__typename":"Bot","id":"B1","url":"https://example.test/b"}]]]}` + "`" + `,
			check: func(t *testing.T, response RuntimeAbstractsResponse) {
				if response.Node != nil {
					t.Fatalf("node = %#v", response.Node)
				}
				if response.NestedNodes[0][0][0] != nil {
					t.Fatalf("null node = %#v", response.NestedNodes[0][0][0])
				}
				other, ok := response.NestedNodes[0][0][1].(*RuntimeAbstractsNestedNodesNodeOctoqlOther)
				if !ok || other.GetTypename() != "Bot" || other.GetId() != "B1" {
					t.Fatalf("nested node = %#v (%T)", response.NestedNodes[0][0][1], response.NestedNodes[0][0][1])
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response RuntimeAbstractsResponse
			if err := json.Unmarshal([]byte(test.body), &response); err != nil {
				t.Fatal(err)
			}
			test.check(t, response)
		})
	}
}

func TestMissingTypename(t *testing.T) {
	var response RuntimeAbstractsResponse
	err := json.Unmarshal([]byte(` + "`" + `{"node":{"id":"I1","url":"https://example.test/i"}}` + "`" + `), &response)
	if err == nil || !strings.Contains(err.Error(), "response was missing Node.__typename") {
		t.Fatalf("error = %v", err)
	}
}
`)

	runGeneratedPackageTests(t, []byte(source), testSource)
}

func TestCatchAllNameCollision(t *testing.T) {
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
	t.Helper()

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
	t.Helper()

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

func runGeneratedPackageTests(t *testing.T, generatedSource, testSource []byte) {
	t.Helper()

	dir := t.TempDir()
	moduleRoot, err := filepath.Abs("..")
	require.NoError(t, err)
	goMod := fmt.Sprintf(`module github.com/willabides/octoql-github-defaults-test

go 1.26.0

require github.com/willabides/octoql v0.0.0

replace github.com/willabides/octoql => %s
`, moduleRoot)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600))
	goSum, err := os.ReadFile(filepath.Join(moduleRoot, "go.sum"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.sum"), goSum, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "generated.go"), generatedSource, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "generated_test.go"), testSource, 0o600))

	for _, args := range [][]string{{"mod", "tidy"}, {"test", "-mod=readonly", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
		output, commandErr := cmd.CombinedOutput()
		require.NoErrorf(t, commandErr, "go %s:\n%s", strings.Join(args, " "), output)
	}
}
