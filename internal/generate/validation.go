package generate

// This file contains helpers to do various bits of validation in the process
// of converting types to Go, notably, for cases where we need to check that
// two types match.

import (
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// generatedTypeFieldsMatch returns nil if existing and candidate produce the
// same Go output, or an error describing the first differing field if they do
// not.  It compares the generated field plan — Go field names, type references,
// JSON names, and omitempty — rather than comparing raw directives, so the
// check is derived from exactly what gets emitted and cannot drift as new
// @octoqlgen options are added.  Returns nil for non-struct/non-interface
// types; those are generated from the schema definition and AST comparison
// alone is sufficient.
func generatedTypeFieldsMatch(existing, candidate goType) error {
	switch ex := existing.(type) {
	case *goStructType:
		can, ok := candidate.(*goStructType)
		if !ok {
			return fmt.Errorf("type kind changed: was struct, now %T", candidate)
		}
		return structFieldsMatch(ex.Fields, can.Fields)
	case *goInterfaceType:
		can, ok := candidate.(*goInterfaceType)
		if !ok {
			return fmt.Errorf("type kind changed: was interface, now %T", candidate)
		}
		return structFieldsMatch(ex.SharedFields, can.SharedFields)
	}
	return nil
}

// structFieldsMatch compares two lists of struct fields by their generated Go
// output: GoName, GoType.Reference(), JSONName, and Omitempty.
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
		case ex.GoType.Reference() != can.GoType.Reference():
			return fmt.Errorf("field %d (%s) Go type: %s vs %s",
				i, ex.GoName, ex.GoType.Reference(), can.GoType.Reference())
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

// selectionsMatch recursively compares the two selection-sets, and returns an
// error if they differ.
//
// It checks field names, aliases, order, and fragment-structure.  It does not
// check arguments or directives, and it does not recurse into named fragments,
// it only checks that their names match.
//
// Two selections that request the same fields can still generate different Go,
// for example when one of them sets pointer or bind.  That difference is caught
// by generatedTypeFieldsMatch, which compares the fields octoqlgen is about to
// emit rather than the options asking for them, so it also covers @skip,
// @include, and anything else that reaches the generated type.
//
// If both selection-sets are nil/empty, they compare equal.
func selectionsMatch(
	pos *ast.Position,
	expectedSelectionSet, actualSelectionSet ast.SelectionSet,
) error {
	if len(expectedSelectionSet) != len(actualSelectionSet) {
		return errorf(
			pos, "expected %d fields, got %d",
			len(expectedSelectionSet), len(actualSelectionSet))
	}

	for i, expected := range expectedSelectionSet {
		switch expected := expected.(type) {
		case *ast.Field:
			actual, ok := actualSelectionSet[i].(*ast.Field)
			switch {
			case !ok:
				return errorf(pos,
					"expected selection #%d to be field, got %T",
					i, actualSelectionSet[i])
			case actual.Name != expected.Name:
				return errorf(actual.Position,
					"expected field %d to be %s, got %s",
					i, expected.Name, actual.Name)
			case actual.Alias != expected.Alias:
				return errorf(actual.Position,
					"expected field %d's alias to be %s, got %s",
					i, expected.Alias, actual.Alias)
			}
			err := selectionsMatch(actual.Position, expected.SelectionSet, actual.SelectionSet)
			if err != nil {
				return fmt.Errorf("in %s sub-selection: %w", actual.Alias, err)
			}
		case *ast.InlineFragment:
			actual, ok := actualSelectionSet[i].(*ast.InlineFragment)
			switch {
			case !ok:
				return errorf(pos,
					"expected selection %d to be inline fragment, got %T",
					i, actualSelectionSet[i])
			case actual.TypeCondition != expected.TypeCondition:
				return errorf(actual.Position,
					"expected fragment %d to be on type %s, got %s",
					i, expected.TypeCondition, actual.TypeCondition)
			}
			err := selectionsMatch(actual.Position, expected.SelectionSet, actual.SelectionSet)
			if err != nil {
				return fmt.Errorf("in inline fragment on %s: %w", actual.TypeCondition, err)
			}
		case *ast.FragmentSpread:
			actual, ok := actualSelectionSet[i].(*ast.FragmentSpread)
			switch {
			case !ok:
				return errorf(pos,
					"expected selection %d to be fragment spread, got %T",
					i, actualSelectionSet[i])
			case actual.Name != expected.Name:
				return errorf(actual.Position,
					"expected fragment %d to be ...%s, got ...%s",
					i, expected.Name, actual.Name)
			}
		}
	}
	return nil
}

// directiveOptionsMatch reports whether two @octoqlgenFor declarations ask for
// the same thing.  A missing directive is equivalent to one that sets nothing.
//
// This compares declarations against each other, before conversion, so there is
// no generated field to compare instead: the point is to reject two operations
// that disagree, not to compare what was emitted.
func directiveOptionsMatch(a, b *octoqlgenDirective) bool {
	if a == nil {
		a = &octoqlgenDirective{}
	}
	if b == nil {
		b = &octoqlgenDirective{}
	}
	return boolOptionsMatch(a.Omitempty, b.Omitempty) &&
		boolOptionsMatch(a.Pointer, b.Pointer) &&
		boolOptionsMatch(a.Struct, b.Struct) &&
		boolOptionsMatch(a.Flatten, b.Flatten) &&
		a.Bind == b.Bind &&
		a.TypeName == b.TypeName &&
		a.Alias == b.Alias
}

func boolOptionsMatch(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// validateBindingSelection checks that if you requested in your type-binding
// that this type must always request certain fields, then in fact it does.
func (g *generator) validateBindingSelection(
	typeName string,
	binding *TypeBinding,
	pos *ast.Position,
	selectionSet ast.SelectionSet,
) error {
	if binding.ExpectExactFields == "" {
		return nil // no validation requested
	}

	// HACK: we parse the selection as if it were a query, which is basically
	// the same (for syntax purposes; it of course wouldn't validate)
	doc, gqlErr := parser.ParseQuery(&ast.Source{Input: binding.ExpectExactFields})
	if gqlErr != nil {
		return errorf(
			nil, "invalid type-binding %s.expect_exact_fields: %w", typeName, gqlErr)
	}

	err := selectionsMatch(pos, doc.Operations[0].SelectionSet, selectionSet)
	if err != nil {
		return fmt.Errorf("invalid selection for type-binding %s: %w", typeName, err)
	}
	return nil
}
