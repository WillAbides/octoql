package generate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	githubPublicSchemaDir          = "testdata/github-public-schema"
	githubPublicSchemaSize         = int64(1520362)
	githubPublicSchemaSHA256       = "c98cb9edeedd1fb56c8678c19a8ad540c8d0739dd94579dfedbe044192e4ab18"
	githubPublicSchemaConfigFile   = "octoqlgen.yaml"
	githubPublicSchemaMaterialized = "schema.docs.graphql"
)

func TestGenerateGitHubPublicSchema(t *testing.T) {
	t.Parallel()

	schemaFilename := filepath.Join(githubPublicSchemaDir, githubPublicSchemaMaterialized)
	configFilename := filepath.Join(githubPublicSchemaDir, githubPublicSchemaConfigFile)
	require.NoError(t, ensureGitHubPublicSchema(t.Context(), schemaFilename, configFilename))
	validateGitHubPublicSchema(t, schemaFilename)

	generatedFilename := filepath.Join(t.TempDir(), "generated.go")
	generated, err := Generate(&Config{
		Schema:     schemaFilename,
		Operations: []string{filepath.Join(githubPublicSchemaDir, "operations.graphql")},
		Generated:  generatedFilename,
		Package:    "githubschema",
	})
	require.NoError(t, err)

	source := generated[generatedFilename]
	compileStandaloneGo(t, map[string][]byte{"public_schema": source})

	output := string(source)
	for _, want := range []string{
		"func (c *Client) RepositorySummary(",
		"func (c *Client) NodesByID(",
		"func (c *Client) StarRepository(",
		"func (c *Client) GitHubAbstractCorpus(",
		"CreatedAt time.Time",
		"Oid string",
		"Url string",
		"OctoqlOther struct",
	} {
		assert.Contains(t, output, want)
	}
	for _, pattern := range []string{
		`type \w*Nodes\w*Repository struct`,
		`type \w*Nodes\w*Issue struct`,
		`type \w*Nodes\w*PullRequest struct`,
	} {
		assert.Regexp(t, pattern, output)
	}
	for _, pattern := range []string{
		"GenqlientOther",
		`type \w*Nodes\w*App struct`,
		`type \w*Search\w*Nodes\w*MarketplaceListing struct`,
	} {
		assert.NotRegexp(t, pattern, output)
	}
}

func ensureGitHubPublicSchema(ctx context.Context, schemaFilename, configFilename string) error {
	_, err := os.Stat(schemaFilename)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("checking materialized GitHub schema: %w", err)
	}

	absoluteConfig, err := filepath.Abs(configFilename)
	if err != nil {
		return fmt.Errorf("resolving public schema config: %w", err)
	}
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return fmt.Errorf("resolving repository root: %w", err)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(
		ctx,
		"go",
		"run",
		"./cmd/octoqlgen",
		"schema",
		"--config",
		absoluteConfig,
	)
	cmd.Dir = moduleRoot
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf(
			"materializing pinned public GitHub schema: %w\n%s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	_, err = os.Stat(schemaFilename)
	if err != nil {
		return fmt.Errorf("octoqlgen completed without materializing %q: %w", schemaFilename, err)
	}
	return nil
}

func validateGitHubPublicSchema(t *testing.T, schemaFilename string) {
	t.Helper()

	schema, err := os.ReadFile(schemaFilename)
	require.NoError(t, err)
	require.Equal(t, githubPublicSchemaSize, int64(len(schema)))
	require.Equal(t, githubPublicSchemaSHA256, fmt.Sprintf("%x", sha256.Sum256(schema)))
}
