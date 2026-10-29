package packetkill

import (
	"encoding/binary"
	"sort"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	ecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

type killTestField struct {
	num  uint8
	data []byte
}

func TestKillParserReadsBoundedFieldsWithoutECS(t *testing.T) {
	pk := killTestPacket(1234, []killTestField{
		{num: 1, data: killTestU32(17)},
		{num: 2, data: killTestString("vehicle")},
		{num: 3, data: killTestU16(77)},
		{num: 4, data: killTestU16(42)},
		{num: 0xa, data: killTestString("cannon")},
		{num: 0xb, data: killTestU32(18)},
		{num: 0xc, data: killTestString("tail")},
	})
	parser := &PacketKillParser{KeepKills: true, ECS: &ecs2.EntityManager{
		Entities: map[uint32]*ecs2.Entity{}, Uid_lookup: map[int32]*ecs2.Entity{},
	}}
	result, err := parser.Parse(pk)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	entry, ok := result.(KillEntry)
	if !ok {
		t.Fatalf("result type = %T", result)
	}
	if entry.CurrentTime != 1234 || entry.KillerPid != 17 || entry.VictimPid != 18 ||
		!entry.HasKillerPID || !entry.HasVictimPID ||
		!entry.HasKillerUID || entry.KillerUid != 42 || !entry.HasVictimUID || entry.VictimUid != 77 ||
		entry.PlayerVehicle != "vehicle" || entry.PlayerWeapon != "cannon" || entry.DestroyedWeapon != "tail" {
		t.Fatalf("parsed entry = %+v", entry)
	}
	if entry.ResolvedKiller != nil || entry.ResolvedVictim != nil {
		t.Fatal("absent ECS entities were represented as resolved")
	}
	if len(parser.Kills) != 1 || parser.Kills[0].PlayerWeapon != "cannon" {
		t.Fatalf("kept kills = %+v", parser.Kills)
	}
}

func TestKillParserDistinguishesUIDZeroFromMissingFields(t *testing.T) {
	present := killTestPacket(2000, []killTestField{
		{num: 3, data: killTestU16(0)},
		{num: 4, data: killTestU16(0)},
		{num: 0xa, data: killTestString("gun")},
	})
	presentParser := &PacketKillParser{}
	result, err := presentParser.Parse(present)
	if err != nil {
		t.Fatalf("explicit UID zero parse: %v", err)
	}
	pair := result.(KillEntry)
	if !pair.HasKillerUID || !pair.HasVictimUID || pair.KillerUid != 0 || pair.VictimUid != 0 {
		t.Fatalf("explicit UID zero lost: %+v", pair)
	}

	missing := killTestPacket(2100, []killTestField{{num: 0xa, data: killTestString("gun")}})
	result, err = presentParser.Parse(missing)
	if err != nil {
		t.Fatalf("missing UID parse: %v", err)
	}
	pair = result.(KillEntry)
	if pair.HasKillerUID || pair.HasVictimUID {
		t.Fatalf("missing UIDs reported present: %+v", pair)
	}
}

func TestKillParserDistinguishesPIDZeroFromMissingFields(t *testing.T) {
	for _, present := range []bool{true, false} {
		fields := []killTestField{{num: 0xa, data: killTestString("gun")}}
		if present {
			fields = append(fields, killTestField{num: 1, data: killTestU32(0)}, killTestField{num: 0xb, data: killTestU32(0)})
		}
		entry, err := parseKillEntry(killTestPacket(2000, fields))
		if err != nil || entry.HasKillerPID != present || entry.HasVictimPID != present || entry.KillerPid != 0 || entry.VictimPid != 0 {
			t.Fatalf("PID presence %v: %+v %v", present, entry, err)
		}
	}
}

func TestKillParserRejectsHostileInputWithoutRetainingIt(t *testing.T) {
	valid := killTestPacket(3000, []killTestField{
		{num: 3, data: killTestU16(9)},
		{num: 4, data: killTestU16(7)},
		{num: 0xa, data: killTestString("gun")},
	})
	oversized := append([]byte(nil), valid.PacketPayload...)
	binary.LittleEndian.PutUint16(oversized[4:6], 0xffff)

	overlongString := killTestPacketFromParts(3100, 1<<0xa,
		[]byte{255}, killTestSizeBytes([]uint32{8}))
	fieldOverrun := killTestPacketFromParts(3200, 1<<3,
		killTestU16(5), killTestSizeBytes([]uint32{64}))
	overlongSize := killTestPacketFromParts(3300, 1<<3,
		killTestU16(5), killTestSizeBytes([]uint32{killMaxFieldBits + 1}))

	tooLarge := make([]byte, killMaxPacketBytes+1)
	copy(tooLarge, []byte{0x02, 0x58, 0x58, 0xf0})
	unterminatedMask := &packet.Packet{PacketType: killPacketType, CurrentTime: 3500,
		PacketPayload: append([]byte{0x02, 0x58, 0x58, 0xf0, 8, 0}, 0x80, 0x80, 0x80, 0x80, 0x80)}
	tests := map[string]*packet.Packet{
		"nil":               nil,
		"short":             {PacketType: killPacketType, PacketPayload: []byte{0x02, 0x58, 0x58, 0xf0}},
		"bad signature":     {PacketType: killPacketType, PacketPayload: []byte{2, 0, 0, 0, 0, 0, 0, 0}},
		"oversized offset":  {PacketType: killPacketType, CurrentTime: 3000, PacketPayload: oversized},
		"overlong string":   overlongString,
		"field overrun":     fieldOverrun,
		"overlong field":    overlongSize,
		"oversized payload": {PacketType: killPacketType, PacketPayload: tooLarge},
		"unterminated mask": unterminatedMask,
	}
	parser := &PacketKillParser{KeepKills: true}
	for name, pk := range tests {
		if _, err := parser.Parse(pk); err == nil {
			t.Fatalf("%s: hostile input was accepted", name)
		}
		if len(parser.Kills) != 0 {
			t.Fatalf("%s: hostile input was retained", name)
		}
	}
}

func TestKillParserOnlyKeepsSuccessfulEntriesAtCapacity(t *testing.T) {
	parser := &PacketKillParser{KeepKills: true, Kills: make([]KillEntry, killMaxRecords)}
	pk := killTestPacket(4000, []killTestField{
		{num: 3, data: killTestU16(3)},
		{num: 4, data: killTestU16(4)},
		{num: 0xa, data: killTestString("gun")},
	})
	if _, err := parser.Parse(pk); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parser.Kills) != killMaxRecords {
		t.Fatalf("kept records = %d, cap = %d", len(parser.Kills), killMaxRecords)
	}
	if _, err := parser.Parse(&packet.Packet{PacketType: killPacketType}); err == nil {
		t.Fatal("malformed input unexpectedly succeeded")
	}
	if len(parser.Kills) != killMaxRecords {
		t.Fatal("malformed input changed retained records")
	}
}

