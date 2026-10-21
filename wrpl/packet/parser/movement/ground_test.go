package packetmovement

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/game"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

type testBitWriter struct {
	buf  []byte
	bits int
}

func (w *testBitWriter) writeBit(v bool) {
	if w.bits%8 == 0 {
		w.buf = append(w.buf, 0)
	}
	if v {
		w.buf[len(w.buf)-1] |= 1 << (7 - (w.bits % 8))
	}
	w.bits++
}

func (w *testBitWriter) writeBits(v uint64, n int) {
	for i := 0; i < n; i++ {
		w.writeBit((v>>i)&1 != 0)
	}
}

func (w *testBitWriter) writeBytes(v []byte) {
	for _, b := range v {
		for i := 7; i >= 0; i-- {
			w.writeBit((b>>i)&1 != 0)
		}
	}
}

func (w *testBitWriter) writeU16(v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.writeBytes(b[:])
}

func (w *testBitWriter) writeU32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.writeBytes(b[:])
}

func (w *testBitWriter) writeCompressed(v uint64) {
	for v >= 0x80 {
		w.writeBytes([]byte{byte(v&0x7f) | 0x80})
		v >>= 7
	}
	w.writeBytes([]byte{byte(v)})
}

func (w *testBitWriter) align() {
	for w.bits%8 != 0 {
		w.writeBit(false)
	}
}

func (w *testBitWriter) bytes() []byte { return w.buf }

func groundPositionPacket(t *testing.T, mid byte, packetTime uint32) *packet.Packet {
	t.Helper()
	vehicle := &testBitWriter{}
	vehicle.writeBytes([]byte{7}) // opaque VehicleInfo leading byte
	vehicle.writeBit(true)
	vehicle.writeBits(0, 3*16)
	vehicle.writeBit(false) // bool2
	vehicle.writeBit(true)  // bool3: full position follows
	for _, v := range []float32{120.5, 330.25, -515.75} {
		vehicle.writeU32(math.Float32bits(v))
	}

	body := &testBitWriter{}
	body.writeU16(0)           // lower UID; zero is valid
	body.writeU16(1)           // upper UID
	body.writeU32(0)           // synchronization time
	body.writeU16(0)           // reliability order
	body.writeBit(true)        // UID present
	body.writeBit(false)       // opaque flag
	body.writeBit(false)       // no extra four-bit field
	body.writeBit(false)       // val2
	body.writeBit(false)       // val3
	body.writeBit(false)       // opaque flag
	body.writeBit(false)       // val1: no direction floats
	body.writeBytes([]byte{0}) // turret count
	body.writeBit(true)        // compressed differential
	body.writeBit(true)        // full differential
	body.writeBits(0, 5)       // packet number delta
	body.writeBits(64, 13)
	body.writeCompressed(uint64(len(vehicle.bytes())))
	body.align()
	body.writeBytes(vehicle.bytes())
	body.writeBit(false) // no Point4
	body.writeBit(false) // no turret data
	body.writeBit(false) // no sensor data
	body.align()

	payload := &testBitWriter{}
	payload.writeBytes([]byte{0x02, 0x58, mid, 0xf0})
	payload.writeCompressed(uint64(body.bits))
	payload.align()
	for i := 0; i < body.bits; i++ {
		payload.writeBit(body.buf[i/8]&(1<<(7-(i%8))) != 0)
	}
	return &packet.Packet{CurrentTime: packetTime, PacketType: 4, PacketPayload: payload.bytes()}
}

func TestGroundPositionsFullDiffUIDZeroWithoutECS(t *testing.T) {
	for _, mid := range []byte{0x73, 0x74} {
		p := NewPositionRetainerParserWithECS(nil)
		pk := groundPositionPacket(t, mid, 1234)
		parsed, err := p.Parse(pk)
		if err != nil {
			t.Fatalf("mid %02x: %v", mid, err)
		}
		if samples := parsed.([]game.SpaceTime); len(samples) != 1 {
			t.Fatalf("mid %02x: parsed %d samples, want 1", mid, len(samples))
		}
		key := GroundKey(0)
		if key != 1<<63 {
			t.Fatalf("UID zero key = %#x", key)
		}
		got := p.GroundPaths[key]
		if len(got) != 1 || got[0].Time != 1234 || got[0].X != 120.5 || got[0].Y != 330.25 || got[0].Z != -515.75 {
			t.Fatalf("mid %02x: GroundPaths[%#x] = %+v", mid, key, got)
		}
	}
}

func TestGroundConstructorModes(t *testing.T) {
	legacy := NewPositionRetainerParser()
	if _, ok := legacy.ParsesMatching()[6]; ok {
		t.Fatal("legacy constructor unexpectedly enabled ECS observation")
	}
	for _, checks := range legacy.ParsesMatching()[4] {
		if len(checks) == 4 && checks[0].Val == 0x02 {
			t.Fatal("legacy constructor unexpectedly enabled ground MPI parsing")
		}
	}
	current := NewPositionRetainerParserWithECS(nil)
	if _, ok := current.ParsesMatching()[6]; !ok {
		t.Fatal("WithECS(nil) did not enable ECS observation")
	}
	if _, err := current.Parse(groundPositionPacket(t, 0x73, 9)); err != nil {
		t.Fatalf("WithECS(nil) ground parse: %v", err)
	}
}

