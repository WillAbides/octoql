package nocontext

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type marshalJSONEmbed struct {
	Value string `json:"value"`
}

func (marshalJSONEmbed) MarshalJSON() ([]byte, error) {
	return json.Marshal("promoted")
}

type guardedMarshalJSON struct {
	noMarshalJSON //nolint:unused // Embedding prevents MarshalJSON promotion.
	marshalJSONEmbed
}

type unmarshalJSONEmbed struct {
	Value  string `json:"value"`
	Called bool   `json:"-"`
}

func (e *unmarshalJSONEmbed) UnmarshalJSON([]byte) error {
	e.Called = true
	return nil
}

type guardedUnmarshalJSON struct {
	noUnmarshalJSON //nolint:unused // Embedding prevents UnmarshalJSON promotion.
	unmarshalJSONEmbed
}

func TestNoMarshalJSONPreventsMethodPromotion(t *testing.T) {
	marshalerType := reflect.TypeFor[json.Marshaler]()
	assert.True(t, reflect.TypeFor[marshalJSONEmbed]().Implements(marshalerType))
	assert.False(t, reflect.TypeFor[guardedMarshalJSON]().Implements(marshalerType))

	value := guardedMarshalJSON{
		marshalJSONEmbed: marshalJSONEmbed{Value: "ordinary"},
	}
	got, err := json.Marshal(value)
	require.NoError(t, err)
	assert.JSONEq(t, `{"value":"ordinary"}`, string(got))
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

func TestMarshalGuardPanicsWhenCalled(t *testing.T) {
	assert.PanicsWithValue(
		t,
		"noUnmarshalJSON.MarshalJSON should never be called!",
		func() {
			_, _ = (noMarshalJSON{}).MarshalJSON()
		},
	)
}

func TestUnmarshalGuardPanicsWhenCalled(t *testing.T) {
	assert.PanicsWithValue(
		t,
		"noUnmarshalJSON.UnmarshalJSON should never be called!",
		func() {
			_ = (noUnmarshalJSON{}).UnmarshalJSON(nil)
		},
	)
}