func killTestPacket(time uint32, fields []killTestField) *packet.Packet {
	fields = append([]killTestField(nil), fields...)
	sort.Slice(fields, func(i, j int) bool { return fields[i].num < fields[j].num })
	var mask uint32
	var body []byte
	sizes := make([]uint32, 0, len(fields))
	for _, field := range fields {
		mask |= 1 << field.num
		body = append(body, field.data...)
		sizes = append(sizes, uint32(len(field.data))*8)
	}
	return killTestPacketFromParts(time, mask, body, killTestSizeBytes(sizes))
}

func killTestPacketFromParts(time uint32, fields uint32, body, sizeBytes []byte) *packet.Packet {
	header := make([]byte, 2)
	header = append(header, killTestULEB(fields)...)
	offset := len(header) + len(body)
	header[0], header[1] = byte(offset), byte(offset>>8)
	payload := []byte{0x02, 0x58, 0x58, 0xf0}
	payload = append(payload, header...)
	payload = append(payload, body...)
	payload = append(payload, sizeBytes...)
	return &packet.Packet{PacketType: killPacketType, CurrentTime: time, PacketPayload: payload}
}

type killTestBits struct {
	data []byte
	used int
}

func (w *killTestBits) write(value uint32, count int) {
	for bit := count - 1; bit >= 0; bit-- {
		if w.used&7 == 0 {
			w.data = append(w.data, 0)
		}
		if value>>bit&1 != 0 {
			w.data[w.used>>3] |= byte(1) << (7 - (w.used & 7))
		}
		w.used++
	}
}

func killTestSizeBytes(sizes []uint32) []byte {
	var bits killTestBits
	for _, size := range sizes {
		bits.write(0, 3)
		for _, part := range killTestULEB(size) {
			bits.write(uint32(part), 8)
		}
	}
	return bits.data
}

func killTestULEB(value uint32) []byte {
	var out []byte
	for {
		part := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			part |= 0x80
		}
		out = append(out, part)
		if value == 0 {
			return out
		}
	}
}

func killTestString(value string) []byte {
	out := []byte{byte(len(value))}
	return append(out, []byte(value)...)
}

func killTestU16(value uint16) []byte {
	var out [2]byte
	binary.LittleEndian.PutUint16(out[:], value)
	return out[:]
}

func killTestU32(value uint32) []byte {
	var out [4]byte
	binary.LittleEndian.PutUint32(out[:], value)
	return out[:]
}
