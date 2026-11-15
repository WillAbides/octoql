package main

import (
	"path/filepath"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureSchema = `$schema: https://json-schema.org/draft/2020-12/schema
title: fixture configuration
description: Terse root description.
x-doc: |
  Root prose.

  A second paragraph.
type: object
additionalProperties: false
required:
  - generated
properties:
  generated:
    description: Path for the generated file.
    x-doc: |
      Leaf prose for ` + "`generated`" + `.
    type: string
    examples:
      - githubapi/generated.go
  package:
    description: Package name override.
    x-doc: |
      Leaf prose for package.
    type: string
  test_handler:
    $ref: '#/$defs/testHandler'
  bindings:
    description: Bindings keyed by name.
    x-doc: |
      Prose about bindings.
    type: object
    additionalProperties:
      type: string
$defs:
  testHandler:
    description: Test handler configuration.
    x-doc: |
      Section prose for the test handler.
    type: object
    additionalProperties: false
    required:
      - generated
    properties:
      generated:
        description: Path for the generated handler.
        x-doc: |
          Nested leaf prose.
        type: string
`

func TestRender(t *testing.T) {
	got, err := render([]byte(fixtureSchema))
	require.NoError(t, err)

	snaps.WithConfig(
		snaps.Dir(filepath.Join("testdata", "snapshots")),
		snaps.Filename("render"),
		snaps.Ext(".md"),
		snaps.Raw(),
	).MatchStandaloneSnapshot(t, string(got))
}

func TestRenderRejectsMissingDoc(t *testing.T) {
	schema := `title: fixture
description: Terse.
x-doc: |
  Root prose.
type: object
properties:
  documented:
    description: Terse.
    x-doc: |
      Prose.
    type: string
  undocumented:
    description: Terse.
    type: string
`

	_, err := render([]byte(schema))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undocumented")
}

func TestRenderRejectsMissingRootDoc(t *testing.T) {
	schema := `title: fixture
description: Terse.
type: object
properties: {}
`

	_, err := render([]byte(schema))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "x-doc")
}
