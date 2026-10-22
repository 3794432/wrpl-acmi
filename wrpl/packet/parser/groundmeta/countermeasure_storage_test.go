package groundmeta

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

type storageTestField struct {
	id   uint16
	body []byte
}

type storageTestBitWriter struct {
	data []byte
	bits int
}

func (w *storageTestBitWriter) writeBits(v uint64, count int) {
	for i := count - 1; i >= 0; i-- {
		byteIndex := w.bits / 8
		if byteIndex == len(w.data) {
			w.data = append(w.data, 0)
		}
		if v&(uint64(1)<<i) != 0 {
			w.data[byteIndex] |= 0x80 >> (w.bits & 7)
		}
		w.bits++
	}
}

func (w *storageTestBitWriter) writeBytes(buf []byte) {
	for _, b := range buf {
		w.writeBits(uint64(b), 8)
	}
}

func (w *storageTestBitWriter) writeCompressed(v uint32) {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		w.writeBits(uint64(b), 8)
		if v == 0 {
			return
		}
	}
}

func (w *storageTestBitWriter) align() {
	for w.bits&7 != 0 {
		w.writeBits(0, 1)
	}
}

func buildStorageRecord(fields ...storageTestField) []byte {
	return buildStorageRecordWithSizes(fields, nil)
}

func buildStorageRecordWithSizes(fields []storageTestField, declaredSizes []uint32) []byte {
	const bitsPerID = 7
	body := make([]byte, 0)
	for _, field := range fields {
		body = append(body, field.body...)
	}
	bitsForIndices := (bitsPerID*len(fields) + 7) &^ 7
	offset := 4 + len(body) + bitsForIndices/8
	raw := make([]byte, 4, offset+8)
	binary.LittleEndian.PutUint16(raw[0:2], uint16(offset))
	binary.LittleEndian.PutUint16(raw[2:4], uint16(len(fields)|(bitsPerID<<12)))
	raw = append(raw, body...)
	indexes := &storageTestBitWriter{}
	for _, field := range fields {
		indexes.writeBits(uint64(field.id), bitsPerID)
	}
	indexes.align()
	raw = append(raw, indexes.data...)

	sizes := &storageTestBitWriter{}
	standard := map[uint32]uint8{1: 1, 8: 2, 16: 3, 32: 4, 64: 5, 96: 6, 128: 7}
	for i, field := range fields {
		size := uint32(len(field.body) * 8)
		if declaredSizes != nil {
			size = declaredSizes[i]
		}
		if header := standard[size]; header != 0 {
			sizes.writeBits(uint64(header), 3)
		} else {
			sizes.writeBits(0, 3)
			sizes.writeCompressed(size)
		}
	}
	sizes.align()
	return append(raw, sizes.data...)
}

func weaponPayload(weapons []StorageWeapon, mods, fmMods []string, paddedRecords bool) []byte {
	w := &storageTestBitWriter{}
	w.writeBits(uint64(len(weapons)), 8)
	for _, weapon := range weapons {
		w.writeBits(uint64(len(weapon.Launcher)), 8)
		w.writeBytes([]byte(weapon.Launcher))
		w.writeBits(uint64(len(weapon.Bullet)), 8)
		w.writeBytes([]byte(weapon.Bullet))
		var count [2]byte
		binary.LittleEndian.PutUint16(count[:], weapon.Count)
		w.writeBytes(count[:])
		if paddedRecords {
			w.writeBits(0, 8)
		}
	}
	w.writeBits(uint64(len(mods)), 8)
	for _, mod := range mods {
		w.writeBits(uint64(len(mod)), 8)
		w.writeBytes([]byte(mod))
	}
	w.writeBits(uint64(len(fmMods)), 8)
	for _, mod := range fmMods {
		w.writeBits(uint64(len(mod)), 8)
		w.writeBytes([]byte(mod))
	}
	return w.data
}

func packWeaponField(payload []byte) []byte {
	return packWeaponFieldBits(payload, uint32(len(payload)*8))
}

func packWeaponFieldBits(payload []byte, innerBits uint32) []byte {
	w := &storageTestBitWriter{}
	w.writeCompressed(innerBits)
	w.align()
	w.writeBytes(payload)
	return w.data
}

func TestUnitStorageWeaponsKnownRecord(t *testing.T) {
	want := []StorageWeapon{
		{Launcher: "countermeasure_split_launcher_jet", Bullet: "", Count: 30},
		{Launcher: "countermeasure_split_launcher_jet", Bullet: "countermeasures_launcher_chaff", Count: 60},
		{Launcher: "cannonM61A1", Bullet: "M60_air_targets", Count: 512},
	}
	payload := weaponPayload(want, []string{"new_cover", "M60_belt_pack"}, []string{"new_compressor_jet"}, false)
	raw := buildStorageRecord(storageTestField{id: 0x33, body: packWeaponField(payload)})
	got, err := UnitStorageWeapons(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("weapons = %#v, want %#v", got, want)
	}
}

