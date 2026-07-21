package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema"
	"github.com/willabides/octoql/internal/generate"
)

type generateCommand struct {
	context      context.Context
	loadConfig   func(string) (*config.Config, error)
	materializer materializer
	generate     func(*generate.Config) (map[string][]byte, error)
	outputWriter outputWriter

	Config string `kong:"name='config',type='path',default='octoqlgen.yaml',placeholder='PATH',help='Path to an octoqlgen configuration file.'"`
}

func (cmd *generateCommand) Run() error {
	loaded, err := cmd.loadConfig(cmd.Config)
	if err != nil {
		return err
	}

	configPath := cmd.Config
	if configPath == "" {
		configPath = config.DefaultFilename
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}

	generatorConfig := generateConfig(loaded)
	generatorConfig.ConfigFile = configPath
	err = generatorConfig.ValidateAndFillDefaults(filepath.Dir(configPath))
	if err != nil {
		return fmt.Errorf("validating generation config: %w", err)
	}

	_, err = cmd.materializer.Materialize(cmd.context, &schema.Request{
		Path:   loaded.SchemaPath(),
		SHA256: loaded.Schema.SHA256Value(),
		Source: loaded.Schema.SourceValue(),
	})
	if err != nil {
		return fmt.Errorf(
			"fetching configured schema: %w; edit schema.source or run octoqlgen schema fetch",
			err,
		)
	}

	outputs, err := cmd.generate(generatorConfig)
	if err != nil {
		return err
	}

	filenames := make([]string, 0, len(outputs))
	for filename := range outputs {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	for _, filename := range filenames {
		err = cmd.outputWriter.Write(filename, outputs[filename])
		if err != nil {
			return fmt.Errorf("writing generated output %q: %w", filename, err)
		}
	}
	return nil
}

func generateConfig(source *config.Config) *generate.Config {
	bindings := map[string]*generate.TypeBinding{}
	if source.Bindings != nil {
		for name, binding := range *source.Bindings {
			if binding == nil {
				bindings[name] = &generate.TypeBinding{}
				continue
			}
			bindings[name] = &generate.TypeBinding{
				Type:              stringValue(binding.Type),
				ExpectExactFields: stringValue(binding.ExpectExactFields),
				Marshaler:         stringValue(binding.Marshaler),
				Unmarshaler:       stringValue(binding.Unmarshaler),
			}
		}
	}

	packageBindings := make([]*generate.PackageBinding, 0, len(source.PackageBindings))
	for _, binding := range source.PackageBindings {
		packageBindings = append(packageBindings, &generate.PackageBinding{
			Package: binding.Package,
		})
	}

	var casing generate.Casing
	if source.Casing != nil {
		casing.Default = generate.CasingAlgorithm(stringValue(source.Casing.Default))
		casing.AllEnums = generate.CasingAlgorithm(stringValue(source.Casing.AllEnums))
		if source.Casing.Enums != nil {
			casing.Enums = make(
				map[string]generate.CasingAlgorithm,
				len(*source.Casing.Enums),
			)
			for name, algorithm := range *source.Casing.Enums {
				casing.Enums[name] = generate.CasingAlgorithm(algorithm)
			}
		}
	}

	return &generate.Config{
		Schema:                          generate.StringList{source.SchemaPath()},
		Operations:                      generate.StringList(source.OperationPaths()),
		Generated:                       source.GeneratedPath(),
		TestHandlerGenerated:            source.TestHandlerGeneratedPath(),
		TestHandlerTypes:                generate.TestHandlerTypeStrategy(source.TestHandlerTypesValue()),
		Package:                         stringValue(source.Package),
		ExportOperations:                source.ExportOperationsPath(),
		ContextType:                     stringValue(source.ContextType),
		ClientGetter:                    stringValue(source.ClientGetter),
		Bindings:                        bindings,
		PackageBindings:                 packageBindings,
		Casing:                          casing,
		StructReferences:                boolValue(source.UseStructReferences),
		OmitUnreferencedImplementations: source.OmitUnreferencedImplementations,
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolValue(value *bool) bool {
	return value != nil && *value
}
