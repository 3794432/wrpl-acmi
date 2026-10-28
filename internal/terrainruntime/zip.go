package terrainruntime

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Preflight the actual directory records before archive/zip can allocate File
// values. An EOCD count alone is insufficient: malicious ZIPs can understate it.
// ZIP64/multidisk archives are unnecessary under our 1 GiB/10000-file limits.
func checkZIPDirectory(r io.ReaderAt, size int64) (int64, error) {
	bad := errors.New("terrainruntime: malformed or excessive ZIP directory")
	if size < 22 || size > maxArchiveBytes {
		return 0, bad
	}
	var end [22]byte
	if _, err := r.ReadAt(end[:], size-22); err != nil {
		return 0, err
	}
	if binary.LittleEndian.Uint32(end[:4]) != 0x06054b50 || binary.LittleEndian.Uint16(end[20:]) != 0 || binary.LittleEndian.Uint32(end[4:8]) != 0 {
		return 0, bad // Canonical ZIPs have no comment, disk split, or trailing data.
	}
	count := int(binary.LittleEndian.Uint16(end[10:12]))
	if count < 1 || count > maxFiles || binary.LittleEndian.Uint16(end[8:10]) != uint16(count) {
		return 0, bad
	}
	length := int64(binary.LittleEndian.Uint32(end[12:16]))
	start := int64(binary.LittleEndian.Uint32(end[16:20]))
	if length > 16<<20 || start+length != size-22 {
		return 0, bad
	}
	pos := start
	for range count {
		var header [46]byte
		if pos+46 > size-22 {
			return 0, bad
		}
		if _, err := r.ReadAt(header[:], pos); err != nil {
			return 0, err
		}
		if binary.LittleEndian.Uint32(header[:4]) != 0x02014b50 || binary.LittleEndian.Uint16(header[34:36]) != 0 {
			return 0, bad
		}
		nameLen := int64(binary.LittleEndian.Uint16(header[28:30]))
		if nameLen == 0 || nameLen > 1024 {
			return 0, bad
		}
		pos += 46 + nameLen + int64(binary.LittleEndian.Uint16(header[30:32])) + int64(binary.LittleEndian.Uint16(header[32:34]))
		if pos > size-22 {
			return 0, bad
		}
	}
	if pos != size-22 {
		return 0, bad
	}
	return start, nil
}

func archiveEntries(files []*zip.File, central int64) (map[string]*zip.File, error) {
	if len(files) == 0 || len(files) > maxFiles {
		return nil, errors.New("terrainruntime: too many or no ZIP entries")
	}
	paths := newPathSet()
	entries := make(map[string]*zip.File, len(files))
	var total uint64
	for _, f := range files {
		if err := paths.add(f.Name); err != nil {
			return nil, err
		}
		if !f.Mode().IsRegular() || f.ExternalAttrs&0x400 != 0 || f.Flags&1 != 0 || (f.Method != zip.Store && f.Method != zip.Deflate) {
			return nil, fmt.Errorf("terrainruntime: nonregular/encrypted/unsupported ZIP entry %q", f.Name)
		}
		if f.UncompressedSize64 > maxFileBytes || f.UncompressedSize64 > maxTotalBytes-total {
			return nil, fmt.Errorf("terrainruntime: ZIP entry %q exceeds size limits", f.Name)
		}
		total += f.UncompressedSize64
		offset, err := f.DataOffset()
		if err != nil {
			return nil, err
		}
		if offset < 0 || offset > central || f.CompressedSize64 > uint64(central-offset) {
			return nil, errors.New("terrainruntime: ZIP file data outside archive body")
		}
		entries[f.Name] = f
	}
	return entries, nil
}
