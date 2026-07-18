// Package generate provides programmatic access to octoqlgen's functionality,
// and documentation of its configuration options.  For general usage
// documentation, see the project [GitHub].
//
// [GitHub]: https://github.com/willabides/octoql
package generate

import (
	"fmt"
	"os"
	"path/filepath"
)

func warn(err error) {
	fmt.Println(err)
}

func readConfigGenerateAndWrite(configFilename string) error {
	var config *Config
	var err error
	if configFilename != "" {
		config, err = ReadAndValidateConfig(configFilename)
		if err != nil {
			return err
		}
	} else {
		config, err = ReadAndValidateConfigFromDefaultLocations()
		if err != nil {
			return err
		}
	}

	generated, err := Generate(config)
	if err != nil {
		return err
	}

	for filename, content := range generated {
		err = os.MkdirAll(filepath.Dir(filename), 0o755)
		if err != nil {
			return errorf(nil,
				"could not create parent directory for generated file %v: %v",
				filename, err)
		}

		err = os.WriteFile(filename, content, 0o644)
		if err != nil {
			return errorf(nil, "could not write generated file %v: %v",
				filename, err)
		}
	}
	return nil
}

// Run generates Go GraphQL client code using configFilename. An empty filename
// searches for genqlient.yaml in the current and parent directories.
func Run(configFilename string) error {
	return readConfigGenerateAndWrite(configFilename)
}
