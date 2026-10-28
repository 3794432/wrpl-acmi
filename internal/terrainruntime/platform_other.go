//go:build !windows && !linux

package terrainruntime

import (
	"errors"
	"os"
)

func isReparse(os.FileInfo) bool { return false }

func publish(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("terrainruntime: atomic directory publication without replacement is unsupported on this platform")
	}
	// Link is atomic and never replaces an existing target. The caller removes
	// the temporary source link after publication.
	return os.Link(source, destination)
}
