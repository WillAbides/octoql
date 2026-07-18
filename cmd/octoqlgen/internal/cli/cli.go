// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

// Package cli defines the octoqlgen command-line interface.
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema"
	"github.com/willabides/octoql/generate"
)

type commandTree struct {
	Generate GenerateCommand  `cmd:"" help:"Generate GraphQL client code."`
	Init     InitCommand      `cmd:"" help:"Create an octoqlgen configuration and materialized schema."`
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
	Materialize SchemaMaterializeCommand `cmd:"" default:"1" help:"Materialize or verify a pinned GraphQL schema."`
	Update      SchemaUpdateCommand      `cmd:"" help:"Update a configured remote schema pin."`
}

type SchemaMaterializeCommand struct {
	context      context.Context
	loadConfig   func(string) (*config.Config, error)
	materializer Materializer
	outputWriter OutputWriter
	stdout       io.Writer

	Config        string `name:"config" type:"path" placeholder:"PATH" help:"Path to an octoqlgen configuration file. Defaults to octoql.yaml."`
	Output        string `short:"o" name:"output" type:"path" placeholder:"PATH" help:"Write the exact schema bytes to a file instead of stdout."`
	GitHubVersion string `name:"github-version" placeholder:"VERSION" help:"Fetch a pinned github/docs schema version (fpt, ghec, or ghes-X.Y)."`
	SourceURL     string `name:"source-url" placeholder:"URL" help:"Fetch a schema from an immutable URL."`
	Revision      string `name:"revision" placeholder:"SHA" help:"Full github/docs commit revision for --github-version."`
	SHA256        string `name:"sha256" placeholder:"HEX" help:"Expected SHA-256 for a direct remote source."`
}

type sourceFlags struct {
	GitHubVersion    string `name:"github-version" placeholder:"VERSION" help:"Use github/docs (fpt, ghec, or ghes-X.Y)."`
	GitHubRepository string `name:"github-repository" placeholder:"OWNER/REPO" help:"Use a schema file from a GitHub repository."`
	GitHubPath       string `name:"github-path" placeholder:"PATH" help:"Path to the schema in --github-repository."`
	GitHubHost       string `name:"github-host" placeholder:"HOST" help:"GitHub host for --github-repository. Defaults to github.com."`
	SourceURL        string `name:"source-url" placeholder:"URL" help:"Use an arbitrary schema URL. Immutable URLs are required for reproducibility."`
}

func (flags *sourceFlags) source() (config.Source, error) {
	hasDocs := flags.GitHubVersion != ""
	hasRepository := flags.GitHubRepository != "" || flags.GitHubPath != "" || flags.GitHubHost != ""
	hasURL := flags.SourceURL != ""
	count := 0
	for _, present := range []bool{hasDocs, hasRepository, hasURL} {
		if present {
			count++
		}
	}
	if count != 1 {
		return config.Source{}, errors.New("exactly one schema source must be selected")
	}
	if hasDocs {
		if flags.GitHubPath != "" || flags.GitHubHost != "" {
			return config.Source{}, errors.New("--github-path and --github-host require --github-repository")
		}
		return config.Source{
			GithubDocs: &config.GithubDocs{Version: flags.GitHubVersion},
		}, nil
	}
	if hasURL {
		return config.Source{Url: &flags.SourceURL}, nil
	}
	if flags.GitHubRepository == "" || flags.GitHubPath == "" {
		return config.Source{}, errors.New("--github-repository and --github-path must be provided together")
	}
	repository := &config.GithubRepository{
		Path:       flags.GitHubPath,
		Repository: flags.GitHubRepository,
	}
	if flags.GitHubHost != "" {
		repository.Host = &flags.GitHubHost
	}
	return config.Source{GithubRepository: repository}, nil
}

type RemoteResolver interface {
	Resolve(context.Context, config.Source) (schema.RemoteResult, error)
}

type InitCommand struct {
	context      context.Context
	resolver     RemoteResolver
	outputWriter OutputWriter
	stdout       io.Writer
	workingDir   func() (string, error)

	ConfigPath           string `name:"config" type:"path" default:"octoql.yaml" placeholder:"PATH" help:"Path for the new octoqlgen configuration."`
	SchemaPath           string `name:"schema-path" type:"path" placeholder:"PATH" help:"Materialized schema path. Defaults to .octoql/schema.graphql."`
	Operations           string `name:"operations" type:"path" placeholder:"GLOB" help:"Operations glob. Defaults to graphql/**/*.graphql."`
	Generated            string `name:"generated" type:"path" placeholder:"PATH" help:"Generated Go file. Defaults to internal/githubapi/generated.go."`
	TestHandlerGenerated string `name:"test-handler-generated" type:"path" placeholder:"PATH" help:"Optional generated test handler Go file."`
	sourceFlags
}

