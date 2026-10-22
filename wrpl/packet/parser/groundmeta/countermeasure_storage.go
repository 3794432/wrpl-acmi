package groundmeta

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
)

const (
	maxStorageWeapons           = 6
	maxStorageWeaponStringBytes = 255
	maxStorageUint32Bytes       = 5
)

var ErrStorageWeaponsMissing = errors.New("unit storage has no field 0x33")

// StorageWeapon is the compact weapon record stored in unit storage field
// 0x33. Count is the ammunition count in that record.
type StorageWeapon struct {
	Launcher string
	Bullet   string
	Count    uint16
}

// UnitStorageWeapons reads the storage weapon records from field 0x33 of an
// IdFieldSerializer255 unit_storage__aircraft payload. The reference
// BitStream::Read(BitStream&) stores a compressed uint32 bit count, aligns to
// the next byte, and copies ceil(bitCount/8) bytes. The copied stream is then
// read to its declared bit boundary.
func UnitStorageWeapons(raw []byte) ([]StorageWeapon, error) {
	layout, err := readStorageLayout(raw)
	if err != nil {
		return nil, err
	}

	start := 32
	found := false
	var weapons []StorageWeapon
	for i := 0; i < layout.fields; i++ {
		idx := layout.indexes[i]
		size := int(layout.sizes[i])
		if idx == 0x33 {
			if found {
				return nil, fmt.Errorf("%w: duplicate field 0x33", ErrStorageLayout)
			}
			found = true
			weapons, err = readStorageWeaponsField(raw, start, size)
			if err != nil {
				return nil, err
			}
		}
		start += size
	}
	if !found {
		return nil, ErrStorageWeaponsMissing
	}
	return weapons, nil
}

func readStorageWeaponsField(raw []byte, start, size int) ([]StorageWeapon, error) {
	if size <= 0 {
		return nil, fmt.Errorf("%w: field 0x33 is empty", ErrStorageLayout)
	}
	end := start + size
	if start < 0 || end < start || end > len(raw)*8 {
		return nil, fmt.Errorf("%w: field 0x33 bounds %d..%d outside %d bits", ErrStorageLayout, start, end, len(raw)*8)
	}

	r := danet.NewBitReader(raw)
	r.BitOffset = start
	innerBits, err := readStorageCompressedUint32(r, end, "field 0x33 bit count")
	if err != nil {
		return nil, err
	}
	r.AlignToByteBoundary()
	if r.BitOffset > end {
		return nil, fmt.Errorf("%w: field 0x33 bit count alignment exceeds field", ErrStorageLayout)
	}
	if uint64(innerBits) > uint64(end-r.BitOffset) {
		return nil, fmt.Errorf("%w: field 0x33 bit count %d exceeds %d available bits", ErrStorageLayout, innerBits, end-r.BitOffset)
	}
	byteCount := (int(innerBits) + 7) / 8
	copyStart := r.BitOffset / 8
	if byteCount < 0 || copyStart > len(raw) || byteCount > len(raw)-copyStart {
		return nil, fmt.Errorf("%w: field 0x33 copied stream exceeds storage", ErrStorageLayout)
	}
	payload := raw[copyStart : copyStart+byteCount]
	r.BitOffset += byteCount * 8
	if r.BitOffset != end {
		return nil, fmt.Errorf("%w: field 0x33 consumed %d of %d bits", ErrStorageLayout, r.BitOffset-start, size)
	}
	if err := validateStorageBitPadding(payload, innerBits); err != nil {
		return nil, fmt.Errorf("%w: field 0x33: %v", ErrStorageLayout, err)
	}

	weapons, exactErr := decodeStorageWeapons(payload, innerBits, false)
	if exactErr == nil {
		return weapons, nil
	}
	// Current replay storage inserts one zero byte after each weapon's uint16
	// count. Accept that exact layout as a compatibility form; all other bytes,
	// including the list terminators, must still be consumed exactly.
	weapons, paddedErr := decodeStorageWeapons(payload, innerBits, true)
	if paddedErr == nil {
		return weapons, nil
	}
	return nil, fmt.Errorf("%w: field 0x33: %v (zero-padded records: %v)", ErrStorageLayout, exactErr, paddedErr)
}

