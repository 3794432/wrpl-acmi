package terrainruntime

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNoPayload indicates that an executable has no Python runtime footer.
var ErrNoPayload = errors.New("terrainruntime: no Python runtime payload")

// Pack appends a manifest-verified Python runtime to a stub. Output must be
// outside the runtime directory and must not exist. Publication is atomic.
// This format uses ordinary ZIP (the runtime limits never require ZIP64).
func Pack(stubPath, runtimeDirectory, outPath string) error {
	if stubPath == "" || runtimeDirectory == "" || outPath == "" {
		return errors.New("terrainruntime: stub, runtime, and output paths are required")
	}
	stub, stubInfo, err := openRegular(stubPath)
	if err != nil {
		return fmt.Errorf("terrainruntime: stub: %w", err)
	}
	defer stub.Close()
	r, err := openDirectory(runtimeDirectory)
	if err != nil {
		return err
	}
	defer r.Close()
	m, data, err := loadManifest(r)
	if err != nil {
		return err
	}
	if err := validateTree(r, m, data); err != nil {
		return err
	}
	out, err := absentTarget(outPath)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(runtimeDirectory)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, out)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return errors.New("terrainruntime: output must be outside the runtime directory")
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".runtime-pack-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(stubInfo.Mode().Perm()); err != nil {
		return err
	}
	n, err := io.CopyBuffer(tmp, io.LimitReader(stub, stubInfo.Size()+1), make([]byte, copyBufferBytes))
	if err != nil {
		return err
	}
	if n != stubInfo.Size() {
		return errors.New("terrainruntime: stub changed while copying")
	}
	h := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(tmp, h))
	names := make([]string, 0, len(m.Files)+1)
	for name := range m.Files {
		names = append(names, name)
	}
	names = append(names, ManifestName)
	sort.Strings(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
		header.SetMode(0o644)
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if name == ManifestName {
			err = verifyCopy(w, bytes.NewReader(data), metadata(data))
		} else {
			f, info, openErr := rootRegular(r, name)
			if openErr != nil {
				return openErr
			}
			if info.Size() != m.Files[name].Size {
				_ = f.Close()
				return fmt.Errorf("terrainruntime: %q changed size", name)
			}
			err = verifyCopy(w, f, m.Files[name])
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			return fmt.Errorf("terrainruntime: pack %s: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	end, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	size := end - stubInfo.Size()
	if size <= 0 || size > maxArchiveBytes {
		return errors.New("terrainruntime: ZIP exceeds archive limit")
	}
	var footer [FooterSize]byte
	copy(footer[:16], PayloadMagic)
	binary.LittleEndian.PutUint64(footer[16:24], uint64(stubInfo.Size()))
	binary.LittleEndian.PutUint64(footer[24:32], uint64(size))
	copy(footer[32:], h.Sum(nil))
	if _, err := tmp.Write(footer[:]); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := publish(tmp.Name(), out); err != nil {
		return fmt.Errorf("terrainruntime: publish bundle: %w", err)
	}
	return nil
}

type payload struct {
	file         *os.File
	offset, size int64
	digest       [32]byte
}

// readPayload verifies bounds and archive SHA before exposing the ZIP reader.
func readPayload(exePath string) (*payload, bool, error) {
	f, info, err := openRegular(exePath)
	if err != nil {
		return nil, false, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
		}
	}()
	if info.Size() < FooterSize {
		return nil, false, nil
	}
	var footer [FooterSize]byte
	if _, err := f.ReadAt(footer[:], info.Size()-FooterSize); err != nil {
		return nil, false, err
	}
	if string(footer[:16]) != PayloadMagic {
		return nil, false, nil
	}
	offset := binary.LittleEndian.Uint64(footer[16:24])
	size := binary.LittleEndian.Uint64(footer[24:32])
	end := uint64(info.Size() - FooterSize)
	if offset > end || size == 0 || size > maxArchiveBytes || size != end-offset {
		return nil, true, errors.New("terrainruntime: malformed payload footer bounds")
	}
	p := &payload{file: f, offset: int64(offset), size: int64(size)}
	copy(p.digest[:], footer[32:])
	h := sha256.New()
	if _, err := io.CopyBuffer(h, io.NewSectionReader(f, p.offset, p.size), make([]byte, copyBufferBytes)); err != nil {
		return nil, true, err
	}
	if !bytes.Equal(h.Sum(nil), p.digest[:]) {
		return nil, true, errors.New("terrainruntime: archive SHA-256 mismatch")
	}
	keep = true
	return p, true, nil
}

// PayloadID returns the verified archive SHA-256 as lowercase hex. Missing
// runtime payloads return ErrNoPayload, including terrain-only installers.
func PayloadID(exePath string) (string, error) {
	p, found, err := readPayload(exePath)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrNoPayload
	}
	defer p.file.Close()
	return fmt.Sprintf("%x", p.digest), nil
}

// Extract returns false, nil for a missing Python runtime payload. If its magic
// is present, all errors return found=true. Files are verified in a private
// sibling temporary directory before atomic publication; an existing target
// (including an empty directory) is never replaced or modified.
func Extract(exePath, destination string) (found bool, err error) {
	if exePath == "" || destination == "" {
		return false, errors.New("terrainruntime: executable and destination are required")
	}
	p, found, err := readPayload(exePath)
	if err != nil || !found {
		return found, err
	}
	defer p.file.Close()
	dest, err := absentTarget(destination)
	if err != nil {
		return true, err
	}
	zr, m, data, err := readRuntimeArchive(p)
	if err != nil {
		return true, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".runtime-extract-*")
	if err != nil {
		return true, err
	}
	defer os.RemoveAll(tmp)
	r, err := os.OpenRoot(tmp)
	if err != nil {
		return true, err
	}
	defer r.Close()
	for _, entry := range zr.File {
		name := filepath.FromSlash(entry.Name)
		if err := r.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return true, err
		}
		out, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return true, err
		}
		src, err := entry.Open()
		if err != nil {
			_ = out.Close()
			return true, err
		}
		meta := m.Files[entry.Name]
		if entry.Name == ManifestName {
			meta = metadata(data)
		}
		copyErr := verifyCopy(out, src, meta)
		readCloseErr := src.Close()
		writeCloseErr := out.Close()
		if copyErr != nil {
			return true, fmt.Errorf("terrainruntime: extract %s: %w", entry.Name, copyErr)
		}
		if readCloseErr != nil {
			return true, readCloseErr
		}
		if writeCloseErr != nil {
			return true, writeCloseErr
		}
	}
	if err := validateTree(r, m, data); err != nil {
		return true, err
	}
	if err := r.Close(); err != nil {
		return true, err
	}
	if err := publish(tmp, dest); err != nil {
		return true, fmt.Errorf("terrainruntime: publish runtime: %w", err)
	}
	return true, nil
}
