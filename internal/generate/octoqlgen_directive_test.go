package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOctoqlgenDirectiveAddMergesRepeatedForDirectives(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	pointerDirective, err := parseDirective(
		`@octoqlgen(for: "IssueFilter.assignee", pointer: true)`,
		nil,
	)
	require.NoError(t, err)
	err = directive.add(pointerDirective, nil)
	require.NoError(t, err)

	omitemptyDirective, err := parseDirective(
		`@octoqlgen(for: "IssueFilter.assignee", omitempty: true)`,
		nil,
	)
	require.NoError(t, err)
	err = directive.add(omitemptyDirective, nil)
	require.NoError(t, err)

	fieldDirective := directive.FieldDirectives["IssueFilter"]["assignee"]
	require.NotNil(t, fieldDirective)
	assert.True(t, fieldDirective.GetPointer())
	assert.True(t, fieldDirective.GetOmitempty())
}

func TestOctoqlgenDirectiveAddRejectsConflictingRepeatedForDirectives(t *testing.T) {
	directive := newOctoqlgenDirective(nil)

	pointerDirective, err := parseDirective(
		`@octoqlgen(for: "IssueFilter.assignee", pointer: true)`,
		nil,
	)
	require.NoError(t, err)
	err = directive.add(pointerDirective, nil)
	require.NoError(t, err)

	conflictingDirective, err := parseDirective(
		`@octoqlgen(for: "IssueFilter.assignee", pointer: false)`,
		nil,
	)
	require.NoError(t, err)
	err = directive.add(conflictingDirective, nil)

	assert.EqualError(t, err, "conflicting values for pointer")
}
