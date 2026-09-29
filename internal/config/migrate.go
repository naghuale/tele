package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// The move of the settings file out of the old place, decided by the
// owner on 29.09.2026.
//
// It is all or nothing, and in this order:
//
//  1. the file is copied to the new path, into a directory only its
//     owner can enter and with a file only its owner can read;
//  2. the copy is read back and every setting in it is compared with
//     the old file, so a truncated or garbled copy is caught before
//     anything is renamed;
//  3. only then is the old file renamed to config.toml.moved-<date>.
//
// The old file is never deleted and the move is never silent: a run
// prints one line about it, and a failure at any step leaves the machine
// exactly as it was, because the new path did not exist before and the
// copy is removed again. telecli then keeps reading the old file, so a
// refused move costs one line of warning and nothing else.

// migrationDateLayout is the date in the name the old file keeps.
const migrationDateLayout = "2006-01-02"

// migrationBackupLimit bounds the search for a free backup name.
const migrationBackupLimit = 100

// MigrationOps are the steps of the move, overridable so a test can fail
// one of them without a file system that refuses.
type MigrationOps struct {
	// Copy puts the old file at the new path.
	Copy func(legacyPath, newPath string) error
	// Verify reports whether the copy holds the same settings as the
	// old file.
	Verify func(legacyPath, newPath string) error
	// MoveAside renames the old file to its backup name.
	MoveAside func(legacyPath, backupPath string) error
	// Remove drops the copy of a move that cannot be finished.
	Remove func(path string) error
	// Now is the clock the backup name is dated with.
	Now func() time.Time
}

func (o MigrationOps) withDefaults() MigrationOps {
	if o.Copy == nil {
		o.Copy = copySettingsFile
	}
	if o.Verify == nil {
		o.Verify = verifySettingsFile
	}
	if o.MoveAside == nil {
		o.MoveAside = func(legacyPath, backupPath string) error {
			return os.Rename(legacyPath, backupPath)
		}
	}
	if o.Remove == nil {
		o.Remove = os.Remove
	}
	if o.Now == nil {
		o.Now = time.Now
	}

	return o
}

// Migration is what the move of one settings file did.
type Migration struct {
	// From is the old path.
	From string
	// To is the new path.
	To string
	// Backup is where the old file ended up. It is empty when the move
	// failed.
	Backup string
	// Err is why the move failed, if it did.
	Err error
}

// migrateSettingsFile copies the settings file to its new place and
// keeps the old one under a dated name.
//
// The error it reports names paths and never the contents of a file: a
// path is what a user can act on.
func migrateSettingsFile(
	legacyPath string,
	newPath string,
	ops MigrationOps,
) Migration {
	ops = ops.withDefaults()

	moved := Migration{From: legacyPath, To: newPath}

	if err := ops.Copy(legacyPath, newPath); err != nil {
		moved.Err = fmt.Errorf(
			"%w: copy to %s: %w",
			ErrConfigWrite,
			newPath,
			err,
		)

		return moved
	}

	// undo drops the copy of a move that cannot be finished. The new
	// path was empty before this call, so removing the copy restores
	// that, and a program must never find two files where it expects
	// one.
	undo := func(stepErr error) Migration {
		if err := ops.Remove(newPath); err != nil {
			moved.Err = fmt.Errorf(
				"%w: %v, and the copy at %s could not be removed "+
					"either (%v): remove it by hand before the next "+
					"start of telecli",
				ErrConfigWrite,
				stepErr,
				newPath,
				err,
			)

			return moved
		}

		moved.Err = stepErr

		return moved
	}

	if err := ops.Verify(legacyPath, newPath); err != nil {
		return undo(fmt.Errorf(
			"%w: the copy at %s does not hold the same settings: %w",
			ErrConfigWrite,
			newPath,
			err,
		))
	}

	backup, err := availableBackupPath(legacyPath, ops.Now())
	if err != nil {
		return undo(err)
	}

	if err := ops.MoveAside(legacyPath, backup); err != nil {
		return undo(fmt.Errorf(
			"%w: move %s aside to %s: %w",
			ErrConfigWrite,
			legacyPath,
			backup,
			err,
		))
	}

	moved.Backup = backup

	return moved
}

// copySettingsFile writes the old file to the new path, byte for byte.
//
// WriteRaw is the same atomic write telecli uses for any other
// configuration, so a reader never sees half a file and the modes are
// the ones the settings are given everywhere else.
func copySettingsFile(legacyPath, newPath string) error {
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return err
	}

	return WriteRaw(newPath, data)
}

// verifySettingsFile reads both files and compares every setting.
//
// The comparison is on the values, not on the bytes: a comment or a
// different order of the same settings is the same configuration, and
// what matters is that the copy says what the old file said.
func verifySettingsFile(legacyPath, newPath string) error {
	before, err := loadFile(legacyPath)
	if err != nil {
		return err
	}

	after, err := loadFile(newPath)
	if err != nil {
		return err
	}

	if !reflect.DeepEqual(before, after) {
		return errors.New("a setting differs")
	}

	return nil
}

// availableBackupPath returns the name the old file keeps: the path with
// the date of the move on it, and a counter if that name is taken.
//
// The old file is never overwritten and never removed, so a second move
// on the same day must not land on the first one.
func availableBackupPath(legacyPath string, now time.Time) (string, error) {
	base := legacyPath + ".moved-" + now.Format(migrationDateLayout)

	candidate := base
	for attempt := 2; attempt <= migrationBackupLimit; attempt++ {
		taken, err := fileExists(candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}

		candidate = fmt.Sprintf("%s-%d", base, attempt)
	}

	return "", fmt.Errorf(
		"%w: %d names like %s are already taken",
		ErrConfigWrite,
		migrationBackupLimit,
		filepath.Base(base),
	)
}
