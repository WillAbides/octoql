package generate

import "sort"

func cloneTypeMap(
	source map[string]goType,
) (map[string]goType, map[goType]goType, error) {
	cloner := typeCloner{cloned: make(map[goType]goType, len(source))}
	result := make(map[string]goType, len(source))
	for name, typ := range source {
		cloned, err := cloner.clone(typ)
		if err != nil {
			return nil, nil, err
		}
		result[name] = cloned
	}
	return result, cloner.cloned, nil
}

type typeCloner struct {
	cloned map[goType]goType
}

func (cloner *typeCloner) clone(typ goType) (goType, error) {
	if cloned := cloner.cloned[typ]; cloned != nil {
		return cloned, nil
	}

	switch source := typ.(type) {
	case *goOpaqueType:
		cloned := *source
		cloner.cloned[typ] = &cloned
		return &cloned, nil
	case *goTypenameForBuiltinType:
		cloned := *source
		cloner.cloned[typ] = &cloned
		return &cloned, nil
	case *goSliceType:
		cloned := &goSliceType{}
		cloner.cloned[typ] = cloned
		elem, err := cloner.clone(source.Elem)
		if err != nil {
			return nil, err
		}
		cloned.Elem = elem
		return cloned, nil
	case *goPointerType:
		cloned := &goPointerType{}
		cloner.cloned[typ] = cloned
		elem, err := cloner.clone(source.Elem)
		if err != nil {
			return nil, err
		}
		cloned.Elem = elem
		return cloned, nil
	case *goGenericType:
		cloned := &goGenericType{
			GoGenericRef:          source.GoGenericRef,
			QualifiedGoGenericRef: source.QualifiedGoGenericRef,
		}
		cloner.cloned[typ] = cloned
		elem, err := cloner.clone(source.Elem)
		if err != nil {
			return nil, err
		}
		cloned.Elem = elem
		return cloned, nil
	case *goEnumType:
		cloned := *source
		cloned.Values = append([]goEnumValue{}, source.Values...)
		cloner.cloned[typ] = &cloned
		return &cloned, nil
	case *goStructType:
		cloned := *source
		cloned.Fields = nil
		cloner.cloned[typ] = &cloned
		fields, err := cloner.cloneFields(source.Fields)
		if err != nil {
			return nil, err
		}
		cloned.Fields = fields
		return &cloned, nil
	case *goInterfaceType:
		cloned := *source
		cloned.SharedFields = nil
		cloned.Implementations = nil
		cloned.OtherImplementation = nil
		cloner.cloned[typ] = &cloned
		sharedFields, err := cloner.cloneFields(source.SharedFields)
		if err != nil {
			return nil, err
		}
		cloned.SharedFields = sharedFields
		cloned.Implementations = make([]*goStructType, 0, len(source.Implementations))
		for _, implementation := range source.Implementations {
			clonedType, cloneErr := cloner.clone(implementation)
			if cloneErr != nil {
				return nil, cloneErr
			}
			clonedImplementation, ok := clonedType.(*goStructType)
			if !ok {
				return nil, errorf(
					nil,
					"internal error: cloned interface implementation was %T",
					clonedType,
				)
			}
			cloned.Implementations = append(cloned.Implementations, clonedImplementation)
		}
		if source.OtherImplementation != nil {
			clonedType, cloneErr := cloner.clone(source.OtherImplementation)
			if cloneErr != nil {
				return nil, cloneErr
			}
			clonedOther, ok := clonedType.(*goStructType)
			if !ok {
				return nil, errorf(
					nil,
					"internal error: cloned catch-all implementation was %T",
					clonedType,
				)
			}
			cloned.OtherImplementation = clonedOther
		}
		return &cloned, nil
	default:
		return nil, errorf(nil, "internal error: cannot clone Go type %T", typ)
	}
}

func (cloner *typeCloner) cloneFields(
	source []*goStructField,
) ([]*goStructField, error) {
	cloned := make([]*goStructField, 0, len(source))
	for _, field := range source {
		clonedField := *field
		clonedType, err := cloner.clone(field.GoType)
		if err != nil {
			return nil, err
		}
		clonedField.GoType = clonedType
		cloned = append(cloned, &clonedField)
	}
	return cloned, nil
}

func resolveRendererReferences(g *generator) error {
	seen := make(map[goType]bool, len(g.typeMap))
	var resolve func(goType) error
	resolve = func(typ goType) error {
		if seen[typ] {
			return nil
		}
		seen[typ] = true

		switch current := typ.(type) {
		case *goOpaqueType:
			if current.QualifiedGoRef == "" {
				return nil
			}
			resolved, err := g.ref(current.QualifiedGoRef)
			if err != nil {
				return err
			}
			current.GoRef = resolved
		case *goSliceType:
			return resolve(current.Elem)
		case *goPointerType:
			return resolve(current.Elem)
		case *goGenericType:
			if current.QualifiedGoGenericRef != "" {
				resolved, err := g.ref(current.QualifiedGoGenericRef)
				if err != nil {
					return err
				}
				current.GoGenericRef = resolved
			}
			return resolve(current.Elem)
		case *goStructType:
			for _, field := range current.Fields {
				err := resolve(field.GoType)
				if err != nil {
					return err
				}
			}
		case *goInterfaceType:
			for _, field := range current.SharedFields {
				err := resolve(field.GoType)
				if err != nil {
					return err
				}
			}
			for _, implementation := range current.Implementations {
				err := resolve(implementation)
				if err != nil {
					return err
				}
			}
			if current.OtherImplementation != nil {
				return resolve(current.OtherImplementation)
			}
		}
		return nil
	}

	typeNames := make([]string, 0, len(g.typeMap))
	for name := range g.typeMap {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		err := resolve(g.typeMap[name])
		if err != nil {
			return err
		}
	}
	return nil
}
