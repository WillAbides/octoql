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
	"github.com/willabides/octoql/internal/generate"
	yamlv3 "gopkg.in/yaml.v3"
)

type commandTree struct {
	Generate generateCommand  `kong:"cmd,help='Generate GraphQL client code.'"`
	Init     initCommand      `kong:"cmd,help='Create an octoqlgen configuration and materialized schema.'"`
	Schema   schemaCommand    `kong:"cmd,help='Materialize or verify a pinned GraphQL schema.'"`
	Version  kong.VersionFlag `kong:"name='version',help='Show version information.'"`
}

type schemaCommand struct {
	Materialize schemaMaterializeCommand `kong:"cmd,default='1',help='Materialize or verify a pinned GraphQL schema.'"`
	Update      schemaUpdateCommand      `kong:"cmd,help='Update a configured remote schema pin.'"`
}

type schemaMaterializeCommand struct {
	context      context.Context
	loadConfig   func(string) (*config.Config, error)
	materializer materializer
	outputWriter outputWriter
	stdout       io.Writer

	Config        string `kong:"name='config',type='path',placeholder='PATH',help='Path to an octoqlgen configuration file. Defaults to octoqlgen.yaml.'"`
	Output        string `kong:"short='o',name='output',type='path',placeholder='PATH',help='Write the exact schema bytes to a file instead of stdout.'"`
	GitHubVersion string `kong:"name='github-version',placeholder='VERSION',help='Fetch a pinned github/docs schema version (fpt, ghec, or ghes-X.Y).'"`
	SourceURL     string `kong:"name='source-url',placeholder='URL',help='Fetch a schema from an immutable URL.'"`
	Revision      string `kong:"name='revision',placeholder='SHA',help='Full github/docs commit revision for --github-version.'"`
	SHA256        string `kong:"name='sha256',placeholder='HEX',help='Expected SHA-256 for a direct remote source.'"`
}

type remoteResolver interface {
	Resolve(context.Context, config.Source) (schema.RemoteResult, error)
}

type initCommand struct {
	stdout io.Writer

	ConfigPath string `kong:"name='config',type='path',default='octoqlgen.yaml',placeholder='PATH',help='Path for the new octoqlgen configuration.'"`
}

