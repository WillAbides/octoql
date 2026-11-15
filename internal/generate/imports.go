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

	"golang.org/x/mod/module"
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

// goRef is a parsed Go type or package-qualified symbol reference. It resolves
// against a target package's importScope at render time.
type goRef struct {
	prefix    string
	pkgPath   string
	localName string
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

func (r goRef) render(scope *importScope) string {
	if r.pkgPath == "" || r.pkgPath == scope.pkgPath {
		return r.prefix + r.localName
	}
	alias := scope.importPackage(r.pkgPath)
	return r.prefix + alias + "." + r.localName
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
	pkgPath     string
	imports     map[string]string
	usedAliases map[string]bool
}

func newImportScope(pkgPath string) *importScope {
	return &importScope{
		pkgPath:     pkgPath,
		imports:     map[string]string{},
		usedAliases: map[string]bool{},
	}
}

func (s *importScope) importPackage(pkgPath string) string {
	alias, ok := s.imports[pkgPath]
	if ok {
		return alias
	}
	basePath := pkgPath
	unversioned, _, splitOK := module.SplitPathVersion(pkgPath)
	if splitOK {
		basePath = unversioned
	}
	pkgName := makeIdentifier(basePath[strings.LastIndex(basePath, "/")+1:])
	alias = allocateIdentifier(pkgName, s.usedAliases)
	s.imports[pkgPath] = alias
	return alias
}

func (s *importScope) ref(fullyQualifiedName string) (string, error) {
	parsed, err := parseRef(fullyQualifiedName)
	if err != nil {
		return "", err
	}
	return parsed.render(s), nil
}

func (s *importScope) declaration() string {
	if len(s.imports) == 0 {
		return ""
	}

	var standard, external []string
	for pkgPath := range s.imports {
		first, _, _ := strings.Cut(pkgPath, "/")
		if strings.Contains(first, ".") {
			external = append(external, pkgPath)
			continue
		}
		standard = append(standard, pkgPath)
	}
	sort.Strings(standard)
	sort.Strings(external)

	var builder strings.Builder
	builder.WriteString("import (\n")
	for index, group := range [][]string{standard, external} {
		if index == 1 && len(standard) > 0 && len(external) > 0 {
			builder.WriteByte('\n')
		}
		for _, pkgPath := range group {
			alias := s.imports[pkgPath]
			if index == 0 && (pkgPath == alias || strings.HasSuffix(pkgPath, "/"+alias)) {
				builder.WriteString("\t" + strconv.Quote(pkgPath) + "\n")
				continue
			}
			builder.WriteString("\t" + alias + " " + strconv.Quote(pkgPath) + "\n")
		}
	}
	builder.WriteString(")\n\n")
	return builder.String()
}

// registerBoundTypeImports pre-registers external type bindings in sorted
// typeMap order so user-bound packages claim their natural package aliases
// before generated runtime or helper stdlib imports.
func registerBoundTypeImports(typeMap map[string]goType, scope *importScope) {
	seen := make(map[goType]bool, len(typeMap))
	var visit func(goType)
	visit = func(typ goType) {
		if seen[typ] {
			return
		}
		seen[typ] = true

		switch current := typ.(type) {
		case *goOpaqueType:
			current.Ref.render(scope)
		case *goSliceType:
			visit(current.Elem)
		case *goPointerType:
			visit(current.Elem)
		case *goStructType:
			for _, field := range current.Fields {
				visit(field.GoType)
			}
		case *goInterfaceType:
			for _, field := range current.SharedFields {
				visit(field.GoType)
			}
			for _, impl := range current.Implementations {
				visit(impl)
			}
			visit(current.OtherImplementation)
		}
	}

	names := make([]string, 0, len(typeMap))
	for name := range typeMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		visit(typeMap[name])
	}
}
