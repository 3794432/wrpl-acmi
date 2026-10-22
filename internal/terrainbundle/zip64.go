package terrainbundle

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	zip64EndSignature     = uint32(0x06064b50)
	zip64LocatorSignature = uint32(0x07064b50)
	zipEndSignature       = uint32(0x06054b50)
	zip64TrailerBytes     = 76 // ZIP64 EOCD (56) plus locator (20)
)

// precheckArchiveEntryCount rejects an excessive central directory before
// archive/zip allocates one File value per entry.
func precheckArchiveEntryCount(r io.ReaderAt, size int64) error {
	if size < 22 {
		return nil
	}
	tailSize := size
	if tailSize > 22+0xffff {
		tailSize = 22 + 0xffff
	}
	tail := make([]byte, tailSize)
	if _, err := r.ReadAt(tail, size-tailSize); err != nil {
		return fmt.Errorf("terrainbundle: read ZIP directory tail: %w", err)
	}
	eocd := -1
	for index := len(tail) - 22; index >= 0; index-- {
		if binary.LittleEndian.Uint32(tail[index:index+4]) != zipEndSignature {
			continue
		}
		commentLength := int(binary.LittleEndian.Uint16(tail[index+20 : index+22]))
		if index+22+commentLength == len(tail) {
			eocd = index
			break
		}
	}
	if eocd < 0 {
		return nil // Let archive/zip report the malformed end record.
	}
	entries := binary.LittleEndian.Uint16(tail[eocd+10 : eocd+12])
	if entries != 0xffff {
		if int(entries) > maxEntries {
			return fmt.Errorf("terrainbundle: archive has %d entries; maximum is %d", entries, maxEntries)
		}
		return nil
	}

	if eocd < 20 || binary.LittleEndian.Uint32(tail[eocd-20:eocd-16]) != zip64LocatorSignature {
		return nil // archive/zip will diagnose the missing ZIP64 locator.
	}
	zip64Offset := binary.LittleEndian.Uint64(tail[eocd-12 : eocd-4])
	if zip64Offset > uint64(size-56) {
		return errors.New("terrainbundle: ZIP64 end record offset is out of bounds")
	}
	record := make([]byte, 56)
	if _, err := r.ReadAt(record, int64(zip64Offset)); err != nil {
		return fmt.Errorf("terrainbundle: read ZIP64 end record: %w", err)
	}
	if !bytes.Equal(record[:4], []byte{'P', 'K', 6, 6}) {
		return nil // archive/zip will diagnose the missing ZIP64 record.
	}
	entryCount := binary.LittleEndian.Uint64(record[32:40])
	if entryCount > maxEntries {
		return fmt.Errorf("terrainbundle: archive has %d entries; maximum is %d", entryCount, maxEntries)
	}
	return nil
}

// ensureZIP64Trailer upgrades archive/zip's ordinary central-directory tail
// to an explicit ZIP64 end record and locator. archive/zip already emits this
// form once offsets exceed 32 bits, but always emitting it keeps the container
// contract stable for small test fixtures as well.
func ensureZIP64Trailer(f *os.File, archiveOffset, archiveEnd int64) (int64, error) {
	if archiveEnd-archiveOffset < 22 {
		return archiveEnd, errors.New("terrainbundle: ZIP archive is too short for an end record")
	}
	endBytes := make([]byte, 22)
	if _, err := f.ReadAt(endBytes, archiveEnd-22); err != nil {
		return archiveEnd, fmt.Errorf("terrainbundle: read ZIP end record: %w", err)
	}
	if binary.LittleEndian.Uint32(endBytes[0:4]) != zipEndSignature || binary.LittleEndian.Uint16(endBytes[20:22]) != 0 {
		return archiveEnd, errors.New("terrainbundle: unexpected ZIP end record")
	}
	entries := binary.LittleEndian.Uint16(endBytes[10:12])
	centralSize := binary.LittleEndian.Uint32(endBytes[12:16])
	centralOffset := binary.LittleEndian.Uint32(endBytes[16:20])
	if entries == 0xffff && centralSize == 0xffffffff && centralOffset == 0xffffffff {
		return archiveEnd, nil
	}
	if entries == 0xffff || centralSize == 0xffffffff || centralOffset == 0xffffffff {
		return archiveEnd, errors.New("terrainbundle: inconsistent ZIP64 end record")
	}

	eocdOffset := archiveEnd - 22
	var trailer [zip64TrailerBytes + 22]byte
	b := trailer[:]
	binary.LittleEndian.PutUint32(b[0:4], zip64EndSignature)
	binary.LittleEndian.PutUint64(b[4:12], 44)
	binary.LittleEndian.PutUint16(b[12:14], 45) // ZIP 4.5
	binary.LittleEndian.PutUint16(b[14:16], 45)
	binary.LittleEndian.PutUint32(b[16:20], 0)
	binary.LittleEndian.PutUint32(b[20:24], 0)
	binary.LittleEndian.PutUint64(b[24:32], uint64(entries))
	binary.LittleEndian.PutUint64(b[32:40], uint64(entries))
	binary.LittleEndian.PutUint64(b[40:48], uint64(centralSize))
	binary.LittleEndian.PutUint64(b[48:56], uint64(centralOffset))
	binary.LittleEndian.PutUint32(b[56:60], zip64LocatorSignature)
	binary.LittleEndian.PutUint32(b[60:64], 0)
	binary.LittleEndian.PutUint64(b[64:72], uint64(eocdOffset-archiveOffset))
	binary.LittleEndian.PutUint32(b[72:76], 1)

	normal := b[76:]
	binary.LittleEndian.PutUint32(normal[0:4], zipEndSignature)
	binary.LittleEndian.PutUint16(normal[4:6], 0)
	binary.LittleEndian.PutUint16(normal[6:8], 0)
	binary.LittleEndian.PutUint16(normal[8:10], 0xffff)
	binary.LittleEndian.PutUint16(normal[10:12], 0xffff)
	binary.LittleEndian.PutUint32(normal[12:16], 0xffffffff)
	binary.LittleEndian.PutUint32(normal[16:20], 0xffffffff)
	binary.LittleEndian.PutUint16(normal[20:22], 0)

	if _, err := f.Seek(eocdOffset, io.SeekStart); err != nil {
		return archiveEnd, fmt.Errorf("terrainbundle: seek ZIP end record: %w", err)
	}
	if _, err := f.Write(trailer[:]); err != nil {
		return archiveEnd, fmt.Errorf("terrainbundle: write ZIP64 end record: %w", err)
	}
	newEnd := archiveEnd + zip64TrailerBytes
	if err := f.Truncate(newEnd); err != nil {
		return archiveEnd, fmt.Errorf("terrainbundle: truncate ZIP trailer: %w", err)
	}
	return newEnd, nil
}
