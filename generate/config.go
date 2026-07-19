package generate

import (
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

func warn(err error) error {
	_, writeErr := fmt.Fprintln(os.Stderr, err)
	if writeErr != nil {
		return fmt.Errorf("writing warning: %w", writeErr)
	}
	return nil
}

// Config controls one programmatic generation run.
// Callers must call [Config.ValidateAndFillDefaults] before calling [Generate].
type Config struct {
	Schema                          StringList
	Operations                      StringList
	Generated                       string
	Package                         string
	ExportOperations                string
	ContextType                     string
	ClientGetter                    string
	Bindings                        map[string]*TypeBinding
	PackageBindings                 []*PackageBinding
	Casing                          Casing
	Optional                        string
	OptionalGenericType             string
	StructReferences                bool
	OmitUnreferencedImplementations *bool

	// The directory of the config-file (relative to which all the other paths
	// are resolved).  Set by ValidateAndFillDefaults.
	baseDir string
	// The package-path into which we are generating.
	pkgPath string
}

// A TypeBinding represents a Go type to which octoqlgen binds a GraphQL type.
type TypeBinding struct {
	Type              string
	ExpectExactFields string
	Marshaler         string
	Unmarshaler       string
}

// A PackageBinding represents a Go package whose exported types become bindings.
type PackageBinding struct {
	Package string
}

// CasingAlgorithm represents a GraphQL-to-Go name conversion algorithm.
type CasingAlgorithm string

const (
	CasingDefault       CasingAlgorithm = "default"
	CasingRaw           CasingAlgorithm = "raw"
	CasingAutoCamelCase CasingAlgorithm = "auto_camel_case"
)

func (algo CasingAlgorithm) validate() error {
	switch algo {
	case CasingDefault, CasingRaw, CasingAutoCamelCase:
		return nil
	default:
		return errorf(nil, "unknown casing algorithm: %s", algo)
	}
}

// Casing configures GraphQL-to-Go name conversion.
type Casing struct {
	Default  CasingAlgorithm
	AllEnums CasingAlgorithm
	Enums    map[string]CasingAlgorithm
}

func (casing *Casing) validate() error {
	if casing.Default != "" {
		if err := casing.Default.validate(); err != nil {
			return err
		}
	}
	if casing.AllEnums != "" {
		if err := casing.AllEnums.validate(); err != nil {
			return err
		}
	}
	for _, algo := range casing.Enums {
		if err := algo.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (casing *Casing) getDefault() CasingAlgorithm {
	if casing.Default != "" {
		return casing.Default
	}
	return CasingDefault
}

func (casing *Casing) forEnum(graphQLTypeName string) CasingAlgorithm {
	if specificConfig, ok := casing.Enums[graphQLTypeName]; ok {
		return specificConfig
	}
	if casing.AllEnums != "" {
		return casing.AllEnums
	}
	return casing.getDefault()
}

// pathJoin is like filepath.Join but 1) it only takes two arguments,
// and 2) if the second argument is an absolute path the first argument
// is ignored (similar to how python's os.path.join() works).
func pathJoin(a, b string) string {
	if filepath.IsAbs(b) {
		return b
	}
	return filepath.Join(a, b)
}

// Try to figure out the package-name and package-path of the given .go file.
//
// Returns a best-guess pkgName if possible, even on error.
func getPackageNameAndPath(filename string) (pkgName, pkgPath string, err error) {
	abs, err := filepath.Abs(filename)
	if err != nil { // path is totally bogus
		return "", "", err
	}

	dir := filepath.Dir(abs)
	// If we don't get a clean answer from go/packages, we'll use the
	// directory-name as a backup guess, as long as it's a valid identifier.
	pkgNameGuess := filepath.Base(dir)
	if !token.IsIdentifier(pkgNameGuess) {
		pkgNameGuess = ""
	}

	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName}, dir)
	if err != nil { // e.g. not in a Go module
		return pkgNameGuess, "", err
	} else if len(pkgs) != 1 { // probably never happens?
		return pkgNameGuess, "", fmt.Errorf("found %v packages in %v, expected 1", len(pkgs), dir)
	}

	pkg := pkgs[0]
	// TODO(benkraft): Can PkgPath ever be empty while in a module? If so, we
	// could warn.
	if pkg.Name != "" { // found a good package!
		return pkg.Name, pkg.PkgPath, nil
	}

	// Package path is valid, but name is empty: probably an empty package
	// (within a valid module). If the package-path-suffix is a valid
	// identifier, that's a better guess than the directory-suffix, so use it.
	pathSuffix := filepath.Base(pkg.PkgPath)
	if token.IsIdentifier(pathSuffix) {
		pkgNameGuess = pathSuffix
	}

	if pkgNameGuess != "" {
		return pkgNameGuess, pkg.PkgPath, nil
	} else {
		return "", "", fmt.Errorf("no package found in %v", dir)
	}
}

// ValidateAndFillDefaults ensures that the configuration is valid, and fills
// in any options that were unspecified.
//
// The argument is the directory relative to which paths will be interpreted,
// typically the directory of the config file.
func (c *Config) ValidateAndFillDefaults(baseDir string) error {
	c.baseDir = baseDir
	for i := range c.Schema {
		c.Schema[i] = pathJoin(baseDir, c.Schema[i])
	}
	for i := range c.Operations {
		c.Operations[i] = pathJoin(baseDir, c.Operations[i])
	}
	if c.Generated == "" {
		c.Generated = "generated.go"
	}
	c.Generated = pathJoin(baseDir, c.Generated)
	if c.ExportOperations != "" {
		c.ExportOperations = pathJoin(baseDir, c.ExportOperations)
	}

	if c.ContextType == "" {
		c.ContextType = "context.Context"
	}
	if c.OmitUnreferencedImplementations == nil {
		omit := true
		c.OmitUnreferencedImplementations = &omit
	}

	if c.Optional != "" && c.Optional != "value" && c.Optional != "pointer" && c.Optional != "pointer_omitempty" && c.Optional != "generic" {
		return errorf(nil, "optional must be one of: 'value' (default), 'pointer', 'pointer_omitempty' or 'generic'")
	}

	if c.Optional == "generic" && c.OptionalGenericType == "" {
		return errorf(nil, "if optional is set to 'generic', optional_generic_type must be set to the fully"+
			"qualified name of a type with a single generic parameter"+
			"\nExample: \"github.com/Org/Repo/optional.Value\"")
	}

	if c.Package != "" && !token.IsIdentifier(c.Package) {
		// No need for link here -- if you're already setting the package
		// you know where to set the package.
		return errorf(nil, "invalid package in octoqlgen.yaml: '%v' is not a valid identifier", c.Package)
	}

	pkgName, pkgPath, err := getPackageNameAndPath(c.Generated)
	if err != nil {
		// Try to guess a name anyway (or use one you specified) -- pkgPath
		// isn't always needed. (But you'll run into trouble binding against
		// the generated package, so at least warn.)
		if c.Package != "" {
			warnErr := warn(errorf(nil, "warning: unable to identify current package-path "+
				"(using 'package' config '%v'): %v\n", c.Package, err))
			if warnErr != nil {
				return warnErr
			}
		} else if pkgName != "" {
			warnErr := warn(errorf(nil, "warning: unable to identify current package-path "+
				"(using directory name '%v': %v\n", pkgName, err))
			if warnErr != nil {
				return warnErr
			}
			c.Package = pkgName
		} else {
			return errorf(nil, "unable to guess package-name: %v"+
				"\nSet package name in octoqlgen.yaml"+
				"\nExample: https://github.com/willabides/octoql/blob/main/example/octoqlgen.yaml", err)
		}
	} else { // err == nil
		if c.Package == pkgName || c.Package == "" {
			c.Package = pkgName
		} else {
			warnErr := warn(errorf(nil, "warning: package setting in octoqlgen.yaml '%v' looks wrong "+
				"('%v' is in package '%v') but proceeding with '%v' anyway\n",
				c.Package, c.Generated, pkgName, c.Package))
			if warnErr != nil {
				return warnErr
			}
		}
	}
	// This is a no-op in some of the error cases, but it still doesn't hurt.
	c.pkgPath = pkgPath

	if len(c.PackageBindings) > 0 {
		for _, binding := range c.PackageBindings {
			if strings.HasSuffix(binding.Package, ".go") {
				// total heuristic -- but this is an easy mistake to make and
				// results in rather bizarre behavior from go/packages.
				return errorf(nil,
					"package %v looks like a file, but should be a package-name",
					binding.Package)
			}

			if binding.Package == c.pkgPath {
				warnErr := warn(errorf(nil, "warning: package_bindings set to the same package as your generated "+
					"code ('%v'); this may cause nondeterministic output due to circularity", c.pkgPath))
				if warnErr != nil {
					return warnErr
				}
			}

			mode := packages.NeedDeps | packages.NeedTypes
			pkgs, err := packages.Load(&packages.Config{
				Mode: mode,
			}, binding.Package)
			if err != nil {
				return err
			}

			if c.Bindings == nil {
				c.Bindings = map[string]*TypeBinding{}
			}

			for _, pkg := range pkgs {
				p := pkg.Types
				if p == nil || p.Scope() == nil || p.Scope().Len() == 0 {
					return errorf(nil, "unable to bind package %s: no types found", binding.Package)
				}

				for _, typ := range p.Scope().Names() {
					if token.IsExported(typ) {
						// Check if type is a manual binding
						_, exist := c.Bindings[typ]
						if !exist {
							pathType := fmt.Sprintf("%s.%s", p.Path(), typ)
							c.Bindings[typ] = &TypeBinding{
								Type: pathType,
							}
						}
					}
				}
			}
		}
	}

	if err := c.Casing.validate(); err != nil {
		return err
	}

	return nil
}

func (c *Config) omitUnreferencedImplementations() bool {
	return c.OmitUnreferencedImplementations == nil || *c.OmitUnreferencedImplementations
}

// GetDefaultCasingAlgorithm returns the default casing algorithm for the config.
func (c *Config) GetDefaultCasingAlgorithm() CasingAlgorithm {
	return c.Casing.getDefault()
}
