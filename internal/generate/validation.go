package generate

// This file contains helpers to check whether two generated Go types match.

import "fmt"

// generatedTypeFieldsMatch returns nil if existing and candidate produce the
// same Go output, or an error describing the first differing field if they do
// not.  It compares the generated field plan — Go field names, type references,
// JSON names, and omitempty — rather than comparing raw directives, so the
// check is derived from exactly what gets emitted and cannot drift as new
// @octoqlgen options are added. Non-struct/non-interface types are determined
// entirely by their schema definition.
func generatedTypeFieldsMatch(existing, candidate goType) error {
	switch ex := existing.(type) {
	case *goStructType:
		can, ok := candidate.(*goStructType)
		if !ok {
			return fmt.Errorf("type kind changed: was struct, now %T", candidate)
		}
		if ex.IsInput != can.IsInput {
			return fmt.Errorf("input type changed: %v vs %v", ex.IsInput, can.IsInput)
		}
		return structFieldsMatch(ex.Fields, can.Fields)
	case *goInterfaceType:
		can, ok := candidate.(*goInterfaceType)
		if !ok {
			return fmt.Errorf("type kind changed: was interface, now %T", candidate)
		}
		err := structFieldsMatch(ex.SharedFields, can.SharedFields)
		if err != nil {
			return err
		}
		if len(ex.Implementations) != len(can.Implementations) {
			return fmt.Errorf("implementation count changed: %d vs %d",
				len(ex.Implementations), len(can.Implementations))
		}
		for i, impl := range ex.Implementations {
			other := can.Implementations[i]
			if impl.GoName != other.GoName {
				return fmt.Errorf("implementation %d changed: %s vs %s", i, impl.GoName, other.GoName)
			}
		}
		if ex.OtherImplementation == nil && can.OtherImplementation == nil {
			return nil
		}
		if ex.OtherImplementation == nil || can.OtherImplementation == nil {
			return fmt.Errorf("catch-all implementation changed")
		}
		if ex.OtherImplementation.GoName != can.OtherImplementation.GoName {
			return fmt.Errorf("catch-all implementation changed: %s vs %s",
				ex.OtherImplementation.GoName, can.OtherImplementation.GoName)
		}
	}
	return nil
}

// structFieldsMatch compares two lists of struct fields by their generated Go
// output: GoName, GoType.QualifiedRef(), JSONName, and Omitempty.
func structFieldsMatch(existing, candidate []*goStructField) error {
	if len(existing) != len(candidate) {
		return fmt.Errorf("field count changed: %d fields vs %d fields",
			len(existing), len(candidate))
	}
	for i, ex := range existing {
		can := candidate[i]
		switch {
		case ex.GoName != can.GoName:
			return fmt.Errorf("field %d Go name: %q vs %q", i, ex.GoName, can.GoName)
		case ex.GoType.QualifiedRef() != can.GoType.QualifiedRef():
			return fmt.Errorf("field %d (%s) Go type: %s vs %s",
				i, ex.GoName, ex.GoType.QualifiedRef(), can.GoType.QualifiedRef())
		case ex.JSONName != can.JSONName:
			return fmt.Errorf("field %d (%s) JSON name: %q vs %q",
				i, ex.GoName, ex.JSONName, can.JSONName)
		case ex.Omitempty != can.Omitempty:
			return fmt.Errorf("field %d (%s) omitempty: %v vs %v",
				i, ex.GoName, ex.Omitempty, can.Omitempty)
		}
	}
	return nil
}
