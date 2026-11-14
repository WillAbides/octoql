package generate

import (
	"fmt"
	"go/token"
	"go/types"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// makeIdentifier takes a string and returns a valid go identifier like it.
//
// If the string is an identifier, return the input. Otherwise, munge it to
// make a valid identifier, which at worst (if the input is entirely emoji,
// say) means coming up with one out of whole cloth. This identifier need not
// be particularly unique; the caller may add a suffix.
func makeIdentifier(candidateIdentifier string) string {
	if token.IsIdentifier(candidateIdentifier) {
		return candidateIdentifier
	}

	var goodChars strings.Builder
	for _, c := range candidateIdentifier {
		// modified from token.IsIdentifier
		if unicode.IsLetter(c) || c == '_' ||
			// digits only valid after first char
			goodChars.Len() > 0 && unicode.IsDigit(c) {
			goodChars.WriteRune(c)
		}
	}
	if goodChars.Len() > 0 {
		return goodChars.String()
	}

	return "alias"
}

func allocateIdentifier(base string, used map[string]bool) string {
	identifier := base
	suffix := 2
	for used[identifier] {
		identifier = base + strconv.Itoa(suffix)
		suffix++
	}
	used[identifier] = true
	return identifier
}

var sliceOrMapPrefixRegexp = regexp.MustCompile(`^(\*|\[\d*\]|map\[string\])*`)

// goRef is a parsed, destination-neutral Go type or package-qualified symbol
// reference. It resolves against a target package's importScope at render time.
type goRef struct {
	prefix    string
	pkgPath   string
	localName string
}

func (r goRef) isZero() bool {
	return r.localName == ""
}

func (r goRef) qualified() string {
	if r.pkgPath == "" {
		return r.prefix + r.localName
	}
	return r.prefix + r.pkgPath + "." + r.localName
}

// isNilable reports whether a Go type reference denotes a type whose outermost
// value can hold nil (pointers, slices, maps, and interface{}), excluding
// fixed-size arrays [N]T. parseRef normalizes bare `any` to `interface{}`.
func (r goRef) isNilable() bool {
	if r.prefix == "" {
		return r.localName == "interface{}"
	}
	return strings.HasPrefix(r.prefix, "*") ||
		strings.HasPrefix(r.prefix, "[]") ||
		strings.HasPrefix(r.prefix, "map[")
}

func (r goRef) render(scope *importScope) (string, error) {
	if r.pkgPath == "" {
		return r.prefix + r.localName, nil
	}
	if r.pkgPath == scope.forbiddenPkgPath {
		return "", errorf(
			nil,
			"test_handler.types local cannot use %q because it is owned by the generated client package %q",
			r.qualified(),
			r.pkgPath,
		)
	}
	if r.pkgPath == scope.pkgPath {
		return r.prefix + r.localName, nil
	}
	alias := scope.importPackage(r.pkgPath)
	return r.prefix + alias + "." + r.localName, nil
}

// parseRef parses and validates a Go type or symbol expression:
//   - any package-qualified symbol (path/to/package.Name)
//   - any predeclared basic type (string, int, etc.)
//   - interface{} (bare `any` is normalized to `interface{}` so local
//     declarations cannot shadow it)
//   - for any allowed type T: *T, []T, [N]T, and map[string]T
func parseRef(fullyQualifiedName string) (goRef, error) {
	const errorMsg = `invalid type-name "%v" (%v); expected a builtin, ` +
		`path/to/package.Name, interface{}, or a slice, map, or pointer of those`

	if strings.Contains(fullyQualifiedName, " ") {
		return goRef{}, errorf(nil, errorMsg, fullyQualifiedName, "contains spaces")
	}

	prefix := sliceOrMapPrefixRegexp.FindString(fullyQualifiedName)
	nameToImport := fullyQualifiedName[len(prefix):]

	pkgPath, localName, hasPkg := strings.CutLast(nameToImport, ".")
	if !hasPkg {
		if nameToImport == "any" {
			return goRef{
				prefix:    prefix,
				localName: "interface{}",
			}, nil
		}
		if nameToImport != "interface{}" && types.Universe.Lookup(nameToImport) == nil {
			return goRef{}, errorf(
				nil,
				errorMsg,
				fullyQualifiedName,
				fmt.Sprintf(`unknown type-name "%v"`, nameToImport),
			)
		}
		return goRef{
			prefix:    prefix,
			localName: nameToImport,
		}, nil
	}

	return goRef{
		prefix:    prefix,
		pkgPath:   pkgPath,
		localName: localName,
	}, nil
}

// importScope tracks imports and alias collisions for one generated Go file.
type importScope struct {
	pkgPath          string
	forbiddenPkgPath string
	imports          map[string]string
	usedAliases      map[string]bool
}

func newImportScope(pkgPath, forbiddenPkgPath string) *importScope {
	return &importScope{
		pkgPath:          pkgPath,
		forbiddenPkgPath: forbiddenPkgPath,
		imports:          map[string]string{},
		usedAliases:      map[string]bool{},
	}
}

func (s *importScope) importPackage(pkgPath string) string {
	alias, ok := s.imports[pkgPath]
	if ok {
		return alias
	}
	pkgName := makeIdentifier(pkgPath[strings.LastIndex(pkgPath, "/")+1:])
	alias = allocateIdentifier(pkgName, s.usedAliases)
	s.imports[pkgPath] = alias
	return alias
}

func (s *importScope) ref(fullyQualifiedName string) (string, error) {
	parsed, err := parseRef(fullyQualifiedName)
	if err != nil {
		return "", err
	}
	return parsed.render(s)
}

func (s *importScope) declaration() string {
	if len(s.imports) == 0 {
		return ""
	}

	paths := make([]string, 0, len(s.imports))
	for pkgPath := range s.imports {
		paths = append(paths, pkgPath)
	}
	sort.Strings(paths)

	var builder strings.Builder
	builder.WriteString("import (\n")
	for _, pkgPath := range paths {
		alias := s.imports[pkgPath]
		if pkgPath == alias || strings.HasSuffix(pkgPath, "/"+alias) {
			builder.WriteString("\t" + strconv.Quote(pkgPath) + "\n")
			continue
		}
		builder.WriteString("\t" + alias + " " + strconv.Quote(pkgPath) + "\n")
	}
	builder.WriteString(")\n\n")
	return builder.String()
}

// registerBoundTypeImports pre-registers external type bindings in sorted
// typeMap order so user-bound packages claim their natural package aliases
// before generated runtime or helper stdlib imports.
func registerBoundTypeImports(typeMap map[string]goType, scope *importScope) error {
	seen := make(map[goType]bool, len(typeMap))
	var visit func(goType) error
	visit = func(typ goType) error {
		if seen[typ] {
			return nil
		}
		seen[typ] = true

		switch current := typ.(type) {
		case *goOpaqueType:
			_, err := current.Ref.render(scope)
			return err
		case *goSliceType:
			return visit(current.Elem)
		case *goPointerType:
			return visit(current.Elem)
		case *goStructType:
			for _, field := range current.Fields {
				err := visit(field.GoType)
				if err != nil {
					return err
				}
			}
		case *goInterfaceType:
			for _, field := range current.SharedFields {
				err := visit(field.GoType)
				if err != nil {
					return err
				}
			}
			for _, impl := range current.Implementations {
				err := visit(impl)
				if err != nil {
					return err
				}
			}
			if current.OtherImplementation != nil {
				return visit(current.OtherImplementation)
			}
		}
		return nil
	}

	names := make([]string, 0, len(typeMap))
	for name := range typeMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		err := visit(typeMap[name])
		if err != nil {
			return err
		}
	}
	return nil
}
