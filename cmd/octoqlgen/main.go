// octoqlgen generates type-safe Go GraphQL clients.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/willabides/octoql/cmd/octoqlgen/internal/cli"
)

var buildVersion string

func run(args []string, stdout, stderr io.Writer) error {
	return cli.Run(args, version(), &cli.Dependencies{
		Context: context.Background(),
		Stdout:  stdout,
		Stderr:  stderr,
	})
}

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}

	version := info.Main.Version
	if version == "" || version == "(devel)" || strings.HasPrefix(version, "v0.0.0-") {
		return "dev"
	}
	return version
}
