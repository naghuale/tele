//go:build linux || darwin

package outbox

import (
	"fmt"
	"os"
	"syscall"
)

func validateDataDirOwner(
	info os.FileInfo,
) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf(
			"%w: cannot determine directory owner",
			ErrOutboxDataDirInvalid,
		)
	}

	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf(
			"%w: owner does not match current user",
			ErrOutboxDataDirInvalid,
		)
	}

	return nil
}
