// octoqlgen generates type-safe Go GraphQL clients.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/willabides/octoql/cmd/octoqlgen/internal/cli"
)

func run(args []string, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cli.Run(args, version(), &cli.Dependencies{
		Context: ctx,
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
