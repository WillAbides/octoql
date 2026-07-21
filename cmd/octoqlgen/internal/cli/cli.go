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
	"github.com/willabides/octoql/internal/generate"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	yamlv3 "gopkg.in/yaml.v3"
)

const (
	mainConfigSchemaURL  = "https://raw.githubusercontent.com/WillAbides/octoql/main/octoqlgen.schema.yaml"
	releaseSchemaBaseURL = "https://github.com/WillAbides/octoql/releases/download/"
)

type commandTree struct {
	Generate generateCommand  `kong:"cmd,help='Generate GraphQL client code.'"`
	Init     initCommand      `kong:"cmd,help='Create an octoqlgen configuration and fetch its schema.'"`
	Schema   schemaCommand    `kong:"cmd,help='Fetch or verify a pinned GraphQL schema.'"`
	Version  kong.VersionFlag `kong:"name='version',help='Show version information.'"`
}

type schemaCommand struct {
	Fetch  schemaFetchCommand  `kong:"cmd,default='1',help='Fetch or verify a pinned GraphQL schema.'"`
	Update schemaUpdateCommand `kong:"cmd,help='Fetch the latest configured GitHub schema and update its revision and checksum.'"`
}

type schemaFetchCommand struct {
	context      context.Context
	loadConfig   func(string) (*config.Config, error)
	materializer materializer
	outputWriter outputWriter
	stdout       io.Writer

	Config string `kong:"name='config',type='path',placeholder='PATH',help='Path to an octoqlgen configuration file. Defaults to octoqlgen.yaml.'"`
	Output string `kong:"short='o',name='output',type='path',placeholder='PATH',help='Write the exact schema bytes to a file instead of stdout.'"`
}

type remoteResolver interface {
	Resolve(context.Context, config.Source) (schema.RemoteResult, error)
}

type initCommand struct {
	context         context.Context
	resolver        remoteResolver
	stdout          io.Writer
	configSchemaURL string

	ConfigPath    string `kong:"name='config',type='path',default='octoqlgen.yaml',placeholder='PATH',help='Path for the new octoqlgen configuration.'"`
	SchemaVersion string `kong:"name='schema-version',default='fpt',placeholder='VERSION',help='GitHub Docs schema version (fpt, ghec, or ghes-X.Y). Defaults to fpt.'"`
}

func githubDocsSource(version, revision string) config.Source {
	filename := "schema.docs.graphql"
	if strings.HasPrefix(version, "ghes-") {
		filename = "schema.docs-enterprise.graphql"
	}
	return config.Source{
		Repository: "github/docs",
		Path:       "src/graphql/data/" + version + "/" + filename,
		Revision:   revision,
	}
}

func (cmd *initCommand) Run() (err error) {
	_, err = os.Lstat(cmd.ConfigPath)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("checking config path %q: %w", cmd.ConfigPath, err)
		}
		return fmt.Errorf("refusing to overwrite existing config %q", cmd.ConfigPath)
	}
	source := githubDocsSource(cmd.SchemaVersion, "")
	result, err := cmd.resolver.Resolve(cmd.context, source)
	if err != nil {
		return fmt.Errorf("resolving GitHub Docs schema: %w", err)
	}
	model := config.Config{
		Schema: config.Schema{
			Path:   ".octoql/schema.graphql",
			Sha256: new(result.SHA256),
			Source: new(githubDocsSource(cmd.SchemaVersion, result.Revision)),
		},
		Operations: []string{"graphql/**/*.graphql"},
		Generated:  "internal/githubapi/generated.go",
	}
	configBytes, err := minimalConfigBytes(&model)
	if err != nil {
		return fmt.Errorf("encoding generated config: %w", err)
	}
	configBytes = config.SetSchemaDirective(configBytes, cmd.configSchemaURL)
	_, err = config.Parse(configBytes)
	if err != nil {
		return fmt.Errorf("validating generated config: %w", err)
	}
	configDir := filepath.Dir(cmd.ConfigPath)
	schemaPath := filepath.Join(configDir, model.Schema.Path)
	schemaCreated := false
	existingSchema, err := os.ReadFile(schemaPath)
	if err == nil && !bytes.Equal(existingSchema, result.Data) {
		return fmt.Errorf("refusing to overwrite existing schema %q", schemaPath)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking schema path %q: %w", schemaPath, err)
	}
	if errors.Is(err, os.ErrNotExist) {
		err = createFileNoReplace(schemaPath, result.Data)
		if err != nil {
			return err
		}
		schemaCreated = true
	}
	initializationComplete := false
	defer func() {
		if schemaCreated && !initializationComplete {
			err = errors.Join(err, os.Remove(schemaPath))
		}
	}()
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
	initializationComplete = true
	_, err = fmt.Fprintf(cmd.stdout, "created %s, %s, and %s\n", cmd.ConfigPath, schemaPath, gitignorePath)
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
						{Kind: yamlv3.ScalarNode, Value: "sha256"},
						{Kind: yamlv3.ScalarNode, Value: model.Schema.SHA256Value()},
						{Kind: yamlv3.ScalarNode, Value: "source"},
						{
							Kind: yamlv3.MappingNode,
							Content: []*yamlv3.Node{
								{Kind: yamlv3.ScalarNode, Value: "repository"},
								{Kind: yamlv3.ScalarNode, Value: model.Schema.Source.Repository},
								{Kind: yamlv3.ScalarNode, Value: "path"},
								{Kind: yamlv3.ScalarNode, Value: model.Schema.Source.Path},
								{Kind: yamlv3.ScalarNode, Value: "revision"},
								{Kind: yamlv3.ScalarNode, Value: model.Schema.Source.Revision},
							},
						},
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