func (cmd *initCommand) Run() error {
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

type schemaUpdateCommand struct {
	context      context.Context
	parseConfig  func(string, []byte) (*config.Config, error)
	resolver     remoteResolver
	outputWriter outputWriter
	stdout       io.Writer

	Config string `kong:"name='config',type='path',default='octoqlgen.yaml',placeholder='PATH',help='Path to an octoqlgen configuration file.'"`
}

func (cmd *schemaUpdateCommand) Run() (err error) {
	err = checkContext(cmd.context)
	if err != nil {
		return err
	}
	userConfigPath, err := filepath.Abs(cmd.Config)
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}
	configPath, err := canonicalPath(userConfigPath)
	if err != nil {
		return err
	}
	unlockConfig, err := acquireConfigUpdateLock(configPath)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, unlockConfig())
	}()
	rawConfig, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading config file %q: %w", cmd.Config, err)
	}
	initialConfig, err := cmd.parseConfigBytes(configPath, rawConfig)
	if err != nil {
		return err
	}
	lockedSchemaPath, err := schema.ResolveSchemaIdentity(initialConfig.SchemaPath())
	if err != nil {
		return err
	}
	unlock, err := acquireUpdateLock(lockedSchemaPath)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, unlock())
	}()

	rawConfig, err = os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading config file %q: %w", cmd.Config, err)
	}
	lockedConfig, err := cmd.parseConfigBytes(configPath, rawConfig)
	if err != nil {
		return err
	}
	err = validateSchemaTarget(lockedSchemaPath, lockedConfig.SchemaPath())
	if err != nil {
		return err
	}
	err = schema.RecoverPendingUpdate(lockedSchemaPath)
	if err != nil {
		return err
	}
	err = checkContext(cmd.context)
	if err != nil {
		return err
	}
	rawConfig, err = os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading config file %q: %w", cmd.Config, err)
	}
	loaded, err := cmd.parseConfigBytes(configPath, rawConfig)
	if err != nil {
		return err
	}
	err = validateSchemaTarget(lockedSchemaPath, loaded.SchemaPath())
	if err != nil {
		return err
	}
	source := loaded.Schema.SourceValue()
	if !hasRemoteSource(source) {
		return errors.New("schema update requires a configured remote schema source")
	}
	err = checkContext(cmd.context)
	if err != nil {
		return err
	}
	result, err := cmd.resolver.Resolve(cmd.context, source)
	if err != nil {
		return err
	}
	err = checkContext(cmd.context)
	if err != nil {
		return err
	}
	originalSchema, err := snapshotFile(lockedSchemaPath)
	if err != nil {
		return fmt.Errorf("reading configured schema: %w", err)
	}
	currentHash := fmt.Sprintf("%x", sha256.Sum256(originalSchema.data))
	if originalSchema.exists && currentHash == result.SHA256 && loaded.Schema.SHA256Value() == result.SHA256 {
		_, outputErr := fmt.Fprintln(cmd.stdout, "schema is unchanged")
		return outputErr
	}
	updatedConfig, err := config.UpdatePin(rawConfig, result.SHA256, result.Revision)
	if err != nil {
		return err
	}
	_, err = config.Parse(updatedConfig)
	if err != nil {
		return fmt.Errorf("validating updated config: %w", err)
	}
	finalConfig, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("checking config before schema publication: %w", err)
	}
	finalSchema, err := snapshotFile(lockedSchemaPath)
	if err != nil {
		return fmt.Errorf("checking schema before publication: %w", err)
	}
	if !bytes.Equal(finalConfig, rawConfig) || !sameFileSnapshot(finalSchema, originalSchema) {
		return errors.New("config or schema changed while updating; no files were published")
	}
	transaction, err := schema.BeginUpdate(lockedSchemaPath, configPath)
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		rollbackErr := transaction.Rollback()
		if rollbackErr != nil {
			return fmt.Errorf(
				"schema update failed and recovery is pending: %w",
				errors.Join(cause, rollbackErr),
			)
		}
		return fmt.Errorf("schema update failed; restored original files: %w", cause)
	}
	err = checkContext(cmd.context)
	if err != nil {
		return rollback(err)
	}
	err = writeSchemaOutput(cmd.outputWriter, lockedSchemaPath, result.Data)
	if err != nil {
		return rollback(fmt.Errorf("publishing updated schema: %w", err))
	}
	err = transaction.MarkSchemaPublished()
	if err != nil {
		return rollback(fmt.Errorf("recording schema publication: %w", err))
	}
	err = checkContext(cmd.context)
	if err != nil {
		return rollback(err)
	}
	currentConfig, err := os.ReadFile(configPath)
	if err != nil {
		return rollback(fmt.Errorf("checking config before update: %w", err))
	}
	if !bytes.Equal(currentConfig, rawConfig) {
		return rollback(errors.New("config changed while updating schema"))
	}
	err = transaction.BeginConfigPublication(updatedConfig)
	if err != nil {
		return rollback(fmt.Errorf("recording config publication: %w", err))
	}
	err = cmd.outputWriter.Write(configPath, updatedConfig)
	if err != nil {
		return rollback(fmt.Errorf("config update failed: %w", err))
	}
	err = transaction.MarkConfigPublished()
	if err != nil {
		return rollback(fmt.Errorf("recording config publication: %w", err))
	}
	publishedConfig, err := os.ReadFile(configPath)
	if err != nil {
		return rollback(fmt.Errorf("reading published config: %w", err))
	}
	published, err := cmd.parseConfigBytes(configPath, publishedConfig)
	if err != nil {
		return rollback(fmt.Errorf("validating published config: %w", err))
	}
	if !schema.SameLockIdentity(lockedSchemaPath, published.SchemaPath()) {
		return rollback(errors.New("published config changed before commit"))
	}
	if !bytes.Equal(publishedConfig, updatedConfig) {
		return rollback(errors.New("published config changed before commit"))
	}
	err = checkContext(cmd.context)
	if err != nil {
		return rollback(err)
	}
	err = transaction.Commit()
	if err != nil {
		return rollback(fmt.Errorf("committing schema update: %w", err))
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

func (cmd *schemaUpdateCommand) parseConfigBytes(
	path string,
	content []byte,
) (*config.Config, error) {
	if cmd.parseConfig != nil {
		return cmd.parseConfig(path, content)
	}
	return config.LoadBytes(path, content)
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("schema update canceled: %w", err)
	}
	return nil
}

