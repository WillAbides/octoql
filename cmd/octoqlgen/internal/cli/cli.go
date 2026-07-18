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

	"github.com/alecthomas/kong"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/config"
	"github.com/willabides/octoql/cmd/octoqlgen/internal/schema"
	"github.com/willabides/octoql/generate"
	yamlv3 "gopkg.in/yaml.v3"
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

type RemoteResolver interface {
	Resolve(context.Context, config.Source) (schema.RemoteResult, error)
}

type InitCommand struct {
	stdout io.Writer

	ConfigPath string `name:"config" type:"path" default:"octoql.yaml" placeholder:"PATH" help:"Path for the new octoqlgen configuration."`
}

func (cmd *InitCommand) Run() error {
	_, err := os.Lstat(cmd.ConfigPath)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("checking config path %q: %w", cmd.ConfigPath, err)
		}
		return fmt.Errorf("refusing to overwrite existing config %q", cmd.ConfigPath)
	}
	model := config.Config{
		Schema: config.Schema{
			Path: ".octoql/schema.graphql",
		},
		Operations: []string{"graphql/**/*.graphql"},
		Generated:  "internal/githubapi/generated.go",
	}
	configBytes, err := minimalConfigBytes(&model)
	if err != nil {
		return fmt.Errorf("encoding generated config: %w", err)
	}
	_, err = config.Parse(configBytes)
	if err != nil {
		return fmt.Errorf("validating generated config: %w", err)
	}
	configDir := filepath.Dir(cmd.ConfigPath)
	gitignorePath := filepath.Join(configDir, ".octoql", ".gitignore")
	err = os.MkdirAll(filepath.Dir(gitignorePath), 0o755)
	if err != nil {
		return fmt.Errorf("creating schema directory: %w", err)
	}
	gitignore, err := os.OpenFile(gitignorePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		_, err = gitignore.WriteString("*\n!.gitignore\n")
		closeErr := gitignore.Close()
		err = errors.Join(err, closeErr)
	}
	if err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("creating schema gitignore: %w", err)
	}
	err = createFileNoReplace(cmd.ConfigPath, configBytes)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.stdout, "created %s and %s\n", cmd.ConfigPath, gitignorePath)
	return err
}

func minimalConfigBytes(model *config.Config) ([]byte, error) {
	document := yamlv3.Node{
		Kind: yamlv3.DocumentNode,
		Content: []*yamlv3.Node{{
			Kind: yamlv3.MappingNode,
			Content: []*yamlv3.Node{
				{Kind: yamlv3.ScalarNode, Value: "schema"},
				{
					Kind: yamlv3.MappingNode,
					Content: []*yamlv3.Node{
						{Kind: yamlv3.ScalarNode, Value: "path"},
						{Kind: yamlv3.ScalarNode, Value: model.Schema.Path},
					},
				},
				{Kind: yamlv3.ScalarNode, Value: "operations"},
				{
					Kind: yamlv3.SequenceNode,
					Content: []*yamlv3.Node{
						{Kind: yamlv3.ScalarNode, Value: model.Operations[0]},
					},
				},
				{Kind: yamlv3.ScalarNode, Value: "generated"},
				{Kind: yamlv3.ScalarNode, Value: model.Generated},
			},
		}},
	}
	var buffer bytes.Buffer
	encoder := yamlv3.NewEncoder(&buffer)
	encoder.SetIndent(2)
	err := encoder.Encode(&document)
	closeErr := encoder.Close()
	err = errors.Join(err, closeErr)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func createFileNoReplace(destination string, data []byte) (err error) {
	directory := filepath.Dir(destination)
	err = os.MkdirAll(directory, 0o755)
	if err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary config file: %w", err)
	}
	defer func() {
		err = errors.Join(err, os.Remove(temp.Name()))
	}()
	_, err = temp.Write(data)
	if err != nil {
		return fmt.Errorf("writing temporary config file: %w", err)
	}
	err = temp.Chmod(0o644)
	if err != nil {
		return fmt.Errorf("setting config permissions: %w", err)
	}
	err = temp.Sync()
	if err != nil {
		return fmt.Errorf("syncing temporary config file: %w", err)
	}
	err = temp.Close()
	if err != nil {
		return fmt.Errorf("closing temporary config file: %w", err)
	}
	err = os.Link(temp.Name(), destination)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("refusing to overwrite existing config %q", destination)
	}
	if err != nil {
		return fmt.Errorf("publishing config file: %w", err)
	}
	return nil
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
	unlock, err := acquireUpdateLock(cmd.Config)
	if err != nil {
		return err
	}
	defer unlock()

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

func acquireUpdateLock(configPath string) (func(), error) {
	lockPath := configPath + ".schema-update.lock"
	err := os.Mkdir(lockPath, 0o700)
	if errors.Is(err, os.ErrExist) {
		return nil, errors.New("another schema update is already in progress")
	}
	if err != nil {
		return nil, fmt.Errorf("acquiring schema update lock: %w", err)
	}
	return func() {
		_ = os.Remove(lockPath)
	}, nil
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
			stdout: dependencies.Stdout,
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
	mode := os.FileMode(0o644)
	info, statErr := os.Stat(destination)
	if statErr == nil {
		mode = info.Mode()
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("reading output permissions: %w", statErr)
	}
	err = temp.Chmod(mode)
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