type schemaUpdateCommand struct {
	context         context.Context
	loadConfig      func(string) (*config.Config, error)
	resolver        remoteResolver
	outputWriter    outputWriter
	stdout          io.Writer
	configSchemaURL string

	Config string `kong:"name='config',type='path',default='octoqlgen.yaml',placeholder='PATH',help='Path to an octoqlgen configuration file.'"`
}

func (cmd *schemaUpdateCommand) Run() error {
	userConfigPath, err := filepath.Abs(cmd.Config)
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}
	configPath, err := canonicalPath(userConfigPath)
	if err != nil {
		return err
	}
	rawConfig, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading config file %q: %w", cmd.Config, err)
	}

	loaded, err := cmd.loadConfig(userConfigPath)
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
	originalSchema, err := snapshotFile(loaded.SchemaPath())
	if err != nil {
		return fmt.Errorf("reading configured schema: %w", err)
	}
	currentHash := fmt.Sprintf("%x", sha256.Sum256(originalSchema.data))
	schemaUnchanged := originalSchema.exists && currentHash == result.SHA256
	pinsUnchanged := loaded.Schema.SHA256Value() == result.SHA256 &&
		sourceRevision(source) == result.Revision
	if schemaUnchanged && pinsUnchanged {
		updatedConfig := config.SetSchemaDirective(rawConfig, cmd.configSchemaURL)
		_, err = config.Parse(updatedConfig)
		if err != nil {
			return fmt.Errorf("validating updated config: %w", err)
		}
		if !bytes.Equal(rawConfig, updatedConfig) {
			currentConfig, readErr := os.ReadFile(configPath)
			if readErr != nil {
				return fmt.Errorf("checking config before update: %w", readErr)
			}
			if !bytes.Equal(currentConfig, rawConfig) {
				return errors.New("config changed while updating schema")
			}
			writeErr := cmd.outputWriter.Write(configPath, updatedConfig)
			if writeErr != nil {
				return fmt.Errorf("config update failed: %w", writeErr)
			}
			_, loadErr := cmd.loadConfig(userConfigPath)
			if loadErr != nil {
				return fmt.Errorf("validating published config: %w", loadErr)
			}
		}
		_, outputErr := fmt.Fprintln(cmd.stdout, "schema is unchanged")
		return outputErr
	}
	updatedConfig, err := config.UpdatePin(rawConfig, result.SHA256, result.Revision)
	if err != nil {
		return err
	}
	updatedConfig = config.SetSchemaDirective(updatedConfig, cmd.configSchemaURL)
	_, err = config.Parse(updatedConfig)
	if err != nil {
		return fmt.Errorf("validating updated config: %w", err)
	}
	finalConfig, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("checking config before schema publication: %w", err)
	}
	finalSchema, err := snapshotFile(loaded.SchemaPath())
	if err != nil {
		return fmt.Errorf("checking schema before publication: %w", err)
	}
	if !bytes.Equal(finalConfig, rawConfig) || !sameFileSnapshot(finalSchema, originalSchema) {
		return errors.New("config or schema changed while updating; no files were published")
	}
	err = cmd.outputWriter.Write(loaded.SchemaPath(), result.Data)
	if err != nil {
		return fmt.Errorf("publishing updated schema: %w", err)
	}
	currentConfig, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("checking config before update: %w", err)
	}
	if !bytes.Equal(currentConfig, rawConfig) {
		return errors.New("config changed while updating schema")
	}
	err = cmd.outputWriter.Write(configPath, updatedConfig)
	if err != nil {
		return fmt.Errorf("config update failed: %w", err)
	}
	_, err = cmd.loadConfig(userConfigPath)
	if err != nil {
		return fmt.Errorf("validating published config: %w", err)
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

func canonicalPath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving config path: %w", err)
	}

	resolvedPath, err := filepath.EvalSymlinks(absolutePath)
	if err == nil {
		return resolvedPath, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return absolutePath, nil
	}
	return "", fmt.Errorf("resolving config symlinks: %w", err)
}