func decodeStorageWeapons(payload []byte, innerBits uint32, paddedRecords bool) ([]StorageWeapon, error) {
	if innerBits > uint32(len(payload)*8) {
		return nil, fmt.Errorf("stream has %d bits, storage has %d", innerBits, len(payload)*8)
	}
	c := storageWeaponCursor{r: danet.NewBitReader(payload), end: int(innerBits)}
	count, err := c.readByte("weapon count")
	if err != nil {
		return nil, err
	}
	if count > maxStorageWeapons {
		return nil, fmt.Errorf("weapon count %d exceeds %d", count, maxStorageWeapons)
	}

	weapons := make([]StorageWeapon, 0, int(count))
	for i := 0; i < int(count); i++ {
		launcher, readErr := c.readString(fmt.Sprintf("weapon %d launcher", i))
		if readErr != nil {
			return nil, readErr
		}
		bullet, readErr := c.readString(fmt.Sprintf("weapon %d bullet", i))
		if readErr != nil {
			return nil, readErr
		}
		n, readErr := c.readU16(fmt.Sprintf("weapon %d count", i))
		if readErr != nil {
			return nil, readErr
		}
		if paddedRecords {
			pad, padErr := c.readByte(fmt.Sprintf("weapon %d zero pad", i))
			if padErr != nil {
				return nil, padErr
			}
			if pad != 0 {
				return nil, fmt.Errorf("weapon %d pad is %#x, want zero", i, pad)
			}
		}
		weapons = append(weapons, StorageWeapon{Launcher: launcher, Bullet: bullet, Count: n})
	}

	if err = readStorageWeaponStringList(&c, "weapon mods"); err != nil {
		return nil, err
	}
	if err = readStorageWeaponStringList(&c, "flight-model mods"); err != nil {
		return nil, err
	}
	if c.r.BitOffset != c.end {
		return nil, fmt.Errorf("stream consumed %d of %d bits", c.r.BitOffset, c.end)
	}
	return weapons, nil
}

func readStorageWeaponStringList(c *storageWeaponCursor, label string) error {
	count, err := c.readByte(label + " count")
	if err != nil {
		return err
	}
	for i := 0; i < int(count); i++ {
		if _, err = c.readString(fmt.Sprintf("%s %d", label, i)); err != nil {
			return err
		}
	}
	return nil
}

type storageWeaponCursor struct {
	r   *danet.BitReader
	end int
}

func (c *storageWeaponCursor) readByte(label string) (byte, error) {
	if c.r.BitOffset < 0 || c.r.BitOffset+8 > c.end {
		return 0, fmt.Errorf("reading %s: need 8 bits, have %d", label, c.end-c.r.BitOffset)
	}
	b, err := c.r.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", label, err)
	}
	return b, nil
}

func (c *storageWeaponCursor) readU16(label string) (uint16, error) {
	if c.r.BitOffset < 0 || c.r.BitOffset+16 > c.end {
		return 0, fmt.Errorf("reading %s: need 16 bits, have %d", label, c.end-c.r.BitOffset)
	}
	buf := [2]byte{}
	if _, err := c.r.Read(buf[:]); err != nil {
		return 0, fmt.Errorf("reading %s: %w", label, err)
	}
	return binary.LittleEndian.Uint16(buf[:]), nil
}

func (c *storageWeaponCursor) readString(label string) (string, error) {
	n, err := readStorageCompressedUint32(c.r, c.end, label+" length")
	if err != nil {
		return "", err
	}
	if n > maxStorageWeaponStringBytes {
		return "", fmt.Errorf("%s length %d exceeds %d", label, n, maxStorageWeaponStringBytes)
	}
	if uint64(n)*8 > uint64(c.end-c.r.BitOffset) {
		return "", fmt.Errorf("%s value needs %d bits, have %d", label, uint64(n)*8, c.end-c.r.BitOffset)
	}
	buf := make([]byte, int(n))
	if n > 0 {
		if _, err = c.r.Read(buf); err != nil {
			return "", fmt.Errorf("reading %s value: %w", label, err)
		}
	}
	for i, b := range buf {
		if b < 0x20 || b > 0x7e {
			return "", fmt.Errorf("%s byte %d is not printable ASCII (%#x)", label, i, b)
		}
	}
	return string(buf), nil
}

func readStorageCompressedUint32(r *danet.BitReader, end int, label string) (uint32, error) {
	var value uint64
	for i := 0; i < maxStorageUint32Bytes; i++ {
		if r.BitOffset < 0 || r.BitOffset+8 > end {
			return 0, fmt.Errorf("%w: reading %s: need 8 bits, have %d", ErrStorageLayout, label, end-r.BitOffset)
		}
		b, err := r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("%w: reading %s: %v", ErrStorageLayout, label, err)
		}
		if i == maxStorageUint32Bytes-1 && (b&0x80 != 0 || b&0x70 != 0) {
			return 0, fmt.Errorf("%w: %s exceeds uint32", ErrStorageLayout, label)
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if value > uint64(^uint32(0)) {
				return 0, fmt.Errorf("%w: %s exceeds uint32", ErrStorageLayout, label)
			}
			return uint32(value), nil
		}
	}
	return 0, fmt.Errorf("%w: %s has unterminated varint", ErrStorageLayout, label)
}

func validateStorageBitPadding(payload []byte, bitCount uint32) error {
	if bitCount > uint32(len(payload)*8) {
		return fmt.Errorf("declared %d bits exceed %d copied bits", bitCount, len(payload)*8)
	}
	remainder := bitCount & 7
	if remainder == 0 || len(payload) == 0 {
		return nil
	}
	mask := byte((1 << (8 - remainder)) - 1)
	if payload[len(payload)-1]&mask != 0 {
		return fmt.Errorf("nonzero padding after bit %d", bitCount)
	}
	return nil
}
