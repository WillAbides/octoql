package generate

import (
	"cmp"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/go/packages"
)

// Config contains generator settings. Generate normalizes unspecified values
// automatically, or callers can validate them before materializing the schema.
type Config struct {
	Schema                          string
	Operations                      []string
	ConfigFile                      string
	Generated                       string
	TestHandlerGenerated            string
	TestHandlerTypes                TestHandlerTypeStrategy
	Package                         string
	ExportOperations                string
	Bindings                        map[string]*TypeBinding
	PackageBindings                 []*PackageBinding
	Casing                          Casing
	StructReferences                bool
	OmitUnreferencedImplementations *bool
	Warnings                        io.Writer // Warnings are discarded when nil.

	// The directory of the config-file (relative to which all the other paths
	// are resolved).  Set by ValidateAndFillDefaults.
	baseDir string
	// The package-path into which we are generating.
	pkgPath string
	// The package name and path for the generated test handler.
	testHandlerPackage string
	testHandlerPkgPath string
	normalized         bool
	outputs            []inspectedPath
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
	err := c.Default.validate()
	if err != nil {
		return err
	}
	if c.AllEnums != "" {
		err = c.AllEnums.validate()
		if err != nil {
			return err
		}
	}
	for _, algo := range c.Enums {
		err = algo.validate()
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Casing) forEnum(graphQLTypeName string) CasingAlgorithm {
	specificConfig, ok := c.Enums[graphQLTypeName]
	if ok {
		return specificConfig
	}
	if c.AllEnums != "" {
		return c.AllEnums
	}
	return c.Default
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
// Without sibling Go files, prefer fallbackPackage to a path-based guess.
// Returns a best-guess pkgName if possible, even on error.
func getPackageNameAndPath(filename, fallbackPackage string) (string, string, error) {
	abs, err := filepath.Abs(filename)
	if err != nil { // path is totally bogus
		return "", "", err
	}

	dir := filepath.Dir(abs)
	pkgPath, moduleErr := packagePathFromModule(dir)
	pkgName := ""
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return "", pkgPath, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if filepath.Join(dir, name) == abs {
			continue
		}
		file, parseErr := parser.ParseFile(
			token.NewFileSet(), filepath.Join(dir, name), nil, parser.PackageClauseOnly,
		)
		if parseErr != nil {
			return "", pkgPath, parseErr
		}
		if pkgName != "" && pkgName != file.Name.Name {
			return "", pkgPath, fmt.Errorf("found packages %s and %s in %q", pkgName, file.Name.Name, dir)
		}
		pkgName = file.Name.Name
	}
	if pkgName == "" {
		pkgName = fallbackPackage
	}
	if pkgName == "" {
		pkgName = filepath.Base(dir)
		unversionedPath, _, ok := module.SplitPathVersion(pkgPath)
		pathSuffix := path.Base(unversionedPath)
		if ok && token.IsIdentifier(pathSuffix) {
			pkgName = pathSuffix
		}
		if !token.IsIdentifier(pkgName) {
			return "", pkgPath, fmt.Errorf("no package found in %v", dir)
		}
	}
	return pkgName, pkgPath, moduleErr
}

func packagePathFromModule(directory string) (string, error) {
	targetDirectory := directory
	currentDirectory := directory
	for {
		moduleFile := filepath.Join(currentDirectory, "go.mod")
		content, err := os.ReadFile(moduleFile)
		if err == nil {
			moduleName := modfile.ModulePath(content)
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

// ValidateAndFillDefaults ensures that the configuration is valid, and fills
// in any options that were unspecified. A successfully normalized Config is
// reused unchanged on later calls.
//
// The argument is the directory relative to which paths will be interpreted,
// typically the directory of the config file.
func (c *Config) ValidateAndFillDefaults(baseDir string) error {
	if c.normalized {
		return nil
	}
	c.Warnings = cmp.Or(c.Warnings, io.Discard)
	c.baseDir = baseDir
	if c.Schema != "" {
		c.Schema = pathJoin(baseDir, c.Schema)
	}
	for i := range c.Operations {
		c.Operations[i] = pathJoin(baseDir, c.Operations[i])
	}
	if c.ConfigFile != "" {
		c.ConfigFile = pathJoin(baseDir, c.ConfigFile)
	}
	c.Generated = pathJoin(baseDir, cmp.Or(c.Generated, "generated.go"))
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
	c.OmitUnreferencedImplementations = cmp.Or(c.OmitUnreferencedImplementations, new(true))
	c.TestHandlerTypes = cmp.Or(c.TestHandlerTypes, TestHandlerTypesClient)
	err = c.TestHandlerTypes.validate()
	if err != nil {
		return err
	}
	err = c.validatePackageNameAndPath()
	if err != nil {
		return err
	}
	for _, binding := range c.PackageBindings {
		err = c.validatePackageBinding(binding)
		if err != nil {
			return err
		}
	}

	if c.Casing.Default == "" {
		c.Casing.Default = CasingDefault
	}
	err = c.Casing.validate()
	if err != nil {
		return err
	}

	c.normalized = true
	return nil
}

func (c *Config) validatePackageNameAndPath() error {
	if c.Package != "" && !token.IsIdentifier(c.Package) {
		// No need for link here -- if you're already setting the package
		// you know where to set the package.
		return errorf(nil, "invalid package in octoqlgen.yaml: '%v' is not a valid identifier", c.Package)
	}

	pkgName, pkgPath, err := getPackageNameAndPath(c.Generated, c.Package)
	switch {
	case err != nil && c.Package != "":
		fmt.Fprintln(c.Warnings, errorf(nil, "warning: unable to identify current package-path "+
			"(using 'package' config '%v'): %v\n", c.Package, err))
	case err != nil && pkgName != "":
		fmt.Fprintln(c.Warnings, errorf(nil, "warning: unable to identify current package-path "+
			"(using directory name '%v': %v\n", pkgName, err))
		c.Package = pkgName
	case err != nil:
		return errorf(nil, "unable to guess package-name: %v"+
			"\nSet package name in octoqlgen.yaml"+
			"\nExample: https://github.com/willabides/octoql/blob/main/example/octoqlgen.yaml", err)
	case c.Package == pkgName || c.Package == "":
		c.Package = pkgName
	default:
		fmt.Fprintln(c.Warnings, errorf(nil, "warning: package setting in octoqlgen.yaml '%v' looks wrong "+
			"('%v' is in package '%v') but proceeding with '%v' anyway\n",
			c.Package, c.Generated, pkgName, c.Package))
	}
	c.pkgPath = pkgPath

	if c.TestHandlerGenerated == "" {
		return nil
	}
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

	handlerPackage, handlerPkgPath, handlerErr := getPackageNameAndPath(c.TestHandlerGenerated, "")
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
	return nil
}

func (c *Config) validatePackageBinding(binding *PackageBinding) error {
	if strings.HasSuffix(binding.Package, ".go") {
		// total heuristic -- but this is an easy mistake to make and
		// results in rather bizarre behavior from go/packages.
		return errorf(
			nil,
			"package %v looks like a file, but should be a package-name",
			binding.Package,
		)
	}

	if binding.Package == c.pkgPath {
		fmt.Fprintln(c.Warnings, errorf(nil, "warning: package_bindings set to the same package as your generated "+
			"code ('%v'); this may cause nondeterministic output due to circularity", c.pkgPath))
	}

	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedDeps | packages.NeedTypes,
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
			if !token.IsExported(typ) {
				continue
			}
			_, exist := c.Bindings[typ]
			if exist {
				continue
			}
			c.Bindings[typ] = &TypeBinding{
				Type: fmt.Sprintf("%s.%s", p.Path(), typ),
			}
		}
	}
	return nil
}

func (c *Config) validateOutputPaths() error {
	var outputs []inspectedPath
	for _, output := range []struct {
		name string
		path string
	}{
		{
			name: "generated",
			path: c.Generated,
		},
		{
			name: "export_operations",
			path: c.ExportOperations,
		},
		{
			name: "test_handler.generated",
			path: c.TestHandlerGenerated,
		},
	} {
		if output.path == "" {
			continue
		}
		inspected, err := inspectPath(output.name, output.path)
		if err != nil {
			return errorf(
				nil,
				"resolving %s output path %q: %v",
				output.name,
				output.path,
				err,
			)
		}
		for _, existing := range outputs {
			if pathsAlias(existing, inspected) {
				return errorf(
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
	if c.ConfigFile != "" {
		err := validateProtectedPath(outputs, "config", c.ConfigFile)
		if err != nil {
			return err
		}
	}
	if c.Schema != "" {
		err := validateProtectedPath(outputs, "schema input", c.Schema)
		if err != nil {
			return err
		}
	}
	for _, filename := range c.Operations {
		err := validateProtectedPath(outputs, "operations input", filename)
		if err != nil {
			return err
		}
	}
	c.outputs = outputs
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

func (c *Config) resolveOperationFiles() ([]string, error) {
	var schemaInspected inspectedPath
	if c.Schema != "" {
		var err error
		schemaInspected, err = inspectPath("schema", c.Schema)
		if err != nil {
			return nil, errorf(nil, "resolving schema input path %q: %v", c.Schema, err)
		}
	}
	if len(c.Operations) == 0 {
		return nil, nil
	}
	filenames, err := expandFilenames(c.Operations)
	if err != nil {
		return nil, err
	}
	operationFiles := make([]string, 0, len(filenames))
	for _, filename := range filenames {
		opInspected, inspectErr := inspectPath("operations", filename)
		if inspectErr != nil {
			return nil, errorf(nil, "resolving operations input path %q: %v", filename, inspectErr)
		}
		for _, output := range c.outputs {
			if pathsAlias(output, opInspected) {
				return nil, errorf(
					nil,
					"%s output path must be different from operations input path: %q",
					output.name,
					output.path,
				)
			}
		}
		if c.Schema != "" && pathsAlias(schemaInspected, opInspected) {
			continue
		}
		operationFiles = append(operationFiles, filename)
	}
	return operationFiles, nil
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
	resolved, existingAncestor, err := resolveOutputPath(absolute)
	if err != nil {
		return "", err
	}
	resolved = filepath.Clean(resolved)
	if filesystemCaseInsensitive(existingAncestor) {
		resolved = strings.ToLower(resolved)
	}
	return resolved, nil
}

func resolveOutputPath(absolute string) (string, string, error) {
	current := absolute
	remainder := ""
	symlinkHops := 0
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, remainder), resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", "", err
		}
		info, statErr := os.Lstat(current)
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", "", statErr
		}
		// EvalSymlinks cannot resolve a dangling link, but its target may
		// still alias another not-yet-created output.
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			symlinkHops++
			if symlinkHops > 255 {
				return "", "", fmt.Errorf("too many symlinks resolving %q", absolute)
			}
			target, readErr := os.Readlink(current)
			if readErr != nil {
				return "", "", readErr
			}
			current = filepath.Join(pathJoin(filepath.Dir(current), target), remainder)
			remainder = ""
			continue
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", err
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

func filesystemCaseInsensitive(existingPath string) bool {
	for current := existingPath; current != filepath.Dir(current); current = filepath.Dir(current) {
		base := filepath.Base(current)
		alternateBase := toggledCase(base)
		if alternateBase == base {
			continue
		}
		info, err := os.Stat(current)
		alternateInfo, alternateErr := os.Stat(filepath.Join(filepath.Dir(current), alternateBase))
		if err == nil && alternateErr == nil && os.SameFile(info, alternateInfo) {
			return true
		}
	}
	return false
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
