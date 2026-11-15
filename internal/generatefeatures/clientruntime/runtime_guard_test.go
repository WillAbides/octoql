package clientruntime

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unmarshalJSONEmbed struct {
	Value  string `json:"value"`
	Called bool   `json:"-"`
}

func (e *unmarshalJSONEmbed) UnmarshalJSON([]byte) error {
	e.Called = true
	return nil
}

type guardedUnmarshalJSON struct {
	_octoqlNoUnmarshalJSON //nolint:unused // Embedding prevents UnmarshalJSON promotion.
	unmarshalJSONEmbed
}

func TestNoUnmarshalJSONPreventsMethodPromotion(t *testing.T) {
	unmarshalerType := reflect.TypeFor[json.Unmarshaler]()
	assert.True(t, reflect.TypeFor[*unmarshalJSONEmbed]().Implements(unmarshalerType))
	assert.False(t, reflect.TypeFor[*guardedUnmarshalJSON]().Implements(unmarshalerType))

	var got guardedUnmarshalJSON
	err := json.Unmarshal([]byte(`{"value":"ordinary"}`), &got)
	require.NoError(t, err)
	assert.Equal(t, "ordinary", got.Value)
	assert.False(t, got.Called)
}
