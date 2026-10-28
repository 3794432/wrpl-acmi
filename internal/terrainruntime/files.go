package terrainruntime

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Validate checks the complete runtime, including exact filename case, hashes,
// sizes, and absence of extra files/directories, symlinks, and reparse points.
// No cache files are exempted. It does not execute Python.
func Validate(directory string) error {
	r, err := openDirectory(directory)
	if err != nil {
		return fmt.Errorf("terrainruntime: runtime directory: %w", err)
	}
	defer r.Close()
	m, data, err := loadManifest(r)
	if err != nil {
		return err
	}
	return validateTree(r, m, data)
}

// Check every existing ancestor as well as the leaf. Windows junctions and
// other reparse points are rejected even when Go does not label them symlinks.
func checkAncestors(name string) error {
	abs, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	for p := abs; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if unsafeInfo(info) {
			return fmt.Errorf("symlink or reparse point %q", p)
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

func unsafeInfo(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0 || isReparse(info)
}

func openDirectory(name string) (*os.Root, error) {
	if name == "" {
		return nil, errors.New("directory path is required")
	}
	if err := checkAncestors(name); err != nil {
		return nil, err
	}
	return os.OpenRoot(name)
}

func openRegular(name string) (*os.File, os.FileInfo, error) {
	if err := checkAncestors(name); err != nil {
		return nil, nil, err
	}
	before, err := os.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if unsafeInfo(before) || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%q is not a regular file", name)
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, nil, err
	}
	return checkedFile(f, before)
}

func rootRegular(r *os.Root, name string) (*os.File, os.FileInfo, error) {
	before, err := r.Lstat(filepath.FromSlash(name))
	if err != nil {
		return nil, nil, err
	}
	if unsafeInfo(before) || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%q is not a regular file", name)
	}
	f, err := r.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, nil, err
	}
	return checkedFile(f, before)
}

func checkedFile(f *os.File, before os.FileInfo) (*os.File, os.FileInfo, error) {
	after, err := f.Stat()
	if err == nil && (unsafeInfo(before) || unsafeInfo(after) || !before.Mode().IsRegular() || !after.Mode().IsRegular() || !os.SameFile(before, after)) {
		err = errors.New("file changed or is not a regular file")
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, after, nil
}

func loadManifest(r *os.Root) (Manifest, []byte, error) {
	f, info, err := rootRegular(r, ManifestName)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("terrainruntime: manifest: %w", err)
	}
	defer f.Close()
	if info.Size() > maxManifestBytes {
		return Manifest{}, nil, errors.New("terrainruntime: manifest exceeds 8 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, nil, err
	}
	if int64(len(data)) != info.Size() {
		return Manifest{}, nil, errors.New("terrainruntime: manifest changed while reading")
	}
	m, err := parseManifest(data)
	return m, data, err
}

func validateTree(r *os.Root, m Manifest, data []byte) error {
	expected := expectedPaths(m)
	seen := make(map[string]bool, len(expected))
	var walk func(string) error
	walk = func(dir string) error {
		d, err := r.Open(filepath.FromSlash(dir))
		if err != nil {
			return err
		}
		defer d.Close()
		for {
			entries, err := d.ReadDir(128)
			if err != nil && err != io.EOF {
				return err
			}
			for _, entry := range entries {
				name := entry.Name()
				if dir != "." {
					name = dir + "/" + name
				}
				want, ok := expected[foldKey(name)]
				if !ok || want.name != name || seen[name] {
					return fmt.Errorf("terrainruntime: extra or incorrectly cased path %q", name)
				}
				info, err := r.Lstat(filepath.FromSlash(name))
				if err != nil {
					return err
				}
				if unsafeInfo(info) || want.directory != info.IsDir() || (!want.directory && !info.Mode().IsRegular()) {
					return fmt.Errorf("terrainruntime: unsafe runtime member %q", name)
				}
				seen[name] = true
				if want.directory {
					if err := walk(name); err != nil {
						return err
					}
				} else {
					meta := m.Files[name]
					if name == ManifestName {
						meta = metadata(data)
					}
					f, info, err := rootRegular(r, name)
					if err != nil {
						return err
					}
					if info.Size() != meta.Size {
						_ = f.Close()
						return fmt.Errorf("terrainruntime: size mismatch for %q", name)
					}
					err = verifyCopy(io.Discard, f, meta)
					closeErr := f.Close()
					if err != nil {
						return fmt.Errorf("terrainruntime: %s: %w", name, err)
					}
					if closeErr != nil {
						return closeErr
					}
				}
			}
			if err == io.EOF {
				break
			}
		}
		return nil
	}
	if err := walk("."); err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return errors.New("terrainruntime: runtime has missing manifest members")
	}
	return nil
}

func metadata(data []byte) File {
	return File{int64(len(data)), fmt.Sprintf("%x", sha256.Sum256(data))}
}

// Reading through size+1 ensures the ZIP reader reaches EOF and checks CRC,
// even for zero-byte files. Memory and writes stay bounded independently of
// untrusted archive headers and concurrently growing disk files.
func verifyCopy(dst io.Writer, src io.Reader, meta File) error {
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(dst, h), io.LimitReader(src, meta.Size+1), make([]byte, copyBufferBytes))
	if err != nil {
		return err
	}
	if n != meta.Size {
		return fmt.Errorf("size mismatch: got %d, expected %d", n, meta.Size)
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != meta.SHA256 {
		return errors.New("SHA-256 mismatch")
	}
	return nil
}

func absentTarget(name string) (string, error) {
	if name == "" {
		return "", errors.New("terrainruntime: output/destination path is required")
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", fmt.Errorf("terrainruntime: %q already exists", name)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := checkAncestors(filepath.Dir(abs)); err != nil {
		return "", err
	}
	info, err := os.Stat(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("terrainruntime: parent must be a directory")
	}
	return abs, nil
}
