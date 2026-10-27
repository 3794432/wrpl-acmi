// Package terrainbundle packs terrain files into, and extracts terrain files
// from, a single-file installer payload embedded after a stub executable.
//
// A payload is laid out as:
//
//	stub executable bytes
//	ZIP archive containing the terrain files
//	64-byte footer
//
// The footer contains the 16-byte PayloadMagic, a little-endian uint64 byte
// offset to the ZIP archive, a little-endian uint64 ZIP archive size, and the
// 32-byte SHA-256 digest of the ZIP archive. The ZIP archive is written in
// ZIP64 form; entries use Deflate compression.
package terrainbundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// FooterSize is the fixed number of bytes at the end of an installer.
	FooterSize = 64
	// PayloadMagic is exactly 16 bytes. The trailing NUL bytes are intentional.
	PayloadMagic = "WRPLTERRAINv1\x00\x00\x00"

	maxReferenceBytes = 8 << 20
	maxMetadataBytes  = 8 << 20
	maxEntries        = 512
	maxFileBytes      = 256 << 20
	maxTotalBytes     = 16 << 30
	maxMapCount       = 128 // Safety bound, independent of the current catalogue size.
	copyBufferBytes   = 128 << 10
)

// Pack appends a ZIP64 terrain payload to a stub executable. The result is
// written through a temporary file in the output directory and renamed into
// place only after all inputs and the appended archive have been verified.
// An existing output, the stub, and every source file are never overwritten.
// Only AtlasReferences.json, GeoReferences.json, and the documented three
// files for every AtlasReferences map id are archived. Other regular files and
// directories produced by the converter may coexist in the source tree. Only
// directories named by AtlasReferences are selected below maps/; other map
// directories are ignored without reading their contents.
func Pack(stubPath, sourceRoot, outPath string) error {
	if len(PayloadMagic) != 16 {
		panic("terrainbundle: PayloadMagic must be exactly 16 bytes")
	}
	if stubPath == "" || sourceRoot == "" || outPath == "" {
		return errors.New("terrainbundle: stub, source, and output paths are required")
	}

	stub, err := inspectRegularFile(stubPath)
	if err != nil {
		return fmt.Errorf("terrainbundle: stub: %w", err)
	}
	defer stub.file.Close()
	rootInfo, err := os.Lstat(sourceRoot)
	if err != nil {
		return fmt.Errorf("terrainbundle: source root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New("terrainbundle: source root must be a real directory")
	}

	inputs, err := discoverInputs(sourceRoot)
	if err != nil {
		return fmt.Errorf("terrainbundle: source: %w", err)
	}
	if err := preflightOutput(stubPath, stub.info, sourceRoot, outPath); err != nil {
		return err
	}

	temp, tempName, err := createOutputTemp(outPath)
	if err != nil {
		return err
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tempName)
		}
	}()

	if err := copyStub(temp, stub); err != nil {
		_ = temp.Close()
		return fmt.Errorf("terrainbundle: copy stub: %w", err)
	}

	zw := zip.NewWriter(temp)
	for _, input := range inputs {
		if err := addInput(zw, input); err != nil {
			_ = zw.Close()
			_ = temp.Close()
			return fmt.Errorf("terrainbundle: add %s: %w", input.archiveName, err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("terrainbundle: finish archive: %w", err)
	}

	archiveEnd, err := temp.Seek(0, io.SeekCurrent)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("terrainbundle: locate archive end: %w", err)
	}
	archiveOffset := stub.info.Size()
	if archiveEnd < archiveOffset {
		_ = temp.Close()
		return errors.New("terrainbundle: archive precedes stub end")
	}
	archiveEnd, err = ensureZIP64Trailer(temp, archiveOffset, archiveEnd)
	if err != nil {
		_ = temp.Close()
		return err
	}
	archiveSize := archiveEnd - archiveOffset

	digest, err := hashSection(temp, archiveOffset, archiveSize)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("terrainbundle: hash archive: %w", err)
	}
	footer := make([]byte, FooterSize)
	copy(footer, PayloadMagic)
	binary.LittleEndian.PutUint64(footer[16:24], uint64(archiveOffset))
	binary.LittleEndian.PutUint64(footer[24:32], uint64(archiveSize))
	copy(footer[32:64], digest[:])
	if _, err := temp.Seek(archiveEnd, io.SeekStart); err != nil {
		_ = temp.Close()
		return fmt.Errorf("terrainbundle: seek footer: %w", err)
	}
	if _, err := temp.Write(footer); err != nil {
		_ = temp.Close()
		return fmt.Errorf("terrainbundle: write footer: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("terrainbundle: sync output: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("terrainbundle: close output: %w", err)
	}

	if _, err := os.Lstat(outPath); err == nil {
		return fmt.Errorf("terrainbundle: output %q appeared while packing; refusing to replace it", outPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("terrainbundle: recheck output: %w", err)
	}
	if err := os.Rename(tempName, outPath); err != nil {
		return fmt.Errorf("terrainbundle: publish output: %w", err)
	}
	keepTemp = true
	return nil
}

// Extract locates and verifies the payload in executablePath, then writes its
// files below a newly created destination directory. It returns false, nil
// when the executable has no terrain payload magic. A malformed footer,
// digest mismatch, unsafe entry, archive limit violation, or ZIP CRC error is
// returned to the caller. destination must not already exist.
// Extraction accepts at most 512 entries and 256 MiB per file, with at most
// 16 GiB extracted in total.
func Extract(executablePath, destination string) (bool, error) {
	if executablePath == "" || destination == "" {
		return false, errors.New("terrainbundle: executable and destination paths are required")
	}
	exe, err := inspectRegularFile(executablePath)
	if err != nil {
		return false, fmt.Errorf("terrainbundle: executable: %w", err)
	}
	defer exe.file.Close()

	if exe.info.Size() < FooterSize {
		return false, nil
	}
	footer := make([]byte, FooterSize)
	if _, err := exe.file.ReadAt(footer, exe.info.Size()-FooterSize); err != nil {
		return false, fmt.Errorf("terrainbundle: read footer: %w", err)
	}
	if !bytes.Equal(footer[:16], []byte(PayloadMagic)) {
		return false, nil
	}
	archiveOffset := binary.LittleEndian.Uint64(footer[16:24])
	archiveSize := binary.LittleEndian.Uint64(footer[24:32])
	if archiveOffset > uint64(exe.info.Size()-FooterSize) ||
		archiveSize == 0 ||
		archiveSize != uint64(exe.info.Size()-FooterSize)-archiveOffset {
		return true, errors.New("terrainbundle: malformed payload footer bounds")
	}
	if err := validateDestination(destination); err != nil {
		return true, err
	}

	section := io.NewSectionReader(exe.file, int64(archiveOffset), int64(archiveSize))
	h := sha256.New()
	if _, err := io.CopyBuffer(h, section, make([]byte, copyBufferBytes)); err != nil {
		return true, fmt.Errorf("terrainbundle: hash payload: %w", err)
	}
	if !bytes.Equal(h.Sum(nil), footer[32:64]) {
		return true, errors.New("terrainbundle: payload SHA-256 mismatch")
	}

	archiveReader := io.NewSectionReader(exe.file, int64(archiveOffset), int64(archiveSize))
	if err := precheckArchiveEntryCount(archiveReader, int64(archiveSize)); err != nil {
		return true, err
	}
	zr, err := zip.NewReader(archiveReader, int64(archiveSize))
	if err != nil {
		return true, fmt.Errorf("terrainbundle: open ZIP payload: %w", err)
	}
	files, err := validateArchiveEntries(zr.File)
	if err != nil {
		return true, err
	}

	tempRoot, err := createExtractTemp(destination)
	if err != nil {
		return true, err
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.RemoveAll(tempRoot)
		}
	}()
	if err := extractFiles(zr.File, files, tempRoot); err != nil {
		return true, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return true, fmt.Errorf("terrainbundle: destination %q appeared during extraction; refusing to replace it", destination)
	} else if !os.IsNotExist(err) {
		return true, fmt.Errorf("terrainbundle: recheck destination: %w", err)
	}
	if err := os.Rename(tempRoot, destination); err != nil {
		return true, fmt.Errorf("terrainbundle: publish destination: %w", err)
	}
	keepTemp = true
	return true, nil
}

