package testutil

import "time"

type ID string

type Account struct {
	ID    ID     `json:"id"`
	Login string `json:"login"`
}

const dateFormat = "2006-01-02"

type Date time.Time

func (d Date) MarshalJSON() ([]byte, error) {
	t := time.Time(d)
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.Format(dateFormat) + `"`), nil
}

func (d *Date) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*d = Date{}
		return nil
	}
	t, err := time.Parse(`"`+dateFormat+`"`, string(b))
	if err != nil {
		return err
	}
	*d = Date(t)
	return nil
}
