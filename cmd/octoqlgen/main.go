// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

// octoqlgen generates type-safe Go GraphQL clients.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/willabides/octoql/generate"
)

type cli struct {
	Generate generateCommand  `cmd:"" help:"Generate GraphQL client code."`
	Version  kong.VersionFlag `name:"version" help:"Show version information."`
}

type generateCommand struct {
	ConfigFilename string `arg:"" optional:"" placeholder:"CONFIG" help:"Path to a genqlient configuration file. Defaults to genqlient.yaml in the current or a parent directory."`
}

func (cmd generateCommand) Run() error {
	return generate.Run(cmd.ConfigFilename)
}

func newParser(cli *cli, options ...kong.Option) (*kong.Kong, error) {
	defaultOptions := []kong.Option{
		kong.Name("octoqlgen"),
		kong.Description("Generate GraphQL client code for a given schema and queries."),
		kong.Vars{"version": version()},
	}
	defaultOptions = append(defaultOptions, options...)
	return kong.New(cli, defaultOptions...)
}

func run(args []string, stdout, stderr io.Writer) error {
	command := cli{}
	parser, err := newParser(&command, kong.Writers(stdout, stderr))
	if err != nil {
		return err
	}
	context, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return context.Run()
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
