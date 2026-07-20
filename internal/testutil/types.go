package testutil

import (
	"context"
	"encoding/json"
	"time"
)

type ID string

type Account struct {
	ID    ID     `json:"id"`
	Login string `json:"login"`
}

type MyContext interface {
	context.Context

	MyMethod()
}

const dateFormat = "2006-01-02"

func MarshalDate(t *time.Time) ([]byte, error) {
	// nil should never happen but we might as well check.  zero-time does
	// happen because omitempty doesn't consider it zero; we'd prefer to write
	// null than "0001-01-01".
	//
	// (I mean, we're tests.  Who cares!  But we may as well try to match what
	// prod code would want.)
	if t == nil || t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.Format(dateFormat) + `"`), nil
}

func UnmarshalDate(b []byte, t *time.Time) error {
	// (modified from time.Time.UnmarshalJSON)
	var err error
	*t, err = time.Parse(`"`+dateFormat+`"`, string(b))
	return err
}

type Option[V any] struct {
	value V
	ok    bool
}

func (o Option[V]) MarshalJSON() ([]byte, error) {
	if o.ok {
		return json.Marshal(o.value)
	}
	return json.Marshal((*V)(nil))
}

func (o *Option[V]) UnmarshalJSON(data []byte) error {
	v := (*V)(nil)

	err := json.Unmarshal(data, &v)
	if err != nil {
		return err
	}

	if v != nil {
		o.value = *v
		o.ok = true
	}

	return nil
}
