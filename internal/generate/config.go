package generate

import (
	"fmt"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
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

// Config contains the normalized settings used by the generator implementation.
type Config struct {
	Schema                          StringList
	Operations                      StringList
	ConfigFile                      string
	Generated                       string
	TestHandlerGenerated            string
	TestHandlerTypes                TestHandlerTypeStrategy
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
	// The package name and path for the generated test handler.
	testHandlerPackage string
	testHandlerPkgPath string
}

// TestHandlerTypeStrategy controls where generated test handlers get operation types.
type TestHandlerTypeStrategy string

const (
	TestHandlerTypesClient TestHandlerTypeStrategy = "client"
	TestHandlerTypesLocal  TestHandlerTypeStrategy = "local"
)

func (s TestHandlerTypeStrategy) validate() error {
	switch s {
	case TestHandlerTypesClient, TestHandlerTypesLocal:
		return nil
	default:
		return errorf(nil, "test_handler.types must be one of: 'client' or 'local'")
	}
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

func (a CasingAlgorithm) validate() error {
	switch a {
	case CasingDefault, CasingRaw, CasingAutoCamelCase:
		return nil
	default:
		return errorf(nil, "unknown casing algorithm: %s", a)
	}
}

// Casing configures GraphQL-to-Go name conversion.
type Casing struct {
	Default  CasingAlgorithm
	AllEnums CasingAlgorithm
	Enums    map[string]CasingAlgorithm
}

func (c *Casing) validate() error {
	if c.Default != "" {
		if err := c.Default.validate(); err != nil {
			return err
		}
	}
	if c.AllEnums != "" {
		if err := c.AllEnums.validate(); err != nil {
			return err
		}
	}
	for _, algo := range c.Enums {
		if err := algo.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Casing) getDefault() CasingAlgorithm {
	if c.Default != "" {
		return c.Default
	}
	return CasingDefault
}

func (c *Casing) forEnum(graphQLTypeName string) CasingAlgorithm {
	if specificConfig, ok := c.Enums[graphQLTypeName]; ok {
		return specificConfig
	}
	if c.AllEnums != "" {
		return c.AllEnums
	}
	return c.getDefault()
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
		modulePkgPath, moduleErr := packagePathFromModule(dir)
		if moduleErr == nil && pkgNameGuess != "" {
			return pkgNameGuess, modulePkgPath, nil
		}
		return pkgNameGuess, "", err
	} else if len(pkgs) != 1 { // probably never happens?
		modulePkgPath, moduleErr := packagePathFromModule(dir)
		if moduleErr == nil && pkgNameGuess != "" {
			return pkgNameGuess, modulePkgPath, nil
		}
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
	if pkg.PkgPath == "" || pkg.PkgPath == "command-line-arguments" {
		modulePkgPath, moduleErr := packagePathFromModule(dir)
		if moduleErr == nil {
			pkg.PkgPath = modulePkgPath
		}
	}

	if pkgNameGuess != "" {
		return pkgNameGuess, pkg.PkgPath, nil
	} else {
		return "", "", fmt.Errorf("no package found in %v", dir)
	}
}

func packagePathFromModule(directory string) (string, error) {
	targetDirectory := directory
	currentDirectory := directory
	for {
		moduleFile := filepath.Join(currentDirectory, "go.mod")
		content, err := os.ReadFile(moduleFile)
		if err == nil {
			moduleName := modulePath(content)
			if moduleName == "" {
				return "", fmt.Errorf("go.mod in %q has no module path", currentDirectory)
			}

			relative, relativeErr := filepath.Rel(currentDirectory, targetDirectory)
			if relativeErr != nil {
				return "", relativeErr
			}
			if relative == "." {
				return moduleName, nil
			}
			return path.Join(moduleName, filepath.ToSlash(relative)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(currentDirectory)
		if parent == currentDirectory {
			return "", fmt.Errorf("no go.mod found for %q", directory)
		}
		currentDirectory = parent
	}
}

func modulePath(goMod []byte) string {
	for line := range strings.SplitSeq(string(goMod), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "module" {
			continue
		}
		if !strings.HasPrefix(fields[1], `"`) {
			return fields[1]
		}
		unquoted, err := strconv.Unquote(fields[1])
		if err == nil {
			return unquoted
		}
		return ""
	}
	return ""
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
	if c.ConfigFile != "" {
		c.ConfigFile = pathJoin(baseDir, c.ConfigFile)
	}
	if c.Generated == "" {
		c.Generated = "generated.go"
	}
	c.Generated = pathJoin(baseDir, c.Generated)
	if c.TestHandlerGenerated != "" {
		c.TestHandlerGenerated = pathJoin(baseDir, c.TestHandlerGenerated)
	}
	if c.ExportOperations != "" {
		c.ExportOperations = pathJoin(baseDir, c.ExportOperations)
	}
	err := c.validateOutputPaths()
	if err != nil {
		return err
	}

	if c.ContextType == "" {
		c.ContextType = "context.Context"
	}
	if c.OmitUnreferencedImplementations == nil {
		omit := true
		c.OmitUnreferencedImplementations = &omit
	}
	if c.TestHandlerTypes == "" {
		c.TestHandlerTypes = TestHandlerTypesClient
	}
	err = c.TestHandlerTypes.validate()
	if err != nil {
		return err
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

	if c.TestHandlerGenerated != "" {
		if c.TestHandlerTypes == TestHandlerTypesClient && c.pkgPath == "" {
			return errorf(
				nil,
				"unable to generate test handler without identifying the generated client package path",
			)
		}
		if c.TestHandlerTypes == TestHandlerTypesClient && c.Package == "main" {
			return errorf(
				nil,
				"test handler cannot import a generated client in package main",
			)
		}

		handlerPackage, handlerPkgPath, handlerErr := getPackageNameAndPath(c.TestHandlerGenerated)
		if handlerErr != nil {
			return errorf(
				nil,
				"unable to identify test handler package for %q: %v",
				c.TestHandlerGenerated,
				handlerErr,
			)
		}
		if handlerPackage == "" || handlerPkgPath == "" {
			return errorf(
				nil,
				"unable to identify test handler package for %q",
				c.TestHandlerGenerated,
			)
		}
		sameDirectory := filepath.Dir(c.TestHandlerGenerated) == filepath.Dir(c.Generated)
		samePackagePath := c.pkgPath != "" && handlerPkgPath == c.pkgPath
		if sameDirectory || samePackagePath {
			return errorf(
				nil,
				"test handler must be generated in a separate package from the client",
			)
		}
		c.testHandlerPackage = handlerPackage
		c.testHandlerPkgPath = handlerPkgPath
	}

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

func (c *Config) validateOutputPaths() error {
	outputs, err := c.outputPaths()
	if err != nil {
		return err
	}
	if c.ConfigFile != "" {
		err = validateProtectedPath(outputs, "config", c.ConfigFile)
		if err != nil {
			return err
		}
	}
	for _, input := range []struct {
		name  string
		paths StringList
	}{
		{name: "schema input", paths: c.Schema},
		{name: "operations input", paths: c.Operations},
	} {
		for _, filename := range input.paths {
			err = validateProtectedPath(outputs, input.name, filename)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProtectedPath(outputs []inspectedPath, name, filename string) error {
	protected, err := inspectPath(name, filename)
	if err != nil {
		return errorf(nil, "resolving %s path %q: %v", name, filename, err)
	}
	for _, output := range outputs {
		if pathsAlias(output, protected) {
			return errorf(
				nil,
				"%s output path must be different from %s path: %q",
				output.name,
				name,
				output.path,
			)
		}
	}
	return nil
}

type inspectedPath struct {
	name      string
	path      string
	canonical string
	info      os.FileInfo
}

func (c *Config) outputPaths() ([]inspectedPath, error) {
	var outputs []inspectedPath
	for _, output := range []struct {
		name string
		path string
	}{
		{name: "generated", path: c.Generated},
		{name: "export_operations", path: c.ExportOperations},
		{name: "test_handler.generated", path: c.TestHandlerGenerated},
	} {
		if output.path == "" {
			continue
		}
		inspected, err := inspectPath(output.name, output.path)
		if err != nil {
			return nil, errorf(
				nil,
				"resolving %s output path %q: %v",
				output.name,
				output.path,
				err,
			)
		}
		for _, existing := range outputs {
			if pathsAlias(existing, inspected) {
				return nil, errorf(
					nil,
					"%s and %s output paths must be different: %q",
					existing.name,
					output.name,
					inspected.canonical,
				)
			}
		}
		outputs = append(outputs, inspected)
	}
	return outputs, nil
}

func (c *Config) validateInputPaths() error {
	outputs, err := c.outputPaths()
	if err != nil {
		return err
	}
	inputs := []struct {
		name  string
		globs StringList
	}{
		{name: "schema", globs: c.Schema},
		{name: "operations", globs: c.Operations},
	}
	for _, input := range inputs {
		filenames, expandErr := expandFilenames(input.globs)
		if expandErr != nil {
			return expandErr
		}
		for _, filename := range filenames {
			inspected, inspectErr := inspectPath(input.name, filename)
			if inspectErr != nil {
				return errorf(
					nil,
					"resolving %s input path %q: %v",
					input.name,
					filename,
					inspectErr,
				)
			}
			for _, output := range outputs {
				if pathsAlias(output, inspected) {
					return errorf(
						nil,
						"%s output path must be different from %s input path: %q",
						output.name,
						input.name,
						output.path,
					)
				}
			}
		}
	}
	return nil
}

func inspectPath(name, filename string) (inspectedPath, error) {
	info, err := os.Stat(filename)
	if err != nil && !os.IsNotExist(err) {
		return inspectedPath{}, err
	}
	canonical, err := canonicalOutputPath(filename)
	if err != nil {
		return inspectedPath{}, err
	}
	return inspectedPath{
		name:      name,
		path:      filename,
		canonical: canonical,
		info:      info,
	}, nil
}

func pathsAlias(left, right inspectedPath) bool {
	if left.info != nil && right.info != nil && os.SameFile(left.info, right.info) {
		return true
	}
	return left.canonical == right.canonical
}

func canonicalOutputPath(filename string) (string, error) {
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	resolved, existingAncestor, err := resolveOutputPath(absolute, 0)
	if err != nil {
		return "", err
	}
	resolved = filepath.Clean(resolved)
	if filesystemCaseInsensitive(existingAncestor) {
		resolved = strings.ToLower(resolved)
	}
	return resolved, nil
}

func resolveOutputPath(
	absolute string,
	symlinkDepth int,
) (resolved, existingAncestor string, err error) {
	if symlinkDepth > 255 {
		return "", "", fmt.Errorf("too many symlinks resolving %q", absolute)
	}

	volume := filepath.VolumeName(absolute)
	root := volume + string(filepath.Separator)
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		return "", "", err
	}
	components := strings.Split(relative, string(filepath.Separator))
	resolved = root
	for index, component := range components {
		if component == "." || component == "" {
			continue
		}

		candidate := filepath.Join(resolved, component)
		info, lstatErr := os.Lstat(candidate)
		if os.IsNotExist(lstatErr) {
			return filepath.Join(
				resolved,
				filepath.Join(components[index:]...),
			), resolved, nil
		}
		if lstatErr != nil {
			return "", "", lstatErr
		}
		if info.Mode()&os.ModeSymlink == 0 {
			resolved = candidate
			continue
		}

		target, readErr := os.Readlink(candidate)
		if readErr != nil {
			return "", "", readErr
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(resolved, target)
		}
		if index+1 < len(components) {
			target = filepath.Join(
				target,
				filepath.Join(components[index+1:]...),
			)
		}
		target, err = filepath.Abs(target)
		if err != nil {
			return "", "", err
		}
		return resolveOutputPath(target, symlinkDepth+1)
	}
	return resolved, resolved, nil
}

func filesystemCaseInsensitive(existingPath string) bool {
	current := existingPath
	for {
		info, err := os.Stat(current)
		if err == nil {
			alternateBase := toggledCase(filepath.Base(current))
			if alternateBase != filepath.Base(current) {
				alternate := filepath.Join(filepath.Dir(current), alternateBase)
				alternateInfo, alternateErr := os.Stat(alternate)
				if alternateErr == nil && os.SameFile(info, alternateInfo) {
					return true
				}
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

func toggledCase(value string) string {
	for index, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			return value[:index] + string(char-'a'+'A') + value[index+1:]
		case char >= 'A' && char <= 'Z':
			return value[:index] + string(char-'A'+'a') + value[index+1:]
		}
	}
	return value
}

func (c *Config) omitUnreferencedImplementations() bool {
	return c.OmitUnreferencedImplementations == nil || *c.OmitUnreferencedImplementations
}

// GetDefaultCasingAlgorithm returns the default casing algorithm for the config.
func (c *Config) GetDefaultCasingAlgorithm() CasingAlgorithm {
	return c.Casing.getDefault()
}