func TestUnitStorageWeaponsEvidenceShape(t *testing.T) {
	want := []StorageWeapon{
		{Launcher: "countermeasure_large_split_launcher_jet", Bullet: "", Count: 25},
		{Launcher: "countermeasure_large_split_launcher_jet", Bullet: "countermeasures_launcher_chaff_large", Count: 23},
		{Launcher: "cannonGSh_301", Bullet: "", Count: 150},
	}
	payload := weaponPayload(want, []string{"countermeasures_belt_pack"}, []string{"new_compressor_jet"}, true)
	raw := buildStorageRecord(storageTestField{id: 0x33, body: packWeaponField(payload)})
	got, err := UnitStorageWeapons(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("weapons = %#v, want %#v", got, want)
	}
}

func TestUnitStorageWeaponsRejectsMalformedFields(t *testing.T) {
	validPayload := weaponPayload([]StorageWeapon{{Launcher: "launcher", Bullet: "bullet", Count: 2}}, nil, nil, false)
	validBody := packWeaponField(validPayload)

	t.Run("truncated storage sizes", func(t *testing.T) {
		raw := buildStorageRecord(storageTestField{id: 0x33, body: validBody})
		for n := 0; n < len(raw); n++ {
			if _, err := UnitStorageWeapons(raw[:n]); err == nil {
				t.Fatalf("prefix of %d/%d bytes was accepted", n, len(raw))
			}
		}
	})

	t.Run("truncated declared stream", func(t *testing.T) {
		body := packWeaponFieldBits(validPayload, uint32(len(validPayload)*8+8))
		raw := buildStorageRecord(storageTestField{id: 0x33, body: body})
		if _, err := UnitStorageWeapons(raw); !errors.Is(err, ErrStorageLayout) {
			t.Fatalf("error = %v, want ErrStorageLayout", err)
		}
	})

	t.Run("short field size", func(t *testing.T) {
		raw := buildStorageRecordWithSizes(
			[]storageTestField{{id: 0x33, body: validBody}},
			[]uint32{uint32(len(validBody)*8 - 8)},
		)
		if _, err := UnitStorageWeapons(raw); !errors.Is(err, ErrStorageLayout) {
			t.Fatalf("error = %v, want ErrStorageLayout", err)
		}
	})

	t.Run("weapon count", func(t *testing.T) {
		payload := append([]byte{7}, weaponPayload(nil, nil, nil, false)[1:]...)
		raw := buildStorageRecord(storageTestField{id: 0x33, body: packWeaponField(payload)})
		if _, err := UnitStorageWeapons(raw); !errors.Is(err, ErrStorageLayout) {
			t.Fatalf("error = %v, want ErrStorageLayout", err)
		}
	})

	t.Run("truncated string", func(t *testing.T) {
		payload := []byte{1, 20, 'a', 'b', 0, 0}
		raw := buildStorageRecord(storageTestField{id: 0x33, body: packWeaponField(payload)})
		if _, err := UnitStorageWeapons(raw); !errors.Is(err, ErrStorageLayout) {
			t.Fatalf("error = %v, want ErrStorageLayout", err)
		}
	})

	t.Run("duplicate field", func(t *testing.T) {
		raw := buildStorageRecord(
			storageTestField{id: 0x33, body: validBody},
			storageTestField{id: 0x33, body: validBody},
		)
		if _, err := UnitStorageWeapons(raw); !errors.Is(err, ErrStorageLayout) {
			t.Fatalf("error = %v, want ErrStorageLayout", err)
		}
	})

	t.Run("missing field", func(t *testing.T) {
		raw := buildStorageRecord(storageTestField{id: 1, body: []byte{0, 0}})
		if _, err := UnitStorageWeapons(raw); !errors.Is(err, ErrStorageWeaponsMissing) {
			t.Fatalf("error = %v, want ErrStorageWeaponsMissing", err)
		}
	})

	t.Run("non-ASCII string", func(t *testing.T) {
		payload := []byte{1, 1, 0xff, 0, 0, 0}
		raw := buildStorageRecord(storageTestField{id: 0x33, body: packWeaponField(payload)})
		if _, err := UnitStorageWeapons(raw); !errors.Is(err, ErrStorageLayout) {
			t.Fatalf("error = %v, want ErrStorageLayout", err)
		}
	})
}