func (cmd *InitCommand) Run() error {
	info, err := os.Lstat(cmd.ConfigPath)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("checking config path %q: %w", cmd.ConfigPath, err)
		}
		_ = info
		return fmt.Errorf("refusing to overwrite existing config %q", cmd.ConfigPath)
	}
	source, err := cmd.source()
	if err != nil {
		return err
	}
	result, err := cmd.resolver.Resolve(cmd.context, source)
	if err != nil {
		return err
	}
	source = sourceWithRevision(source, result.Revision)
	paths := cmd.defaultPaths()
	model := config.Config{
		Schema: config.Schema{
			Path:   paths.schema,
			Sha256: &result.SHA256,
			Source: &source,
		},
		Operations: []string{paths.operations},
		Generated:  paths.generated,
	}
	if cmd.TestHandlerGenerated != "" {
		model.TestHandler = &config.TestHandler{Generated: cmd.TestHandlerGenerated}
	}
	configBytes := formatConfig(&model)
	schemaDestination := filepath.Join(filepath.Dir(cmd.ConfigPath), paths.schema)
	err = cmd.outputWriter.Write(schemaDestination, result.Data)
	if err != nil {
		return fmt.Errorf("materializing schema: %w", err)
	}
	err = cmd.outputWriter.Write(cmd.ConfigPath, configBytes)
	if err != nil {
		return fmt.Errorf(
			"writing config after schema publication failed; schema was written to %q and can be recovered by rerunning init: %w",
			schemaDestination,
			err,
		)
	}
	_, err = fmt.Fprintf(
		cmd.stdout,
		"created %s with schema %s, revision %s, sha256 %s\n",
		cmd.ConfigPath,
		paths.schema,
		displayRevision(result.Revision),
		result.SHA256,
	)
	return err
}

type initPaths struct {
	schema     string
	operations string
	generated  string
}

func (cmd *InitCommand) defaultPaths() initPaths {
	paths := initPaths{
		schema:     ".octoql/schema.graphql",
		operations: "graphql/**/*.graphql",
		generated:  "internal/githubapi/generated.go",
	}
	if cmd.SchemaPath != "" {
		paths.schema = cmd.SchemaPath
	}
	if cmd.Operations != "" {
		paths.operations = cmd.Operations
	}
	if cmd.Generated != "" {
		paths.generated = cmd.Generated
	}
	return paths
}

type SchemaUpdateCommand struct {
	context      context.Context
	loadConfig   func(string) (*config.Config, error)
	resolver     RemoteResolver
	outputWriter OutputWriter
	stdout       io.Writer

	Config string `name:"config" type:"path" default:"octoql.yaml" placeholder:"PATH" help:"Path to an octoqlgen configuration file."`
}

func (cmd *SchemaUpdateCommand) Run() error {
	rawConfig, err := os.ReadFile(cmd.Config)
	if err != nil {
		return fmt.Errorf("reading config file %q: %w", cmd.Config, err)
	}
	loaded, err := cmd.loadConfig(cmd.Config)
	if err != nil {
		return err
	}
	source := loaded.Schema.SourceValue()
	if !hasRemoteSource(source) {
		return errors.New("schema update requires a configured remote schema source")
	}
	result, err := cmd.resolver.Resolve(cmd.context, source)
	if err != nil {
		return err
	}
	currentData, err := os.ReadFile(loaded.SchemaPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading configured schema: %w", err)
	}
	currentHash := fmt.Sprintf("%x", sha256.Sum256(currentData))
	if err == nil && currentHash == result.SHA256 && loaded.Schema.SHA256Value() == result.SHA256 {
		_, outputErr := fmt.Fprintln(cmd.stdout, "schema is unchanged")
		return outputErr
	}
	updatedConfig, err := config.UpdatePin(rawConfig, result.SHA256, result.Revision)
	if err != nil {
		return err
	}
	err = cmd.outputWriter.Write(loaded.SchemaPath(), result.Data)
	if err != nil {
		return fmt.Errorf("publishing updated schema: %w", err)
	}
	currentConfig, err := os.ReadFile(cmd.Config)
	if err != nil {
		return fmt.Errorf("checking config before update: %w", err)
	}
	if !bytes.Equal(currentConfig, rawConfig) {
		return errors.New("config changed while updating schema; schema was published but config was not changed")
	}
	err = cmd.outputWriter.Write(cmd.Config, updatedConfig)
	if err != nil {
		return fmt.Errorf("config update failed after schema publication; rerun schema update to recover: %w", err)
	}
	_, err = fmt.Fprintf(
		cmd.stdout,
		"updated schema revision %s -> %s, sha256 %s -> %s\n",
		displayRevision(sourceRevision(source)),
		displayRevision(result.Revision),
		loaded.Schema.SHA256Value(),
		result.SHA256,
	)
	return err
}