func validateSchemaTarget(lockedSchemaPath, configuredSchemaPath string) error {
	identityPath, err := schema.ResolveSchemaIdentity(configuredSchemaPath)
	if err != nil {
		return err
	}
	if identityPath != lockedSchemaPath {
		return errors.New("schema path changed while acquiring update lock")
	}
	resolvedPath, err := schema.ResolveSchemaPath(configuredSchemaPath)
	if err != nil {
		return err
	}
	if resolvedPath != lockedSchemaPath {
		return errors.New("schema path changed while acquiring update lock")
	}
	return nil
}

func acquireUpdateLock(schemaPath string) (func() error, error) {
	return schema.AcquireExclusiveLock(schemaPath)
}

func acquireConfigUpdateLock(configPath string) (func() error, error) {
	unlock, err := schema.AcquireExclusiveLock(configPath)
	if err != nil {
		return nil, fmt.Errorf("acquiring config update lock: %w", err)
	}
	return unlock, nil
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

func (cmd *schemaMaterializeCommand) Run() error {
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

func (cmd *schemaMaterializeCommand) request() (schema.Request, error) {
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

type materializer interface {
	Materialize(context.Context, schema.Request) ([]byte, error)
}

type outputWriter interface {
	Write(string, []byte) error
}

type schemaOutputWriter interface {
	WriteSchema(string, []byte) error
}

type Dependencies struct {
	Context        context.Context
	Stdout         io.Writer
	Stderr         io.Writer
	Generate       func(*generate.Config) (map[string][]byte, error)
	LoadConfig     func(string) (*config.Config, error)
	Materializer   materializer
	RemoteResolver remoteResolver
	OutputWriter   outputWriter
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
		Generate: generateCommand{
			context:      dependencies.Context,
			loadConfig:   dependencies.LoadConfig,
			materializer: dependencies.Materializer,
			generate:     dependencies.Generate,
			outputWriter: dependencies.OutputWriter,
		},
		Init: initCommand{
			stdout: dependencies.Stdout,
		},
		Schema: schemaCommand{
			Materialize: schemaMaterializeCommand{
				context:      dependencies.Context,
				loadConfig:   dependencies.LoadConfig,
				materializer: dependencies.Materializer,
				outputWriter: dependencies.OutputWriter,
				stdout:       dependencies.Stdout,
			},
			Update: schemaUpdateCommand{
				context:      dependencies.Context,
				parseConfig:  config.LoadBytes,
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

type atomicOutputWriter struct {
	beforeSchemaRename func() error
}

func (atomicOutputWriter) Write(destination string, data []byte) (err error) {
	return writeOutputAtomically(destination, data, false, nil)
}

func (writer atomicOutputWriter) WriteSchema(destination string, data []byte) (err error) {
	return writeOutputAtomically(destination, data, true, writer.beforeSchemaRename)
}

func writeSchemaOutput(writer outputWriter, destination string, data []byte) error {
	schemaWriter, ok := writer.(schemaOutputWriter)
	if ok {
		return schemaWriter.WriteSchema(destination, data)
	}
	err := verifySchemaPublicationPath(destination)
	if err != nil {
		return err
	}
	return writer.Write(destination, data)
}

func writeOutputAtomically(
	destination string,
	data []byte,
	rejectSymlink bool,
	beforeRename func() error,
) (err error) {
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
	var info os.FileInfo
	var statErr error
	if rejectSymlink {
		info, statErr = os.Lstat(destination)
	}
	if !rejectSymlink {
		info, statErr = os.Stat(destination)
	}
	if statErr == nil {
		if rejectSymlink && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("schema path must not be a symlink")
		}
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
	if rejectSymlink {
		err = verifySchemaPublicationPath(destination)
		if err != nil {
			return err
		}
	}
	if beforeRename != nil {
		err = beforeRename()
		if err != nil {
			return err
		}
	}
	// A late final-component symlink cannot redirect this write: Rename
	// replaces the directory entry instead of following it. Its insertion is
	// still not reliably observable between the validation above and Rename.
	err = renameOutputAtomically(temp.Name(), destination)
	if err != nil {
		return fmt.Errorf("publishing output file: %w", err)
	}
	shouldRemove = false
	return nil
}

func verifySchemaPublicationPath(destination string) error {
	resolvedPath, err := schema.ResolveSchemaPath(destination)
	if err != nil {
		return err
	}
	if resolvedPath != destination {
		return errors.New("schema path changed before publication")
	}
	return nil
}
