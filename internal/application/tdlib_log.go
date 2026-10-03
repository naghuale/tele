package application

import (
	"path/filepath"

	"telecli/internal/config"
)

// Where the journal of the library itself goes, and why it is a file the
// program names rather than a level it asks for.
//
// TDLib writes its internal log to stderr on its own, and lowering the
// verbosity does not silence it: level 1 keeps the warnings, and one of
// them — a line about a poll whose answers were corrected — appeared over
// a running interface on 03.10 (owner, task 5, review-tele5, SHA 6a77c6e).
// The terminal belongs to the renderer while the interface is on it, so the
// library is told where to write instead of being asked to be quieter: the
// journal goes beside the interface's own reasons, which the user has
// already been told about and can already find.

// TDLibLogFileName is the file TDLib's own journal goes to.
//
// It sits next to telecli.log rather than inside the tdlib folder, because
// that folder holds the library's database and its downloaded files, and
// nothing a user will ever open belongs among them.
const TDLibLogFileName = "tdlib.log"

// TDLibLogPath is the file the library's journal is written to, or "" when
// no data folder is configured.
//
// It is exported for the same reason TUILogPath is: `telecli doctor`
// prints it, and a journal nobody can find is a journal that has to be
// silent to stay useful.
func TDLibLogPath(cfg config.Config) string {
	dataDir := resolveDataDir(cfg)
	if dataDir == "" {
		return ""
	}

	return filepath.Join(dataDir, TDLibLogFileName)
}
