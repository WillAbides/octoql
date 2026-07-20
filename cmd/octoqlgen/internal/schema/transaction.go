package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	updateJournalSuffix = ".schema-update.pending"
	journalPhasePending = "pending"
	journalPhaseDone    = "done"
)

type journalSnapshot struct {
	Data   []byte      `json:"data"`
	Exists bool        `json:"exists"`
	Mode   fs.FileMode `json:"mode"`
}

type updateJournal struct {
	ConfigPath     string          `json:"config_path"`
	OriginalConfig journalSnapshot `json:"original_config"`
	OriginalSchema journalSnapshot `json:"original_schema"`
	Phase          string          `json:"phase"`
	SchemaPath     string          `json:"schema_path"`
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
	journalPath := updateJournalPath(resolvedSchemaPath)
	_, err = os.Lstat(journalPath)
	if err == nil {
		return nil, errors.New("recovering an interrupted schema update is required before starting another update")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("checking schema update journal: %w", err)
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
	transaction.journal.Phase = journalPhaseDone
	err := writeJournal(transaction.journalPath, &transaction.journal)
	if err != nil {
		return err
	}
	err = os.Remove(transaction.journalPath)
	if err != nil {
		return fmt.Errorf("removing schema update journal: %w", err)
	}
	return nil
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
	return recoverPendingUpdate(updateJournalPath(resolvedSchemaPath), resolvedSchemaPath)
}

func hasPendingUpdate(schemaPath string) (bool, error) {
	resolvedSchemaPath, err := ResolveSchemaIdentity(schemaPath)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(updateJournalPath(resolvedSchemaPath))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("checking schema update journal: %w", err)
}

func recoverPendingUpdate(journalPath, schemaPath string) error {
	content, err := os.ReadFile(journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading schema update journal: %w", err)
	}

	var journal updateJournal
	err = json.Unmarshal(content, &journal)
	if err != nil {
		return fmt.Errorf("decoding schema update journal: %w", err)
	}
	recordedSchemaPath, err := ResolveSchemaIdentity(journal.SchemaPath)
	if err != nil {
		return err
	}
	if recordedSchemaPath != schemaPath {
		return errors.New("schema update journal belongs to a different schema path")
	}
	if journal.ConfigPath == "" {
		return errors.New("schema update journal is missing its config path")
	}

	switch journal.Phase {
	case journalPhaseDone:
		err = os.Remove(journalPath)
		if err != nil {
			return fmt.Errorf("removing completed schema update journal: %w", err)
		}
		return nil
	case journalPhasePending:
		err = restore(journal.ConfigPath, journal.OriginalConfig)
		if err != nil {
			return fmt.Errorf("restoring config from schema update journal: %w", err)
		}
		err = restore(recordedSchemaPath, journal.OriginalSchema)
		if err != nil {
			return fmt.Errorf("restoring schema from schema update journal: %w", err)
		}
		err = os.Remove(journalPath)
		if err != nil {
			return fmt.Errorf("removing recovered schema update journal: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("schema update journal has unknown phase %q", journal.Phase)
	}
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
	content, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("encoding schema update journal: %w", err)
	}
	err = writeFileAtomically(path, content, 0o600)
	if err != nil {
		return fmt.Errorf("writing schema update journal: %w", err)
	}
	return nil
}

func updateJournalPath(schemaPath string) string {
	return schemaPath + updateJournalSuffix
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
	err = os.Rename(temp.Name(), destination)
	if err != nil {
		return fmt.Errorf("publishing output file: %w", err)
	}
	shouldRemove = false
	return nil
}
