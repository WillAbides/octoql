// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

// Package cli defines the octoqlgen command-line interface.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema"
	"github.com/willabides/octoql/generate"
)

type commandTree struct {
	Generate GenerateCommand  `cmd:"" help:"Generate GraphQL client code."`
	Schema   SchemaCommand    `cmd:"" help:"Materialize or verify a pinned GraphQL schema."`
	Version  kong.VersionFlag `name:"version" help:"Show version information."`
}

type GenerateCommand struct {
	run func(string) error

	ConfigFilename string `arg:"" optional:"" placeholder:"CONFIG" help:"Path to a genqlient configuration file. Defaults to genqlient.yaml in the current or a parent directory."`
}

func (cmd GenerateCommand) Run() error {
	return cmd.run(cmd.ConfigFilename)
}

type SchemaCommand struct {
	context      context.Context
	loadConfig   func(string) (*config.Config, error)
	materializer Materializer
	outputWriter OutputWriter
	stdout       io.Writer

	Config        string `name:"config" type:"path" placeholder:"PATH" help:"Path to an octoql configuration file. Defaults to octoql.yaml."`
	Output        string `short:"o" name:"output" type:"path" placeholder:"PATH" help:"Write the exact schema bytes to a file instead of stdout."`
	GitHubVersion string `name:"github-version" placeholder:"VERSION" help:"Fetch a pinned github/docs schema version (fpt, ghec, or ghes-X.Y)."`
	SourceURL     string `name:"source-url" placeholder:"URL" help:"Fetch a schema from an immutable URL."`
	Revision      string `name:"revision" placeholder:"SHA" help:"Full github/docs commit revision for --github-version."`
	SHA256        string `name:"sha256" placeholder:"HEX" help:"Expected SHA-256 for a direct remote source."`
}

func (cmd *SchemaCommand) Run() error {
	request, err := cmd.request()
	if err != nil {
		return err
	}

	data, err := cmd.materializer.Materialize(cmd.context, request)
	if err != nil {
		return err
	}
	if cmd.Output != "" {
		err = cmd.outputWriter.Write(cmd.Output, data)
		if err != nil {
			return fmt.Errorf("writing schema output %q: %w", cmd.Output, err)
		}
		return nil
	}

	_, err = cmd.stdout.Write(data)
	if err != nil {
		return fmt.Errorf("writing schema to stdout: %w", err)
	}
	return nil
}

func (cmd *SchemaCommand) request() (schema.Request, error) {
	hasGitHubVersion := cmd.GitHubVersion != ""
	hasSourceURL := cmd.SourceURL != ""
	if hasGitHubVersion && hasSourceURL {
		return schema.Request{}, errors.New("--github-version and --source-url are mutually exclusive")
	}

	if !hasGitHubVersion && !hasSourceURL {
		if cmd.Revision != "" || cmd.SHA256 != "" {
			return schema.Request{}, errors.New("--revision and --sha256 require a direct remote source")
		}
		loaded, err := cmd.loadConfig(cmd.Config)
		if err != nil {
			return schema.Request{}, err
		}
		return schema.Request{
			Path:   loaded.SchemaPath(),
			SHA256: loaded.Schema.SHA256Value(),
			Source: loaded.Schema.SourceValue(),
		}, nil
	}

	if cmd.SHA256 == "" {
		return schema.Request{}, errors.New("--sha256 is required with a direct remote source")
	}
	if cmd.Config != "" {
		return schema.Request{}, errors.New("--config cannot be combined with a direct remote source")
	}
	if hasGitHubVersion {
		if cmd.Revision == "" {
			return schema.Request{}, errors.New("--revision is required with --github-version")
		}
		return schema.Request{
			SHA256: cmd.SHA256,
			Source: config.Source{
				GithubDocs: &config.GithubDocs{
					Version:  cmd.GitHubVersion,
					Revision: cmd.Revision,
				},
			},
		}, nil
	}
	if cmd.Revision != "" {
		return schema.Request{}, errors.New("--revision is only valid with --github-version")
	}
	return schema.Request{
		SHA256: cmd.SHA256,
		Source: config.Source{Url: new(cmd.SourceURL)},
	}, nil
}

type Materializer interface {
	Materialize(context.Context, schema.Request) ([]byte, error)
}

type OutputWriter interface {
	Write(string, []byte) error
}

type Dependencies struct {
	Context      context.Context
	Stdout       io.Writer
	Stderr       io.Writer
	Generate     func(string) error
	LoadConfig   func(string) (*config.Config, error)
	Materializer Materializer
	OutputWriter OutputWriter
}

func Run(args []string, version string, dependencies *Dependencies) error {
	if dependencies == nil {
		dependencies = &Dependencies{}
	}
	dependencies.setDefaults()
	command := newCommandTree(dependencies)
	parser, err := newParser(
		command,
		version,
		kong.Writers(dependencies.Stdout, dependencies.Stderr),
	)
	if err != nil {
		return err
	}
	parsed, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return parsed.Run()
}

func newCommandTree(dependencies *Dependencies) *commandTree {
	return &commandTree{
		Generate: GenerateCommand{
			run: dependencies.Generate,
		},
		Schema: SchemaCommand{
			context:      dependencies.Context,
			loadConfig:   dependencies.LoadConfig,
			materializer: dependencies.Materializer,
			outputWriter: dependencies.OutputWriter,
			stdout:       dependencies.Stdout,
		},
	}
}

func newParser(command *commandTree, version string, options ...kong.Option) (*kong.Kong, error) {
	defaultOptions := []kong.Option{
		kong.Name("octoqlgen"),
		kong.Description("Generate GraphQL client code for a given schema and queries."),
		kong.Vars{"version": version},
	}
	defaultOptions = append(defaultOptions, options...)
	return kong.New(command, defaultOptions...)
}

func (d *Dependencies) setDefaults() {
	if d.Context == nil {
		d.Context = context.Background()
	}
	if d.Stdout == nil {
		d.Stdout = io.Discard
	}
	if d.Stderr == nil {
		d.Stderr = io.Discard
	}
	if d.Generate == nil {
		d.Generate = generate.Run
	}
	if d.LoadConfig == nil {
		d.LoadConfig = config.Load
	}
	if d.Materializer == nil {
		d.Materializer = schema.NewMaterializer()
	}
	if d.OutputWriter == nil {
		d.OutputWriter = atomicOutputWriter{}
	}
}

type atomicOutputWriter struct{}

func (atomicOutputWriter) Write(destination string, data []byte) (err error) {
	directory := filepath.Dir(destination)
	err = os.MkdirAll(directory, 0o755)
	if err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary output file: %w", err)
	}
	isClosed := false
	shouldRemove := true
	defer func() {
		if !isClosed {
			err = errors.Join(err, temp.Close())
		}
		if shouldRemove {
			err = errors.Join(err, os.Remove(temp.Name()))
		}
	}()

	_, err = io.Copy(temp, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("writing temporary output file: %w", err)
	}
	err = temp.Chmod(0o644)
	if err != nil {
		return fmt.Errorf("setting temporary output permissions: %w", err)
	}
	err = temp.Sync()
	if err != nil {
		return fmt.Errorf("syncing temporary output file: %w", err)
	}
	err = temp.Close()
	isClosed = true
	if err != nil {
		return fmt.Errorf("closing temporary output file: %w", err)
	}
	err = os.Rename(temp.Name(), destination)
	if err != nil {
		return fmt.Errorf("publishing output file: %w", err)
	}
	shouldRemove = false
	return nil
}
