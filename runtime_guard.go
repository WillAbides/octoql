package octoql

// NoUnmarshalJSON is for generated code only.
//
// Embedding NoUnmarshalJSON alongside a type with an UnmarshalJSON method
// prevents that sibling method from being promoted.
type NoUnmarshalJSON struct{}

// UnmarshalJSON should never be called. It exists only to prevent a sibling
// UnmarshalJSON method from being promoted.
func (NoUnmarshalJSON) UnmarshalJSON([]byte) error {
	panic("NoUnmarshalJSON.UnmarshalJSON should never be called!")
}

// NoMarshalJSON is for generated code only.
//
// Embedding NoMarshalJSON alongside a type with a MarshalJSON method prevents
// that sibling method from being promoted.
type NoMarshalJSON struct{}

// MarshalJSON should never be called. It exists only to prevent a sibling
// MarshalJSON method from being promoted.
func (NoMarshalJSON) MarshalJSON() ([]byte, error) {
	panic("NoUnmarshalJSON.MarshalJSON should never be called!")
}