type fileSnapshot struct {
	data   []byte
	mode   os.FileMode
	exists bool
}

func snapshotFile(path string) (fileSnapshot, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{data: data, mode: info.Mode(), exists: true}, nil
}

func sameFileSnapshot(left, right fileSnapshot) bool {
	return left.exists == right.exists && left.mode == right.mode && bytes.Equal(left.data, right.data)
}

func sourceRevision(source config.Source) string {
	return source.Revision
}

func hasRemoteSource(source config.Source) bool {
	return source.Repository != "" || source.Path != "" || source.Revision != ""
}

func displayRevision(revision string) string {
	if revision == "" {
		return "n/a"
	}
	return revision
}

func (cmd *schemaFetchCommand) Run() error {
	request, err := cmd.request()
	if err != nil {
		return err
	}

	data, err := cmd.materializer.Materialize(cmd.context, &request)
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

func (cmd *schemaFetchCommand) request() (schema.Request, error) {
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

type materializer interface {
	Materialize(context.Context, *schema.Request) ([]byte, error)
}

type outputWriter interface {
	Write(string, []byte) error
}

type Dependencies struct {
	Context         context.Context
	Stdout          io.Writer
	Stderr          io.Writer
	Generate        func(*generate.Config) (map[string][]byte, error)
	LoadConfig      func(string) (*config.Config, error)
	Materializer    materializer
	RemoteResolver  remoteResolver
	OutputWriter    outputWriter
	configSchemaURL string
}

func Run(args []string, version string, dependencies *Dependencies) error {
	if dependencies == nil {
		dependencies = &Dependencies{}
	}
	dependencies.setDefaults()
	dependencies.configSchemaURL = configSchemaURL(version)
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
		return []string{"schema", "fetch"}
	}
	if args[1] == "update" || args[1] == "fetch" || args[1] == "--help" {
		return args
	}
	normalized := make([]string, 0, len(args)+1)
	normalized = append(normalized, "schema", "fetch")
	normalized = append(normalized, args[1:]...)
	return normalized
}

func newCommandTree(dependencies *Dependencies) *commandTree {
	return &commandTree{
		Generate: generateCommand{
			context:      dependencies.Context,
			loadConfig:   dependencies.LoadConfig,
			materializer: dependencies.Materializer,
			generate:     dependencies.Generate,
			outputWriter: dependencies.OutputWriter,
		},
		Init: initCommand{
			context:         dependencies.Context,
			resolver:        dependencies.RemoteResolver,
			stdout:          dependencies.Stdout,
			configSchemaURL: dependencies.configSchemaURL,
		},
		Schema: schemaCommand{
			Fetch: schemaFetchCommand{
				context:      dependencies.Context,
				loadConfig:   dependencies.LoadConfig,
				materializer: dependencies.Materializer,
				outputWriter: dependencies.OutputWriter,
				stdout:       dependencies.Stdout,
			},
			Update: schemaUpdateCommand{
				context:         dependencies.Context,
				loadConfig:      dependencies.LoadConfig,
				resolver:        dependencies.RemoteResolver,
				outputWriter:    dependencies.OutputWriter,
				stdout:          dependencies.Stdout,
				configSchemaURL: dependencies.configSchemaURL,
			},
		},
	}
}

func configSchemaURL(version string) string {
	if !semver.IsValid(version) || module.IsPseudoVersion(version) {
		return mainConfigSchemaURL
	}
	return releaseSchemaBaseURL + version + "/octoqlgen.schema.yaml"
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
		d.Generate = generate.Generate
	}
	if d.LoadConfig == nil {
		d.LoadConfig = config.Load
	}
	if d.Materializer == nil {
		d.Materializer = schema.NewMaterializer()
	}
	if d.RemoteResolver == nil {
		resolver, ok := d.Materializer.(remoteResolver)
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
