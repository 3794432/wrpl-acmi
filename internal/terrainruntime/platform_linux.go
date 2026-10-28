package terrainruntime

import (
	"golang.org/x/sys/unix"
	"os"
)

func isReparse(os.FileInfo) bool { return false }

func publish(source, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
}