func TestGroundUncompressedBodyIsRejected(t *testing.T) {
	// The current parser deliberately supports the compressed path evidenced by
	// the replay. An uncompressed body must stop the packet rather than drift the
	// UID bit stream.
	body := &testBitWriter{}
	body.writeU16(0)
	body.writeU16(1)
	body.writeU32(0)
	body.writeU16(0)
	body.writeBit(true)
	body.writeBit(false)
	body.writeBit(false)
	body.writeBit(false)
	body.writeBit(false)
	body.writeBit(false)
	body.writeBit(false)
	body.writeBytes([]byte{0})
	body.writeBit(false)
	payload := &testBitWriter{}
	payload.writeBytes([]byte{0x02, 0x58, 0x73, 0xf0})
	payload.writeCompressed(uint64(body.bits))
	payload.align()
	for i := 0; i < body.bits; i++ {
		payload.writeBit(body.buf[i/8]&(1<<(7-(i%8))) != 0)
	}
	p := NewPositionRetainerParserWithECS(nil)
	_, err := p.Parse(&packet.Packet{PacketType: 4, PacketPayload: payload.bytes()})
	if err == nil || !strings.Contains(err.Error(), "unsupported uncompressed") {
		t.Fatalf("uncompressed body error = %v", err)
	}
}

func TestGroundDeltaHistoryRejectsStaleSlot(t *testing.T) {
	h := newGroundDeltaHistory()
	total := 0
	if _, err := h.apply(true, 0, 64, []byte{0xaa}, &total); err != nil {
		t.Fatal(err)
	}
	// Packet 96 maps to the same ring slot as packet 64. Matching only the slot
	// must not treat that stale base as valid.
	_, err := h.apply(false, 0, 96, []byte{0x04, 222}, &total)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("stale history base error = %v", err)
	}
}

func TestGroundDeltaHistoryAppliesPartial(t *testing.T) {
	h := newGroundDeltaHistory()
	total := 0
	base := []byte{0xaa, 0xbb}
	if _, err := h.apply(true, 0, 64, base, &total); err != nil {
		t.Fatal(err)
	}
	// RLE 0x04 followed by parity 222 emits four zero 2-bit units: one zero byte.
	got, err := h.apply(false, 1, 65, []byte{0x04, 222}, &total)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(base) {
		t.Fatalf("patched bytes = %x, want %x", got, base)
	}
}

func TestLegacyPositionTruncationReturnsError(t *testing.T) {
	payload := make([]byte, 40)
	copy(payload, []byte{0xff, 0x0f, 0x80, 0x80, 0x80, 0xa3, 0xf0})
	payload[10] = 0
	payload[11] = 0
	payload[13] = 0x13
	p := NewPositionRetainerParser()
	_, err := p.Parse(&packet.Packet{PacketType: 4, PacketPayload: payload})
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated legacy error = %v", err)
	}
}

func TestECSObservationDoesNotPanicWithoutParsedEntity(t *testing.T) {
	ecs := packetecs2.NewPacketECSParser(packetecs2.ComponentHashMaps{})
	ecs.Messages = []packetecs2.ParsedPacketECS{{PacketSeq: 1, Messages: []*packetecs2.Message{{}}}}
	p := NewPositionRetainerParserWithECS(ecs)
	if _, err := p.Parse(&packet.Packet{PacketType: 6, PacketPayload: []byte{0x26, 0, 0, 0, 0, 0}}); err != nil {
		t.Fatalf("destroy packet: %v", err)
	}
}

func TestGroundEndRequiresObservedDestructionForKnownEID(t *testing.T) {
	ecs := packetecs2.NewPacketECSParser(packetecs2.ComponentHashMaps{})
	p := NewPositionRetainerParserWithECS(ecs)
	key := GroundKey(0)
	p.GroundPaths[key] = []game.SpaceTime{{Time: 100, X: 1}}
	p.groundEIDToUID[7] = 0
	// EID 7 is encoded by the two-byte immediate form: (7<<2)|1.
	payload := []byte{0x26, 0, 0x1d, 0}
	if _, err := p.Parse(&packet.Packet{CurrentTime: 500, PacketType: 6, PacketPayload: payload}); err != nil {
		t.Fatal(err)
	}
	if got, ok := p.GroundEnds[key]; !ok || got != 500 {
		t.Fatalf("GroundEnds[%#x] = %d,%t; want 500,true", key, got, ok)
	}
	unknown := GroundKey(1)
	p.GroundPaths[unknown] = []game.SpaceTime{{Time: 100}}
	if _, err := p.Parse(&packet.Packet{CurrentTime: 600, PacketType: 6, PacketPayload: payload}); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.GroundEnds[unknown]; ok {
		t.Fatalf("unknown UID received a guessed destruction: %+v", p.GroundEnds)
	}
}
