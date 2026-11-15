package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
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
	stderr       io.Writer

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
	generatorConfig.Warnings = cmd.stderr
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
	var bindings map[string]string
	if source.Bindings != nil {
		bindings = maps.Clone(*source.Bindings)
	}

	return &generate.Config{
		Schema:               source.SchemaPath(),
		Operations:           source.OperationPaths(),
		Generated:            source.GeneratedPath(),
		TestHandlerGenerated: source.TestHandlerGeneratedPath(),
		Package:              stringValue(source.Package),
		Bindings:             bindings,
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
