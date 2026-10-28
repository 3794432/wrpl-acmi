package terrain

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Conversion sources and notices only. Game archives and generated rasters
// must never be embedded in the publicly distributed installer.
//
//go:embed converter/*.py converter/LICENSES/* georeference/UPSTREAM-* runtime-lock.json
var conversionAssets embed.FS

func ConversionDigest() string {
	h := sha256.New()
	for _, tree := range []fs.FS{conversionAssets, Assets} {
		_ = fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(tree, name)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "%d:%s:%d:", len(name), name, len(data))
			h.Write(data)
			return nil
		})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func MaterializeConversion(directory string) error {
	if err := Materialize(directory); err != nil {
		return err
	}
	return fs.WalkDir(conversionAssets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := conversionAssets.ReadFile(name)
		if err != nil {
			return err
		}
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
