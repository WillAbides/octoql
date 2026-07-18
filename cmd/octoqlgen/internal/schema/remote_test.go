// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
)

func TestMaterializerResolveRejectsInvalidSourceVariants(t *testing.T) {
	t.Parallel()

	url := "https://example.test/schema.graphql"
	tests := []struct {
		source config.Source
		name   string
	}{
		{
			name: "local only",
		},
		{
			name: "docs and url",
			source: config.Source{
				GithubDocs: &config.GithubDocs{Version: "fpt"},
				Url:        &url,
			},
		},
		{
			name: "repository and url",
			source: config.Source{
				GithubRepository: &config.GithubRepository{
					Repository: "owner/repository",
					Path:       "schema.graphql",
				},
				Url: &url,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewMaterializer().Resolve(t.Context(), test.source)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "exactly one remote source")
		})
	}
}
