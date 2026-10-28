package terrainruntime

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
)

// ValidateEmbedded pins a cached runtime to the manifest in exePath's verified
// Python payload. It rejects replacement of both a cached file and its manifest.
// It performs one streaming hash pass over the ZIP, decompresses only its
// manifest, then hashes disk files against that trusted manifest. The caller
// must trust the executable itself and prevent writes between validation/use.
func ValidateEmbedded(exePath, directory string) error {
	p, found, err := readPayload(exePath)
	if err != nil {
		return err
	}
	if !found {
		return ErrNoPayload
	}
	defer p.file.Close()
	_, m, data, err := readRuntimeArchive(p)
	if err != nil {
		return err
	}
	r, err := openDirectory(directory)
	if err != nil {
		return err
	}
	defer r.Close()
	f, info, err := rootRegular(r, ManifestName)
	if err != nil {
		return err
	}
	if info.Size() != int64(len(data)) {
		_ = f.Close()
		return errors.New("terrainruntime: cached manifest differs from embedded manifest")
	}
	err = verifyCopy(io.Discard, f, metadata(data))
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("terrainruntime: cached manifest differs from embedded manifest: %w", err)
	}
	if closeErr != nil {
		return closeErr
	}
	// Use the already parsed embedded manifest, rather than reopening and
	// trusting mutable cached JSON after comparing it.
	return validateTree(r, m, data)
}

func readRuntimeArchive(p *payload) (*zip.Reader, Manifest, []byte, error) {
	fail := func(err error) (*zip.Reader, Manifest, []byte, error) { return nil, Manifest{}, nil, err }
	section := io.NewSectionReader(p.file, p.offset, p.size)
	central, err := checkZIPDirectory(section, p.size)
	if err != nil {
		return fail(err)
	}
	zr, err := zip.NewReader(section, p.size)
	if err != nil {
		return fail(fmt.Errorf("terrainruntime: ZIP: %w", err))
	}
	entries, err := archiveEntries(zr.File, central)
	if err != nil {
		return fail(err)
	}
	mf, ok := entries[ManifestName]
	if !ok {
		return fail(errors.New("terrainruntime: archive missing runtime-manifest.json"))
	}
	if mf.UncompressedSize64 > maxManifestBytes {
		return fail(errors.New("terrainruntime: manifest exceeds 8 MiB"))
	}
	rc, err := mf.Open()
	if err != nil {
		return fail(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(rc, maxManifestBytes+1))
	closeErr := rc.Close()
	if readErr != nil {
		return fail(readErr)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if uint64(len(data)) != mf.UncompressedSize64 {
		return fail(errors.New("terrainruntime: manifest size mismatch"))
	}
	m, err := parseManifest(data)
	if err != nil {
		return fail(err)
	}
	if len(entries) != len(m.Files)+1 {
		return fail(errors.New("terrainruntime: ZIP members differ from manifest"))
	}
	for name, meta := range m.Files {
		entry, ok := entries[name]
		if !ok || entry.UncompressedSize64 != uint64(meta.Size) {
			return fail(fmt.Errorf("terrainruntime: ZIP member %q missing or size mismatch", name))
		}
	}
	return zr, m, data, nil
}
