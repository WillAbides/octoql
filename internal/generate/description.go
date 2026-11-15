package generate

// Code relating to generating GoDoc from GraphQL descriptions.
//
// For fields, and types where we just copy the "whole" type (enum and
// input-object), this is easy: we just use the GraphQL description.  But for
// output struct and interface types, there are often more useful things we can
// say.

import (
	"fmt"
	"strings"
)

// descriptionInfo is embedded in types whose descriptions may be more complex
// than just a copy of the GraphQL doc.
type descriptionInfo struct {
	// custom comment override for this type, if any
	CommentOverride string
	// name of the corresponding GraphQL type
	GraphQLName string
	// GraphQL schema's description of the type .GraphQLName, if any
	GraphQLDescription string
	// name of the corresponding GraphQL fragment, if any
	FragmentName string
	// whether the corresponding GraphQL type is a union rather than an interface
	IsUnion bool
}

func maybeAddTypeDescription(info descriptionInfo, description string) string {
	if info.GraphQLDescription == "" {
		return description
	}
	return fmt.Sprintf(
		"%v\nThe GraphQL type's documentation follows.\n\n%v",
		description, info.GraphQLDescription,
	)
}

func fragmentDescription(goName string, info descriptionInfo) string {
	return maybeAddTypeDescription(info, fmt.Sprintf(
		"%v includes the GraphQL fields of %v requested by the fragment %v.",
		goName, info.GraphQLName, info.FragmentName,
	))
}

func structDescription(typ *goStructType) string {
	switch {
	case typ.CommentOverride != "":
		return typ.CommentOverride
	case typ.IsInput:
		// Input types have all their fields, just use the GraphQL description.
		return typ.GraphQLDescription
	case typ.FragmentName != "":
		return fragmentDescription(typ.GoName, typ.descriptionInfo)
	default:
		// For types where we only have some fields, note that, along with
		// the GraphQL documentation (if any).  We don't want to just use
		// the GraphQL documentation, since it may refer to fields we
		// haven't selected, say.
		return maybeAddTypeDescription(typ.descriptionInfo, fmt.Sprintf(
			"%v includes the requested fields of the GraphQL type %v.",
			typ.GoName, typ.GraphQLName,
		))
	}
}

func interfaceDescription(typ *goInterfaceType) string {
	goImplNames := make([]string, 0, len(typ.Implementations)+1)
	for _, impl := range typ.Implementations {
		goImplNames = append(goImplNames, impl.GoName)
	}
	goImplNames = append(goImplNames, typ.OtherImplementation.GoName)
	implementationList := fmt.Sprintf(
		"\n\n%v is implemented by the following types:\n\t%v",
		typ.GoName, strings.Join(goImplNames, "\n\t"),
	)

	switch {
	case typ.CommentOverride != "":
		return typ.CommentOverride + implementationList
	case typ.FragmentName != "":
		return fragmentDescription(typ.GoName, typ.descriptionInfo) + implementationList
	default:
		kind := "interface"
		if typ.IsUnion {
			kind = "union"
		}
		return maybeAddTypeDescription(typ.descriptionInfo, fmt.Sprintf(
			"%v includes the requested fields of the GraphQL %s %v.%v",
			typ.GoName, kind, typ.GraphQLName, implementationList,
		))
	}
}
