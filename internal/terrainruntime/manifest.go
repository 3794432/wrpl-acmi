// Package terrainruntime packs and verifies Python runtimes, never game assets.
// A bundle contains a stub, a ZIP, and a 64-byte footer: 16-byte magic,
// little-endian uint64 archive offset and size, and the archive's SHA-256.
// Hashes detect corruption; they do not authenticate an untrusted publisher.
package terrainruntime

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	PayloadMagic     = "WRPLPYTHONv1\x00\x00\x00\x00"
	FooterSize       = 64
	ManifestName     = "runtime-manifest.json"
	maxManifestBytes = 8 << 20
	maxFiles         = 10000 // Includes the manifest itself.
	maxFileBytes     = 256 << 20
	maxTotalBytes    = 1 << 30 // Includes the manifest itself.
	maxArchiveBytes  = maxTotalBytes + (32 << 20)
	copyBufferBytes  = 128 << 10
)

// Manifest describes every runtime file except runtime-manifest.json itself.
// Names use case-sensitive, slash-separated relative paths. PythonVersion is
// descriptive metadata; callers choose which Python version to build/use.
type Manifest struct {
	Schema        int             `json:"schema"`
	PythonVersion string          `json:"python_version"`
	Files         map[string]File `json:"files"`
}

// File records the exact size and lowercase hexadecimal SHA-256 of a file.
type File struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// object decodes exact, case-sensitive JSON keys and rejects duplicate keys.
// encoding/json's struct decoding alone permits both duplicates and case aliases.
func object(d *json.Decoder, limit int, field func(string) error) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	if t != json.Delim('{') {
		return errors.New("expected JSON object")
	}
	seen := make(map[string]bool)
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return fmt.Errorf("duplicate or invalid JSON key %q", key)
		}
		if len(seen) >= limit {
			return errors.New("too many JSON members")
		}
		seen[key] = true
		if err := field(key); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func parseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if len(data) > maxManifestBytes || !utf8.Valid(data) {
		return m, errors.New("invalid or oversized runtime manifest")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	fields := 0
	err := object(d, 3, func(key string) error {
		fields++
		switch key {
		case "schema":
			return d.Decode(&m.Schema)
		case "python_version":
			return d.Decode(&m.PythonVersion)
		case "files":
			m.Files = make(map[string]File)
			return object(d, maxFiles-1, func(name string) error {
				var f File
				n := 0
				if err := object(d, 2, func(key string) error {
					n++
					switch key {
					case "size":
						// A pointer distinguishes an explicit null from size zero.
						var size *int64
						if err := d.Decode(&size); err != nil {
							return err
						}
						if size == nil {
							return errors.New("null file size")
						}
						f.Size = *size
						return nil
					case "sha256":
						return d.Decode(&f.SHA256)
					default:
						return fmt.Errorf("unknown file field %q", key)
					}
				}); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				if n != 2 {
					return fmt.Errorf("%s: missing size or sha256", name)
				}
				m.Files[name] = f
				return nil
			})
		default:
			return fmt.Errorf("unknown manifest field %q", key)
		}
	})
	if err != nil {
		return m, fmt.Errorf("terrainruntime: manifest: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return m, errors.New("terrainruntime: trailing manifest JSON")
	}
	if fields != 3 || m.Schema != 1 || strings.TrimSpace(m.PythonVersion) == "" || len(m.PythonVersion) > 128 {
		return m, errors.New("terrainruntime: manifest requires schema 1, python_version, and files")
	}
	if _, ok := m.Files["python.exe"]; !ok {
		return m, errors.New("terrainruntime: manifest missing python.exe (case-sensitive)")
	}
	paths := newPathSet()
	if err := paths.add(ManifestName); err != nil {
		return m, err
	}
	total := int64(len(data))
	for name, f := range m.Files {
		if err := paths.add(name); err != nil {
			return m, err
		}
		if f.Size < 0 || f.Size > maxFileBytes || f.Size > maxTotalBytes-total {
			return m, fmt.Errorf("terrainruntime: %q exceeds runtime size limits", name)
		}
		total += f.Size
		digest, err := hex.DecodeString(f.SHA256)
		if err != nil || len(digest) != 32 || strings.ToLower(f.SHA256) != f.SHA256 {
			return m, fmt.Errorf("terrainruntime: invalid SHA-256 for %q", name)
		}
	}
	return m, nil
}

func safeName(name string) error {
	if len(name) == 0 || len(name) > 1024 || !utf8.ValidString(name) || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, `\:<>"|?*`) {
		return fmt.Errorf("terrainruntime: unsafe runtime path %q", name)
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return fmt.Errorf("terrainruntime: control character in path %q", name)
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return fmt.Errorf("terrainruntime: unsafe runtime path %q", name)
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		deviceSuffix := strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT")
		device := deviceSuffix != base && utf8.RuneCountInString(deviceSuffix) == 1 && strings.ContainsAny(deviceSuffix, "123456789¹²³")
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" ||
			device {
			return fmt.Errorf("terrainruntime: reserved Windows path %q", name)
		}
		if strings.EqualFold(part, "maps") {
			return fmt.Errorf("terrainruntime: game path %q is forbidden", name)
		}
		switch strings.ToLower(path.Ext(part)) {
		case ".data", ".bin", ".blk", ".blkx":
			return fmt.Errorf("terrainruntime: game file %q is forbidden", name)
		}
	}
	return nil
}

// foldKey also covers Unicode case-fold aliases such as K and the Kelvin sign.
func foldKey(s string) string {
	return strings.Map(func(r rune) rune {
		least := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < least {
				least = next
			}
		}
		return least
	}, s)
}

type pathEntry struct {
	name      string
	directory bool
}
type pathSet map[string]pathEntry

func newPathSet() pathSet { return make(pathSet) }
func (s pathSet) add(name string) error {
	if err := safeName(name); err != nil {
		return err
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		dir := i < len(parts)-1
		key := foldKey(p)
		if old, ok := s[key]; ok {
			if old.name != p || !old.directory || !dir {
				return fmt.Errorf("terrainruntime: conflicting paths %q and %q", old.name, name)
			}
		} else {
			// Bound the implicit directory tree as well as the file count.
			if len(s) >= 2*maxFiles {
				return errors.New("terrainruntime: excessive runtime path components")
			}
			s[key] = pathEntry{p, dir}
		}
	}
	return nil
}

func expectedPaths(m Manifest) pathSet {
	s := newPathSet()
	_ = s.add(ManifestName)
	for name := range m.Files {
		_ = s.add(name)
	}
	return s
}
