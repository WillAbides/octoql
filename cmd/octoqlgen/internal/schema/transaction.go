package schema

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
)

const (
	journalPhasePending          = "pending"
	journalPhaseSchemaPublished  = "schema_published"
	journalPhaseConfigPublishing = "config_publishing"
	journalPhaseConfigPublished  = "config_published"
	journalPhaseDone             = "done"
	journalDirectory             = "schema-update-journals"
	legacyUpdateJournalSuffix    = ".schema-update.pending"
)

type journalSnapshot struct {
	Data   []byte      `json:"data"`
	Exists bool        `json:"exists"`
	Mode   fs.FileMode `json:"mode"`
}

type updateJournal struct {
	ConfigPath           string          `json:"config_path"`
	OriginalConfig       journalSnapshot `json:"original_config"`
	OriginalSchema       journalSnapshot `json:"original_schema"`
	ExpectedConfigSHA256 string          `json:"expected_config_sha256"`
	Phase                string          `json:"phase"`
	SchemaPath           string          `json:"schema_path"`
}

// UpdateTransaction records the original schema and config until an update has
// published both files.
type UpdateTransaction struct {
	journal     updateJournal
	journalPath string
}

// BeginUpdate records enough state to restore an interrupted schema update.
// The caller must hold the schema's exclusive lock.
func BeginUpdate(schemaPath, configPath string) (*UpdateTransaction, error) {
	resolvedSchemaPath, err := ResolveSchemaPath(schemaPath)
	if err != nil {
		return nil, err
	}
	journalPath, err := updateJournalPath(resolvedSchemaPath)
	if err != nil {
		return nil, err
	}
	legacy, err := hasLegacyJournal(resolvedSchemaPath)
	if err != nil {
		return nil, err
	}
	if legacy {
		return nil, legacyJournalError(resolvedSchemaPath)
	}
	pending, err := journalExists(journalPath)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, errors.New("recovering an interrupted schema update is required before starting another update")
	}

	originalConfig, err := snapshot(configPath)
	if err != nil {
		return nil, fmt.Errorf("snapshotting config before schema update: %w", err)
	}
	originalSchema, err := snapshot(resolvedSchemaPath)
	if err != nil {
		return nil, fmt.Errorf("snapshotting schema before schema update: %w", err)
	}
	journal := updateJournal{
		ConfigPath:     configPath,
		OriginalConfig: originalConfig,
		OriginalSchema: originalSchema,
		Phase:          journalPhasePending,
		SchemaPath:     resolvedSchemaPath,
	}
	err = writeJournal(journalPath, &journal)
	if err != nil {
		return nil, err
	}
	return &UpdateTransaction{
		journal:     journal,
		journalPath: journalPath,
	}, nil
}

// Commit records that both files have been published before removing the
// recovery journal.
func (transaction *UpdateTransaction) Commit() error {
	err := syncTransactionFiles(
		transaction.journal.ConfigPath,
		transaction.journal.SchemaPath,
	)
	if err != nil {
		return fmt.Errorf("syncing published schema update files: %w", err)
	}
	transaction.journal.Phase = journalPhaseDone
	err = writeJournal(transaction.journalPath, &transaction.journal)
	if err != nil {
		return err
	}
	err = removeJournal(transaction.journalPath)
	if err != nil {
		return fmt.Errorf("removing schema update journal: %w", err)
	}
	return nil
}

// MarkSchemaPublished records that recovery must restore the schema but not
// the config if the update is interrupted before config publication begins.
func (transaction *UpdateTransaction) MarkSchemaPublished() error {
	return transaction.setPhase(journalPhaseSchemaPublished)
}

// BeginConfigPublication records the expected config bytes before they are
// published so recovery can avoid overwriting an unrelated concurrent edit.
func (transaction *UpdateTransaction) BeginConfigPublication(config []byte) error {
	sum := sha256.Sum256(config)
	transaction.journal.ExpectedConfigSHA256 = fmt.Sprintf("%x", sum)
	return transaction.setPhase(journalPhaseConfigPublishing)
}

// MarkConfigPublished records that both files were published.
func (transaction *UpdateTransaction) MarkConfigPublished() error {
	return transaction.setPhase(journalPhaseConfigPublished)
}

func (transaction *UpdateTransaction) setPhase(phase string) error {
	transaction.journal.Phase = phase
	return writeJournal(transaction.journalPath, &transaction.journal)
}

// Rollback restores files captured by BeginUpdate.
func (transaction *UpdateTransaction) Rollback() error {
	return recoverPendingUpdate(transaction.journalPath, transaction.journal.SchemaPath)
}

