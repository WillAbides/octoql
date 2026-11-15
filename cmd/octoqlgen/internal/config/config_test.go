package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v4"
)

// TestDocumentationYAMLBlocksParse keeps the generated configuration reference
// honest: every YAML example it shows must use option names the config model
// actually recognizes.
func TestDocumentationYAMLBlocksParse(t *testing.T) {
	t.Parallel()

	filename := filepath.Join("..", "..", "..", "..", "docs", "configuration.md")
	content, err := os.ReadFile(filename)
	require.NoError(t, err)

	blocks := yamlBlocks(string(content))
	require.NotEmpty(t, blocks, "expected at least one yaml example")

	for i, block := range blocks {
		loaded := &Config{}
		err = yaml.Load([]byte(block), loaded, yaml.WithKnownFields())
		assert.NoError(t, err, "yaml block %d:\n%s", i+1, block)
	}
}

// yamlBlocks returns the contents of every fenced yaml block in a Markdown
// document.
func yamlBlocks(document string) []string {
	var blocks []string
	var current []string
	inBlock := false

	for line := range strings.SplitSeq(document, "\n") {
		if !inBlock {
			if strings.TrimSpace(line) == "```yaml" {
				inBlock = true
				current = nil
			}
			continue
		}
		if strings.TrimSpace(line) == "```" {
			inBlock = false
			blocks = append(blocks, strings.Join(current, "\n"))
			continue
		}
		current = append(current, line)
	}

	return blocks
}