type inspectedFile struct {
	file *os.File
	info os.FileInfo
}

func inspectRegularFile(name string) (inspectedFile, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return inspectedFile{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return inspectedFile{}, fmt.Errorf("%q must be a regular file and not a symlink", name)
	}
	f, err := os.Open(name)
	if err != nil {
		return inspectedFile{}, err
	}
	after, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return inspectedFile{}, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = f.Close()
		return inspectedFile{}, fmt.Errorf("%q changed or is not a regular file", name)
	}
	return inspectedFile{file: f, info: after}, nil
}

func addInput(zw *zip.Writer, input bundleInput) error {
	source, err := inspectRegularFile(input.diskPath)
	if err != nil {
		return err
	}
	defer source.file.Close()
	if source.info.Size() != input.size {
		return fmt.Errorf("size changed from %d to %d", input.size, source.info.Size())
	}
	header := &zip.FileHeader{
		Name:     input.archiveName,
		Method:   zip.Deflate,
		Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	header.SetMode(0o644)
	destination, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	h := sha256.New()
	written, err := copyStream(io.MultiWriter(destination, h), source.file)
	if err != nil {
		return err
	}
	if written != input.size {
		return fmt.Errorf("read %d bytes, expected %d", written, input.size)
	}
	if input.sha256 != "" && fmt.Sprintf("%x", h.Sum(nil)) != input.sha256 {
		return errors.New("SHA-256 differs from metadata")
	}
	after, err := source.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(source.info, after) || after.Size() != input.size {
		return errors.New("source file changed while packing")
	}
	return nil
}

func copyStub(dst *os.File, stub inspectedFile) error {
	written, err := copyStream(dst, stub.file)
	if err != nil {
		return err
	}
	if written != stub.info.Size() {
		return fmt.Errorf("read %d bytes, expected %d", written, stub.info.Size())
	}
	after, err := stub.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(stub.info, after) || after.Size() != stub.info.Size() {
		return errors.New("stub changed while packing")
	}
	return nil
}

func copyStream(dst io.Writer, src io.Reader) (int64, error) {
	return io.CopyBuffer(dst, src, make([]byte, copyBufferBytes))
}

func hashSection(f *os.File, offset, size int64) ([32]byte, error) {
	var digest [32]byte
	if offset < 0 || size < 0 {
		return digest, errors.New("negative archive section")
	}
	h := sha256.New()
	section := io.NewSectionReader(f, offset, size)
	if _, err := io.CopyBuffer(h, section, make([]byte, copyBufferBytes)); err != nil {
		return digest, err
	}
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func preflightOutput(stubPath string, stubInfo os.FileInfo, sourceRoot, outPath string) error {
	if _, err := os.Lstat(outPath); err == nil {
		return fmt.Errorf("terrainbundle: output %q already exists", outPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("terrainbundle: inspect output: %w", err)
	}
	outResolved, err := resolveForComparison(outPath, false)
	if err != nil {
		return fmt.Errorf("terrainbundle: output path: %w", err)
	}
	stubResolved, err := filepath.EvalSymlinks(stubPath)
	if err != nil {
		return fmt.Errorf("terrainbundle: resolve stub: %w", err)
	}
	if samePath(outResolved, stubResolved) {
		return errors.New("terrainbundle: output must not be the stub")
	}
	if !stubInfo.Mode().IsRegular() {
		return errors.New("terrainbundle: stub is not a regular file")
	}
	rootResolved, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return fmt.Errorf("terrainbundle: resolve source root: %w", err)
	}
	if pathWithin(outResolved, rootResolved) {
		return errors.New("terrainbundle: output must be outside the source tree")
	}
	return nil
}

func createOutputTemp(outPath string) (*os.File, string, error) {
	dir := filepath.Dir(outPath)
	info, err := os.Stat(dir)
	if err != nil {
		return nil, "", fmt.Errorf("terrainbundle: output directory: %w", err)
	}
	if !info.IsDir() {
		return nil, "", errors.New("terrainbundle: output parent is not a directory")
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(outPath)+".terrain-pack-*")
	if err != nil {
		return nil, "", fmt.Errorf("terrainbundle: create output temporary file: %w", err)
	}
	return f, f.Name(), nil
}

func validateDestination(destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("terrainbundle: destination %q already exists", destination)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("terrainbundle: inspect destination: %w", err)
	}
	info, err := os.Stat(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("terrainbundle: destination directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("terrainbundle: destination parent is not a directory")
	}
	return nil
}

func createExtractTemp(destination string) (string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".terrain-extract-*")
	if err != nil {
		return "", fmt.Errorf("terrainbundle: create extraction temporary directory: %w", err)
	}
	return dir, nil
}

func resolveForComparison(name string, mustExist bool) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if mustExist {
		return filepath.EvalSymlinks(abs)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func pathWithin(candidate, root string) bool {
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type archivePath struct {
	file     *zip.File
	isDir    bool
	fileName string
}

func validateArchiveEntries(entries []*zip.File) ([]archivePath, error) {
	if len(entries) > maxEntries {
		return nil, fmt.Errorf("terrainbundle: archive has %d entries; maximum is %d", len(entries), maxEntries)
	}
	seen := make(map[string]string, len(entries))
	result := make([]archivePath, 0, len(entries))
	var declaredTotal uint64
	for _, entry := range entries {
		name := entry.Name
		if !utf8.ValidString(name) {
			return nil, fmt.Errorf("terrainbundle: archive entry name is not valid UTF-8")
		}
		if strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") || path.IsAbs(name) || strings.ContainsRune(name, 0) {
			return nil, fmt.Errorf("terrainbundle: unsafe archive entry %q", name)
		}
		clean := path.Clean(name)
		if clean == "." || clean != strings.TrimSuffix(name, "/") || strings.HasPrefix(clean, "../") || clean == ".." {
			return nil, fmt.Errorf("terrainbundle: unsafe archive entry %q", name)
		}
		for _, component := range strings.Split(clean, "/") {
			if component == "" || component == "." || component == ".." {
				return nil, fmt.Errorf("terrainbundle: unsafe archive entry %q", name)
			}
		}
		key := strings.ToLower(clean)
		if previous, ok := seen[key]; ok {
			return nil, fmt.Errorf("terrainbundle: duplicate archive entries %q and %q", previous, name)
		}
		seen[key] = name
		parent := clean
		for {
			index := strings.LastIndexByte(parent, '/')
			if index < 0 {
				break
			}
			parent = parent[:index]
			if previous, ok := seen[strings.ToLower(parent)]; ok {
				return nil, fmt.Errorf("terrainbundle: archive entry %q conflicts with %q", name, previous)
			}
		}

		isDir := strings.HasSuffix(name, "/")
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("terrainbundle: symbolic link archive entry %q is not allowed", name)
		}
		if mode&os.ModeType != 0 && !mode.IsDir() && !mode.IsRegular() {
			return nil, fmt.Errorf("terrainbundle: special archive entry %q is not allowed", name)
		}
		if isDir && entry.UncompressedSize64 != 0 {
			return nil, fmt.Errorf("terrainbundle: directory entry %q has file data", name)
		}
		if !isDir {
			if entry.UncompressedSize64 > maxFileBytes {
				return nil, fmt.Errorf("terrainbundle: archive entry %q exceeds %d bytes", name, maxFileBytes)
			}
			if declaredTotal > maxTotalBytes-entry.UncompressedSize64 {
				return nil, fmt.Errorf("terrainbundle: archive expands beyond %d bytes", maxTotalBytes)
			}
			declaredTotal += entry.UncompressedSize64
		}
		result = append(result, archivePath{file: entry, isDir: isDir, fileName: clean})
	}
	return result, nil
}

func extractFiles(entries []*zip.File, validated []archivePath, root string) error {
	var total uint64
	byFile := make(map[*zip.File]archivePath, len(validated))
	for _, item := range validated {
		byFile[item.file] = item
	}
	for _, entry := range entries {
		item := byFile[entry]
		target := filepath.Join(root, filepath.FromSlash(item.fileName))
		if !pathWithin(target, root) {
			return fmt.Errorf("terrainbundle: archive entry %q escapes destination", entry.Name)
		}
		if item.isDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("terrainbundle: create directory %q: %w", entry.Name, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("terrainbundle: create parent for %q: %w", entry.Name, err)
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("terrainbundle: create %q: %w", entry.Name, err)
		}
		rc, err := entry.Open()
		if err != nil {
			_ = out.Close()
			return fmt.Errorf("terrainbundle: open %q: %w", entry.Name, err)
		}
		allowed := uint64(maxFileBytes)
		if remaining := uint64(maxTotalBytes) - total; remaining < allowed {
			allowed = remaining
		}
		written, copyErr := io.CopyBuffer(out, io.LimitReader(rc, int64(allowed)+1), make([]byte, copyBufferBytes))
		closeReadErr := rc.Close()
		closeWriteErr := out.Close()
		if copyErr != nil {
			return fmt.Errorf("terrainbundle: extract %q: %w", entry.Name, copyErr)
		}
		if closeReadErr != nil {
			return fmt.Errorf("terrainbundle: close ZIP entry %q: %w", entry.Name, closeReadErr)
		}
		if closeWriteErr != nil {
			return fmt.Errorf("terrainbundle: close output %q: %w", entry.Name, closeWriteErr)
		}
		if uint64(written) != entry.UncompressedSize64 {
			return fmt.Errorf("terrainbundle: %q size is %d bytes, archive declares %d", entry.Name, written, entry.UncompressedSize64)
		}
		if uint64(written) > allowed {
			return fmt.Errorf("terrainbundle: %q exceeds archive extraction limits", entry.Name)
		}
		total += uint64(written)
	}
	return nil
}
