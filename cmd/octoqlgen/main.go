// octoqlgen generates type-safe Go GraphQL clients.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/willabides/octoql/cmd/octoqlgen/internal/cli"
	"golang.org/x/mod/module"
)

type buildMetadata struct {
	version  string
	revision string
}

func run(args []string, stdout, stderr io.Writer) error {
	metadata := currentBuildMetadata()
	return cli.Run(args, metadata.version, &cli.Dependencies{
		Context:              context.Background(),
		Stdout:               stdout,
		Stderr:               stderr,
		ConfigSchemaRevision: metadata.revision,
	})
}

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func currentBuildMetadata() buildMetadata {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return buildMetadata{version: "dev"}
	}
	return metadataFromBuildInfo(info)
}

func metadataFromBuildInfo(info *debug.BuildInfo) buildMetadata {
	metadata := buildMetadata{version: "dev"}
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			metadata.revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if modified {
		return buildMetadata{version: "dev"}
	}

	version := info.Main.Version
	if module.IsPseudoVersion(version) {
		if metadata.revision == "" {
			revision, err := module.PseudoVersionRev(version)
			if err == nil {
				metadata.revision = revision
			}
		}
		return metadata
	}
	if version == "" || version == "(devel)" {
		return metadata
	}
	metadata.version = version
	return metadata
}
