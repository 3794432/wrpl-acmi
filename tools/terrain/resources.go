// Package terrain embeds the PowerShell installer and reference metadata that
// ship with the standalone terrain installer.
package terrain

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Assets contains exactly the scripts and reference files needed by
// wrpl-terrain-installer. PowerShell scripts are kept together so
// dot-sourced Terrain-Format.ps1 and Terrain-194-Encoding.ps1 resolve beside
// their callers.
//
//go:embed Install-All-Terrains.ps1 Install-Geographic-Terrains.ps1 Terrain-Format.ps1 Terrain-194-Encoding.ps1 Set-Tacview-Terrain-Format.ps1 AtlasReferences.json GeoReferences.json
var Assets embed.FS

var resourceNames = []string{
	"Install-All-Terrains.ps1",
	"Install-Geographic-Terrains.ps1",
	"Terrain-Format.ps1",
	"Terrain-194-Encoding.ps1",
	"Set-Tacview-Terrain-Format.ps1",
	"AtlasReferences.json",
	"GeoReferences.json",
}

// Names returns the embedded resource names in installation order.
func Names() []string {
	return append([]string(nil), resourceNames...)
}

// FS returns the read-only embedded resource tree.
func FS() fs.FS {
	return Assets
}

// Materialize writes the embedded resources into directory. Existing files
// are replaced so a fresh temporary payload always matches this executable.
func Materialize(directory string) error {
	if directory == "" {
		return fmt.Errorf("terrain resource destination is empty")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create terrain resource directory: %w", err)
	}
	for _, name := range resourceNames {
		data, err := Assets.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", name, err)
		}
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("write embedded %s: %w", name, err)
		}
	}
	return nil
}