func sourceWithRevision(source config.Source, revision string) config.Source {
	if source.GithubDocs != nil {
		source.GithubDocs.Revision = revision
	}
	if source.GithubRepository != nil {
		source.GithubRepository.Revision = revision
	}
	return source
}

func sourceRevision(source config.Source) string {
	if source.GithubDocs != nil {
		return source.GithubDocs.Revision
	}
	if source.GithubRepository != nil {
		return source.GithubRepository.Revision
	}
	return ""
}

func hasRemoteSource(source config.Source) bool {
	return source.GithubDocs != nil || source.GithubRepository != nil || source.Url != nil
}

func displayRevision(revision string) string {
	if revision == "" {
		return "n/a"
	}
	return revision
}

func formatConfig(model *config.Config) []byte {
	var builder strings.Builder
	builder.WriteString("schema:\n")
	builder.WriteString("  path: ")
	builder.WriteString(model.Schema.Path)
	builder.WriteString("\n  sha256: ")
	builder.WriteString(*model.Schema.Sha256)
	builder.WriteString("\n  source:\n")
	if model.Schema.Source.GithubDocs != nil {
		builder.WriteString("    github_docs:\n      version: ")
		builder.WriteString(model.Schema.Source.GithubDocs.Version)
		builder.WriteString("\n      revision: ")
		builder.WriteString(model.Schema.Source.GithubDocs.Revision)
		builder.WriteString("\n")
	}
	if model.Schema.Source.GithubRepository != nil {
		repository := model.Schema.Source.GithubRepository
		builder.WriteString("    github_repository:\n      repository: ")
		builder.WriteString(repository.Repository)
		builder.WriteString("\n      path: ")
		builder.WriteString(repository.Path)
		if repository.Host != nil {
			builder.WriteString("\n      host: ")
			builder.WriteString(*repository.Host)
		}
		builder.WriteString("\n      revision: ")
		builder.WriteString(repository.Revision)
		builder.WriteString("\n")
	}
	if model.Schema.Source.Url != nil {
		builder.WriteString("    url: ")
		builder.WriteString(*model.Schema.Source.Url)
		builder.WriteString("\n")
	}
	builder.WriteString("operations:\n  - ")
	builder.WriteString(model.Operations[0])
	builder.WriteString("\ngenerated: ")
	builder.WriteString(model.Generated)
	builder.WriteString("\n")
	if model.TestHandler != nil {
		builder.WriteString("test_handler:\n  generated: ")
		builder.WriteString(model.TestHandler.Generated)
		builder.WriteString("\n")
	}
	return []byte(builder.String())
}

func (cmd *SchemaMaterializeCommand) Run() error {
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

func (cmd *SchemaMaterializeCommand) request() (schema.Request, error) {
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
	Context        context.Context
	Stdout         io.Writer
	Stderr         io.Writer
	Generate       func(string) error
	LoadConfig     func(string) (*config.Config, error)
	Materializer   Materializer
	RemoteResolver RemoteResolver
	OutputWriter   OutputWriter
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
	parsed, err := parser.Parse(normalizeArgs(args))
	if err != nil {
		return err
	}
	return parsed.Run()
}

func normalizeArgs(args []string) []string {
	if len(args) == 0 || args[0] != "schema" {
		return args
	}
	if len(args) == 1 {
		return []string{"schema", "materialize"}
	}
	if args[1] == "update" || args[1] == "materialize" || args[1] == "--help" {
		return args
	}
	normalized := make([]string, 0, len(args)+1)
	normalized = append(normalized, "schema", "materialize")
	normalized = append(normalized, args[1:]...)
	return normalized
}

func newCommandTree(dependencies *Dependencies) *commandTree {
	return &commandTree{
		Generate: GenerateCommand{
			run: dependencies.Generate,
		},
		Init: InitCommand{
			context:      dependencies.Context,
			resolver:     dependencies.RemoteResolver,
			outputWriter: dependencies.OutputWriter,
			stdout:       dependencies.Stdout,
			workingDir:   os.Getwd,
		},
		Schema: SchemaCommand{
			Materialize: SchemaMaterializeCommand{
				context:      dependencies.Context,
				loadConfig:   dependencies.LoadConfig,
				materializer: dependencies.Materializer,
				outputWriter: dependencies.OutputWriter,
				stdout:       dependencies.Stdout,
			},
			Update: SchemaUpdateCommand{
				context:      dependencies.Context,
				loadConfig:   dependencies.LoadConfig,
				resolver:     dependencies.RemoteResolver,
				outputWriter: dependencies.OutputWriter,
				stdout:       dependencies.Stdout,
			},
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
	if d.RemoteResolver == nil {
		resolver, ok := d.Materializer.(RemoteResolver)
		if !ok {
			d.RemoteResolver = schema.NewMaterializer()
		} else {
			d.RemoteResolver = resolver
		}
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