// RecoverPendingUpdate restores a transaction that was interrupted before
// Commit completed. The caller must hold the schema's exclusive lock.
func RecoverPendingUpdate(schemaPath string) error {
	resolvedSchemaPath, err := ResolveSchemaIdentity(schemaPath)
	if err != nil {
		return err
	}
	journalPath, err := updateJournalPath(resolvedSchemaPath)
	if err != nil {
		return err
	}
	legacy, err := hasLegacyJournal(resolvedSchemaPath)
	if err != nil {
		return err
	}
	if legacy {
		return legacyJournalError(resolvedSchemaPath)
	}
	return recoverPendingUpdate(journalPath, resolvedSchemaPath)
}

func hasPendingUpdate(schemaPath string) (bool, error) {
	resolvedSchemaPath, err := ResolveSchemaIdentity(schemaPath)
	if err != nil {
		return false, err
	}
	journalPath, err := updateJournalPath(resolvedSchemaPath)
	if err != nil {
		return false, err
	}
	legacy, err := hasLegacyJournal(resolvedSchemaPath)
	if err != nil {
		return false, err
	}
	if legacy {
		return false, legacyJournalError(resolvedSchemaPath)
	}
	return journalExists(journalPath)
}

func recoverPendingUpdate(journalPath, schemaPath string) error {
	content, exists, err := readJournal(journalPath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	var journal updateJournal
	err = json.Unmarshal(content, &journal)
	if err != nil {
		return fmt.Errorf("decoding schema update journal: %w", err)
	}
	if journal.SchemaPath != schemaPath {
		return errors.New("schema update journal belongs to a different schema path")
	}
	if journal.ConfigPath == "" {
		return errors.New("schema update journal is missing its config path")
	}

	switch journal.Phase {
	case journalPhaseDone:
		err = removeJournal(journalPath)
		if err != nil {
			return fmt.Errorf("removing completed schema update journal: %w", err)
		}
		return nil
	case journalPhasePending, journalPhaseSchemaPublished:
		err = restore(schemaPath, journal.OriginalSchema)
		if err != nil {
			return fmt.Errorf("restoring schema from schema update journal: %w", err)
		}
		err = syncRecoveredTransactionFiles(schemaPath)
		if err != nil {
			return fmt.Errorf("syncing recovered schema update files: %w", err)
		}
		err = removeJournal(journalPath)
		if err != nil {
			return fmt.Errorf("removing recovered schema update journal: %w", err)
		}
		return nil
	case journalPhaseConfigPublishing, journalPhaseConfigPublished:
		restoreConfig, err := shouldRestoreConfig(&journal)
		if err != nil {
			return err
		}
		if restoreConfig {
			err = restore(journal.ConfigPath, journal.OriginalConfig)
			if err != nil {
				return fmt.Errorf("restoring config from schema update journal: %w", err)
			}
		}
		err = restore(schemaPath, journal.OriginalSchema)
		if err != nil {
			return fmt.Errorf("restoring schema from schema update journal: %w", err)
		}
		syncedPaths := []string{schemaPath}
		if restoreConfig {
			syncedPaths = append(syncedPaths, journal.ConfigPath)
		}
		err = syncRecoveredTransactionFiles(syncedPaths...)
		if err != nil {
			return fmt.Errorf("syncing recovered schema update files: %w", err)
		}
		err = removeJournal(journalPath)
		if err != nil {
			return fmt.Errorf("removing recovered schema update journal: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("schema update journal has unknown phase %q", journal.Phase)
	}
}

func shouldRestoreConfig(journal *updateJournal) (bool, error) {
	if journal.ExpectedConfigSHA256 == "" {
		return false, nil
	}
	content, err := os.ReadFile(journal.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading config before recovery: %w", err)
	}
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum) == journal.ExpectedConfigSHA256, nil
}

func snapshot(path string) (journalSnapshot, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return journalSnapshot{}, nil
	}
	if err != nil {
		return journalSnapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return journalSnapshot{}, err
	}
	return journalSnapshot{
		Data:   data,
		Exists: true,
		Mode:   info.Mode(),
	}, nil
}

func restore(path string, snapshot journalSnapshot) error {
	if !snapshot.Exists {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return nil
	}
	return writeFileAtomically(path, snapshot.Data, snapshot.Mode)
}

func writeJournal(path string, journal *updateJournal) error {
	_, err := journalExists(path)
	if err != nil {
		return err
	}
	content, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("encoding schema update journal: %w", err)
	}
	err = writeFileAtomically(path, content, 0o600)
	if err != nil {
		return fmt.Errorf("writing schema update journal: %w", err)
	}
	err = secureJournalFile(path)
	if err != nil {
		return fmt.Errorf("securing schema update journal: %w", err)
	}
	err = syncDirectory(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("syncing schema update journal directory: %w", err)
	}
	return nil
}

func updateJournalPath(schemaPath string) (string, error) {
	stateDirectory, err := journalStateDirectory()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(schemaPath))
	return filepath.Join(stateDirectory, fmt.Sprintf("%x", sum)), nil
}

