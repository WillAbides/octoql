// configdocgen renders the octoqlgen configuration reference from the JSON
// schema that also generates the configuration model.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	schemaPath := flag.String("schema", "octoqlgen.schema.yaml", "path to the JSON schema")
	outputPath := flag.String("output", "docs/configuration.md", "path to the generated Markdown reference")
	flag.Parse()

	err := run(*schemaPath, *outputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configdocgen:", err)
		os.Exit(1)
	}
}

func run(schemaPath, outputPath string) error {
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}

	rendered, err := render(schema)
	if err != nil {
		return err
	}

	return os.WriteFile(outputPath, rendered, 0o644)
}
