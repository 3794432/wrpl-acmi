package groundmeta

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/idfieldserializer"
)

var (
	ErrUnitNameMissing = errors.New("unit storage has no field 0x6")
	ErrStorageLayout   = errors.New("invalid unit storage layout")
)

const maxUnitStorageBytes = 1 << 20

// UnitStorageUnitName decodes only the unit_name field (0x6) from the
// IdFieldSerializer255 payload carried by unit_storage__* components. The
// field mapping is explicit in the reference Unit.cpp::LoadFromStorage.
func UnitStorageUnitName(raw []byte) (string, error) {
	layout, err := readStorageLayout(raw)
	if err != nil {
		return "", err
	}
	start := 32
	found := false
	name := ""
	for i := 0; i < layout.fields; i++ {
		idx := layout.indexes[i]
		size := int(layout.sizes[i])
		if idx != 0x6 {
			start += size
			continue
		}
		if found {
			return "", fmt.Errorf("%w: duplicate field 0x6", ErrStorageLayout)
		}
		found = true
		if size < 8 || size%8 != 0 {
			return "", fmt.Errorf("%w: field 0x6 size %d is not a byte string", ErrStorageLayout, size)
		}
		r := danet.NewBitReader(raw)
		r.BitOffset = start
		n, readErr := r.ReadByte()
		if readErr != nil {
			return "", fmt.Errorf("%w: reading field 0x6 length: %v", ErrStorageLayout, readErr)
		}
		if int(n)+1 > size/8 {
			return "", fmt.Errorf("%w: field 0x6 length %d exceeds field size %d", ErrStorageLayout, n, size)
		}
		buf := make([]byte, int(n))
		if n > 0 {
			if _, readErr = r.Read(buf); readErr != nil {
				return "", fmt.Errorf("%w: reading field 0x6 value: %v", ErrStorageLayout, readErr)
			}
		}
		if strings.IndexByte(string(buf), 0) >= 0 {
			return "", fmt.Errorf("%w: field 0x6 contains NUL", ErrStorageLayout)
		}
		name = string(buf)
		start += size
	}
	if !found {
		return "", ErrUnitNameMissing
	}
	return name, nil
}

type storageLayout struct {
	fields  int
	sizes   []uint32
	indexes []uint16
}

func readStorageLayout(raw []byte) (storageLayout, error) {
	if len(raw) > maxUnitStorageBytes {
		return storageLayout{}, fmt.Errorf("%w: storage has %d bytes, limit %d", ErrStorageLayout, len(raw), maxUnitStorageBytes)
	}
	if len(raw) < 4 {
		return storageLayout{}, fmt.Errorf("%w: need 4 header bytes, have %d", ErrStorageLayout, len(raw))
	}
	r := danet.NewBitReader(raw)
	offset, err := r.ReadU16LE()
	if err != nil {
		return storageLayout{}, fmt.Errorf("%w: offset: %v", ErrStorageLayout, err)
	}
	count, err := r.ReadU16LE()
	if err != nil {
		return storageLayout{}, fmt.Errorf("%w: count: %v", ErrStorageLayout, err)
	}
	fields := int(count & 0x0fff)
	bitsPerID := int(count >> 12)
	if fields == 0 || fields >= 255 {
		return storageLayout{}, fmt.Errorf("%w: field count %d", ErrStorageLayout, fields)
	}
	if bitsPerID == 0 || bitsPerID > 16 {
		return storageLayout{}, fmt.Errorf("%w: id width %d", ErrStorageLayout, bitsPerID)
	}
	offsetBytes := int(offset)
	if offsetBytes < 4 || offsetBytes*8 > len(raw)*8 {
		return storageLayout{}, fmt.Errorf("%w: size table offset %d outside %d bytes", ErrStorageLayout, offsetBytes, len(raw))
	}
	bitsForIndices := (bitsPerID*fields + 7) &^ 7
	indexStart := offsetBytes*8 - bitsForIndices
	if indexStart < 32 {
		return storageLayout{}, fmt.Errorf("%w: index table starts before field data", ErrStorageLayout)
	}
	if offsetBytes*8 < indexStart {
		return storageLayout{}, fmt.Errorf("%w: size table overlaps index table", ErrStorageLayout)
	}
	dataBits := indexStart - 32

	r.BitOffset = offsetBytes * 8
	sizes := make([]uint32, fields)
	total := 0
	for i := 0; i < fields; i++ {
		size, readErr := idfieldserializer.ReadSize(r)
		if readErr != nil {
			return storageLayout{}, fmt.Errorf("%w: size %d/%d: %v", ErrStorageLayout, i, fields, readErr)
		}
		if uint64(size) > uint64(dataBits-total) {
			return storageLayout{}, fmt.Errorf("%w: field data exceeds %d bits", ErrStorageLayout, dataBits)
		}
		sizes[i] = size
		total += int(size)
	}
	r.BitOffset = indexStart
	indexes := make([]uint16, fields)
	for i := 0; i < fields; i++ {
		b, readErr := r.ReadBits(bitsPerID)
		if readErr != nil {
			return storageLayout{}, fmt.Errorf("%w: index %d/%d: %v", ErrStorageLayout, i, fields, readErr)
		}
		var pad [2]byte
		copy(pad[:], b)
		indexes[i] = binary.LittleEndian.Uint16(pad[:])
	}
	return storageLayout{fields: fields, sizes: sizes, indexes: indexes}, nil
}