func journalStateDirectory() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("locating schema update journal directory: %w", err)
	}
	if currentUser.HomeDir == "" {
		return "", errors.New("locating schema update journal directory: current user has no home directory")
	}
	homeDirectory := filepath.Clean(currentUser.HomeDir)
	err = verifyJournalParent(homeDirectory)
	if err != nil {
		return "", err
	}
	applicationDirectory := filepath.Join(homeDirectory, ".octoqlgen")
	err = createJournalDirectory(applicationDirectory)
	if err != nil {
		return "", fmt.Errorf("creating schema update journal directory: %w", err)
	}
	err = verifyJournalParent(applicationDirectory)
	if err != nil {
		return "", err
	}
	err = secureJournalDirectory(applicationDirectory)
	if err != nil {
		return "", fmt.Errorf("securing schema update journal parent directory: %w", err)
	}
	directory := filepath.Join(applicationDirectory, journalDirectory)
	err = createJournalDirectory(directory)
	if err != nil {
		return "", fmt.Errorf("creating schema update journal directory: %w", err)
	}
	err = verifyJournalParent(directory)
	if err != nil {
		return "", err
	}
	err = secureJournalDirectory(directory)
	if err != nil {
		return "", fmt.Errorf("securing schema update journal directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", fmt.Errorf("checking schema update journal directory: %w", err)
	}
	err = verifyJournalDirectory(directory, info)
	if err != nil {
		return "", err
	}
	return directory, nil
}

func createJournalDirectory(directory string) error {
	err := os.Mkdir(directory, 0o700)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	err = syncDirectory(directory)
	if err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(directory))
}

func verifyJournalParent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("checking schema update journal parent directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("schema update journal parent directory %q must be a directory, not a symlink", path)
	}
	if !journalParentModeIsSecure(info) {
		return fmt.Errorf("schema update journal parent directory %q must not be writable by other users", path)
	}
	err = verifyJournalParentACL(path)
	if err != nil {
		return fmt.Errorf("checking schema update journal parent directory ACL: %w", err)
	}
	err = verifyJournalParentOwner(path, info)
	if err != nil {
		return fmt.Errorf("checking schema update journal parent directory ownership: %w", err)
	}
	return nil
}

func hasLegacyJournal(schemaPath string) (bool, error) {
	_, err := os.Lstat(legacyJournalPath(schemaPath))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking legacy schema update journal: %w", err)
	}
	return true, nil
}

func legacyJournalPath(schemaPath string) string {
	return schemaPath + legacyUpdateJournalSuffix
}

func legacyJournalError(schemaPath string) error {
	return fmt.Errorf(
		"legacy schema update journal %q cannot be recovered automatically; inspect and remove it before retrying",
		legacyJournalPath(schemaPath),
	)
}

func journalExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking schema update journal: %w", err)
	}
	err = verifyJournalFile(path, info)
	if err != nil {
		return false, err
	}
	return true, nil
}

func readJournal(path string) ([]byte, bool, error) {
	exists, err := journalExists(path)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("reading schema update journal: %w", err)
	}
	return content, true, nil
}

func removeJournal(path string) error {
	exists, err := journalExists(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	err = os.Remove(path)
	if err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func verifyJournalDirectory(path string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("schema update journal directory %q must be a directory, not a symlink", path)
	}
	if !journalModeIsPrivate(info) {
		return fmt.Errorf("schema update journal directory %q must not be accessible by other users", path)
	}
	err := verifyJournalACL(path)
	if err != nil {
		return fmt.Errorf("checking schema update journal directory ACL: %w", err)
	}
	err = verifyJournalOwner(path, info)
	if err != nil {
		return fmt.Errorf("checking schema update journal directory ownership: %w", err)
	}
	return nil
}

func verifyJournalFile(path string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("schema update journal %q must be a regular file, not a symlink", path)
	}
	if !journalModeIsPrivate(info) {
		return fmt.Errorf("schema update journal %q must not be accessible by other users", path)
	}
	err := verifyJournalACL(path)
	if err != nil {
		return fmt.Errorf("checking schema update journal ACL: %w", err)
	}
	err = verifyJournalOwner(path, info)
	if err != nil {
		return fmt.Errorf("checking schema update journal ownership: %w", err)
	}
	return nil
}

func syncTransactionFiles(paths ...string) error {
	for _, path := range paths {
		err := syncFile(path)
		if err != nil {
			return err
		}
		err = syncDirectory(filepath.Dir(path))
		if err != nil {
			return err
		}
	}
	return nil
}

func syncRecoveredTransactionFiles(paths ...string) error {
	for _, path := range paths {
		err := syncFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		err = syncDirectory(filepath.Dir(path))
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomically(destination string, data []byte, mode fs.FileMode) (err error) {
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

	_, err = temp.Write(data)
	if err != nil {
		return fmt.Errorf("writing temporary output file: %w", err)
	}
	err = temp.Chmod(mode.Perm())
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
	err = renameFileAtomically(temp.Name(), destination)
	if err != nil {
		return fmt.Errorf("publishing output file: %w", err)
	}
	shouldRemove = false
	return nil
}
