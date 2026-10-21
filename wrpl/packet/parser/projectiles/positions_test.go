package projectiles

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
)

type testBitWriter struct {
	data []byte
	bit  int
}

func (w *testBitWriter) writeBit(v bool) {
	if w.bit/8 == len(w.data) {
		w.data = append(w.data, 0)
	}
	if v {
		w.data[w.bit/8] |= 1 << (7 - (w.bit & 7))
	}
	w.bit++
}

func (w *testBitWriter) writeBits(value uint64, count int) {
	if w.bit&7 == 0 && count&7 == 0 {
		needed := w.bit/8 + count/8
		for len(w.data) < needed {
			w.data = append(w.data, 0)
		}
		for i := 0; i < count/8; i++ {
			w.data[w.bit/8+i] = byte(value >> (i * 8))
		}
		w.bit += count
		return
	}
	// ReadBits reconstructs little-endian values by packing the first stream
	// bit into the high bit of each output byte.
	full := count / 8
	for i := 0; i < full; i++ {
		b := byte(value >> (i * 8))
		for bit := 7; bit >= 0; bit-- {
			w.writeBit(b&(1<<bit) != 0)
		}
	}
	if rem := count % 8; rem > 0 {
		b := byte(value>>(full*8)) & byte((1<<rem)-1)
		for bit := rem - 1; bit >= 0; bit-- {
			w.writeBit(b&(1<<bit) != 0)
		}
	}
}

func (w *testBitWriter) writeByte(v byte) { w.writeBits(uint64(v), 8) }

func (w *testBitWriter) writeU16(v uint16) { w.writeBits(uint64(v), 16) }

func (w *testBitWriter) writeFloat32(v float32) {
	w.writeBits(uint64(math.Float32bits(v)), 32)
}

func (w *testBitWriter) writeULEB(v uint64) {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		w.writeByte(b)
		if v == 0 {
			return
		}
	}
}

func (w *testBitWriter) writeRawEntity(raw uint32) {
	w.writeBits(uint64(raw), 32)
}

func (w *testBitWriter) writeDirectHeader(message uint16) {
	w.writeByte(0x02)
	w.writeByte(0x58)
	w.writeU16(message)
}

func (w *testBitWriter) writeExtendedHeader(ext uint64, message uint16) {
	w.writeByte(0xff)
	w.writeByte(0x0f)
	w.writeULEB(ext)
	w.writeU16(message)
}

func (w *testBitWriter) writeRawRecord(raw uint32, guidance bool, x, y, z float32) {
	w.writeBit(guidance)
	w.writeBit(true) // no extra tail
	w.writeRawEntity(raw)
	w.writeBit(true) // raw Point3
	w.writeFloat32(x)
	w.writeFloat32(y)
	w.writeFloat32(z)
}

func (w *testBitWriter) writeCompressedRecord(raw uint32, guidance bool, compressed1, compressed2 uint32) {
	w.writeBit(guidance)
	w.writeBit(true) // no extra tail
	w.writeRawEntity(raw)
	w.writeBit(false) // compressed Point3
	w.writeBits(uint64(compressed1), 32)
	w.writeBits(uint64(compressed2), 32)
}

// writeTail covers PositionSync.cpp's optional field block, including the
// seeker payload branch when guidance is absent.
func (w *testBitWriter) writeTail(guidance bool) {
	w.writeTailWithSeekerValueBits(guidance, math.Float32bits(9.5))
}

func (w *testBitWriter) writeTailWithSeekerValueBits(guidance bool, seekerValueBits uint32) {
	w.writeULEB(41) // zig-zag value
	for _, v := range []float32{4, 5, 6} {
		w.writeFloat32(v)
	}
	for i := uint16(1); i <= 6; i++ {
		w.writeU16(i * 100)
	}
	w.writeBit(true)
	w.writeU16(77)
	w.writeByte(0x0f)
	for i := uint64(0); i < 4; i++ {
		w.writeULEB(100 + i)
	}
	if guidance {
		w.writeBit(false) // SACLOS payload absent
		return
	}
	w.writeBits(uint64(seekerValueBits), 32)
	w.writeBit(true)
	w.writeU16(11)
	w.writeBits(0b10101010101, 11)
}

func buildPositionPacket(writeHeader func(*testBitWriter), body *testBitWriter) []byte {
	w := &testBitWriter{}
	writeHeader(w)
	w.writeULEB(uint64(body.bit))
	for _, b := range body.data {
		w.writeByte(b)
	}
	return w.data
}

func directPositionPacket(message uint16, body *testBitWriter) []byte {
	return buildPositionPacket(func(w *testBitWriter) { w.writeDirectHeader(message) }, body)
}

func extendedPositionPacket(ext uint64, message uint16, body *testBitWriter) []byte {
	return buildPositionPacket(func(w *testBitWriter) { w.writeExtendedHeader(ext, message) }, body)
}

func parseOne(t *testing.T, payload []byte, time uint32) (*PositionParser, int, error) {
	t.Helper()
	p := NewPositionParser()
	result, err := p.Parse(&packet.Packet{CurrentTime: time, PacketPayload: payload})
	added, _ := result.(int)
	return p, added, err
}

func TestBitWriterRoundTrip(t *testing.T) {
	const raw = uint32(0xf1234567)
	w := &testBitWriter{}
	w.writeBit(true)
	w.writeBit(false)
	w.writeRawEntity(raw)
	w.writeFloat32(12.5)
	r := danet.NewBitReader(w.data)
	if got, err := readBool(r); err != nil || !got {
		t.Fatalf("first bool=%v err=%v", got, err)
	}
	if got, err := readBool(r); err != nil || got {
		t.Fatalf("second bool=%v err=%v", got, err)
	}
	if got, err := r.ReadU32LE(); err != nil || got != raw {
		t.Fatalf("raw entity=0x%x err=%v", got, err)
	}
	if got, err := readFloat32(r); err != nil || got != 12.5 {
		t.Fatalf("float=%v err=%v", got, err)
	}
}

func TestRawPositionsUseNestedBitstreamAndFullEntityValue(t *testing.T) {
	const raw1 = uint32(0x001f8c01)
	const raw2 = uint32(0xf1234567)
	body := &testBitWriter{}
	body.writeByte(2)
	body.writeRawRecord(raw1, true, 125.5, 250.25, -75.75)
	body.writeRawRecord(raw2, true, 130.5, 260.25, -80.75)
	body.writeByte(0)
	body.writeByte(0)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 42_000)
	if err != nil || added != 2 {
		t.Fatalf("Parse() added=%d err=%v", added, err)
	}
	if len(p.Samples) != 2 {
		t.Fatalf("raw entity keys collapsed: got %d keys", len(p.Samples))
	}
	if got := p.Samples[uint64(raw1)][0]; got.Time != 42_000 || got.X != 125.5 || got.Y != 250.25 || got.Z != -75.75 || !got.FromSync {
		t.Fatalf("first sample = %+v", got)
	}
	if got := p.Samples[uint64(raw2)][0]; got.X != 130.5 || got.Z != -80.75 || !got.FromSync {
		t.Fatalf("full-width entity sample = %+v", got)
	}
}

func TestCapturedReplayPacket(t *testing.T) {
	const wire = "0258dbf0a40801006307c03d899b58d07664e8cdfed908b0ca0b002fb7a672da77bbc54267ede295216880400007c018a080f0d010101000000002f810199dc4b346a1acfc459ed6bb45a0d7ccc3ea3b85422ed998c11d85a9be0df4fd3e3314463f05ad838473e8bfae7cfa7f00007f7f83001981bfcfea9fd1403920000ffffffff0fffffffff0f00000"
	payload, err := hex.DecodeString(wire)
	if err != nil {
		t.Fatal(err)
	}
	p, added, err := parseOne(t, payload, 106779)
	if err != nil || added != 1 {
		t.Fatalf("Parse() added=%d err=%v", added, err)
	}
	got := p.Samples[2067457][0]
	if got.Time != 106779 || got.X != -27942.4609375 || got.Y != 10732.8779296875 || got.Z != 6430.80419921875 || !got.FromSync {
		t.Fatalf("captured sample = %+v", got)
	}
}

func TestRawGenerationCandidatesStayDistinct(t *testing.T) {
	const rawGenerationOne = uint32(0x001f8c01)
	const rawGenerationZero = uint32(0x001f8c02)
	body := &testBitWriter{}
	body.writeByte(2)
	body.writeRawRecord(rawGenerationOne, true, 1, 2, 3)
	body.writeRawRecord(rawGenerationZero, true, 4, 5, 6)
	body.writeByte(0)
	body.writeByte(0)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 7)
	if err != nil || added != 2 {
		t.Fatalf("Parse() added=%d err=%v", added, err)
	}
	if len(p.Samples) != 2 {
		t.Fatalf("distinct raw keys collapsed: got %d keys", len(p.Samples))
	}
	if p.Samples[uint64(rawGenerationOne)][0].X != 1 || p.Samples[uint64(rawGenerationZero)][0].X != 4 {
		t.Fatalf("generation-like raw keys mapped incorrectly: %+v", p.Samples)
	}
}

func TestCompressedPositionUsesExtendedHeader(t *testing.T) {
	const (
		compressed1 = uint32(0xc0000300)
		compressed2 = uint32(0x00300000)
		rawEntity   = uint32(0x00214401)
	)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeCompressedRecord(rawEntity, false, compressed1, compressed2)
	body.writeByte(0)
	body.writeByte(0)

	p, added, err := parseOne(t, extendedPositionPacket(0x2a, shellPositions, body), 1234)
	if err != nil || added != 1 {
		t.Fatalf("Parse() added=%d err=%v", added, err)
	}
	got := p.Samples[uint64(rawEntity)][0]
	if got.X != 7500 || got.Y != 7500 || got.Z != 7500 {
		t.Fatalf("compressed sample = %+v", got)
	}
}

func TestWeaponSyncSeparatesRocketsBombsAndTorpedoes(t *testing.T) {
	const (
		rocketEntity  = uint32(0x001f8c01)
		bombEntity    = uint32(0x0024c401)
		torpedoEntity = uint32(0x00274401)
	)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeRawRecord(rocketEntity, true, 1, 2, 3)
	body.writeByte(1)
	body.writeBit(false)
	body.writeBit(false)
	body.writeRawEntity(bombEntity)
	body.writeBit(true)
	body.writeFloat32(4)
	body.writeFloat32(5)
	body.writeFloat32(6)
	body.writeTail(false)
	body.writeByte(1)
	body.writeBit(true)
	body.writeBit(false)
	body.writeRawEntity(torpedoEntity)
	body.writeBit(true)
	body.writeFloat32(7)
	body.writeFloat32(8)
	body.writeFloat32(9)
	body.writeTail(true)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 10)
	if err != nil {
		t.Fatalf("Parse() added=%d err=%v", added, err)
	}
	if added != 2 {
		t.Fatalf("retained samples=%d, want rocket+bomb", added)
	}
	if len(p.Samples) != 1 || len(p.Samples[uint64(rocketEntity)]) != 1 {
		t.Fatalf("rocket samples=%+v", p.Samples)
	}
	if len(p.BombSamples) != 1 || len(p.BombSamples[uint64(bombEntity)]) != 1 {
		t.Fatalf("bomb samples=%+v", p.BombSamples)
	}
	if got := p.BombSamples[uint64(bombEntity)][0]; got.Time != 10 || got.X != 4 || got.Y != 5 || got.Z != 6 || !got.FromSync {
		t.Fatalf("bomb sample = %+v", got)
	}
	if motion := p.BombSamples[uint64(bombEntity)][0].RawMotion; motion == nil || !motion.Complete || motion.HasGuidance {
		t.Fatalf("bomb tail evidence = %+v", motion)
	}
	if _, ok := p.Samples[uint64(bombEntity)]; ok {
		t.Fatalf("bomb class polluted rocket samples: %+v", p.Samples)
	}
	if _, ok := p.BombSamples[uint64(rocketEntity)]; ok {
		t.Fatalf("rocket class polluted bomb samples: %+v", p.BombSamples)
	}
	if _, ok := p.Samples[uint64(torpedoEntity)]; ok {
		t.Fatalf("torpedo class was retained: %+v", p.Samples)
	}
	if _, ok := p.BombSamples[uint64(torpedoEntity)]; ok {
		t.Fatalf("torpedo class was retained as a bomb: %+v", p.BombSamples)
	}
}

func TestMalformedBombDoesNotRetainOrPolluteRocketSamples(t *testing.T) {
	const (
		rocketEntity = uint32(0x001f8c01)
		bombEntity   = uint32(0x0024c401)
	)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeRawRecord(rocketEntity, true, 1, 2, 3)
	body.writeByte(1)
	body.writeBit(false)
	body.writeBit(false) // tail promised but the record ends after position
	body.writeRawEntity(bombEntity)
	body.writeBit(true)
	body.writeFloat32(4)
	body.writeFloat32(5)
	body.writeFloat32(6)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 10)
	if err == nil || added != 1 {
		t.Fatalf("malformed bomb added=%d err=%v", added, err)
	}
	if len(p.Samples) != 1 || len(p.Samples[uint64(rocketEntity)]) != 1 {
		t.Fatalf("rocket retained before bomb error = %+v", p.Samples)
	}
	if len(p.BombSamples) != 0 {
		t.Fatalf("malformed bomb was retained: %+v", p.BombSamples)
	}
}

func TestInvalidBombPositionIsSkippedWithoutPollutingRockets(t *testing.T) {
	const (
		rocketEntity = uint32(0x001f8c01)
		bombEntity   = uint32(0x0024c401)
	)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeRawRecord(rocketEntity, true, 1, 2, 3)
	body.writeByte(1)
	body.writeRawRecord(bombEntity, true, float32(math.Inf(1)), 5, 6)
	body.writeByte(0)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 10)
	if err != nil || added != 1 {
		t.Fatalf("invalid bomb added=%d err=%v", added, err)
	}
	if len(p.Samples) != 1 || len(p.Samples[uint64(rocketEntity)]) != 1 {
		t.Fatalf("rocket samples = %+v", p.Samples)
	}
	if len(p.BombSamples) != 0 {
		t.Fatalf("non-finite bomb position was retained: %+v", p.BombSamples)
	}
}

func TestZeroValuePositionParserInitializesBothSampleMaps(t *testing.T) {
	const (
		rocketEntity = uint32(0x001f8c01)
		bombEntity   = uint32(0x0024c401)
	)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeRawRecord(rocketEntity, true, 1, 2, 3)
	body.writeByte(1)
	body.writeRawRecord(bombEntity, true, 4, 5, 6)
	body.writeByte(0)
	payload := directPositionPacket(rocketPositions, body)

	p := &PositionParser{}
	result, err := p.Parse(&packet.Packet{CurrentTime: 1, PacketPayload: payload})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if result != 2 {
		t.Fatalf("added=%v, want 2", result)
	}
	if len(p.Samples[uint64(rocketEntity)]) != 1 || len(p.BombSamples[uint64(bombEntity)]) != 1 {
		t.Fatalf("zero-value parser samples=%+v bombs=%+v", p.Samples, p.BombSamples)
	}
}

func TestTailFieldsAndFollowingRecord(t *testing.T) {
	const raw1 = uint32(0x0020bc02)
	const raw2 = uint32(0x00274401)
	body := &testBitWriter{}
	body.writeByte(2)
	body.writeBit(true)
	body.writeBit(false) // extra tail present
	body.writeRawEntity(raw1)
	body.writeBit(true)
	body.writeFloat32(1)
	body.writeFloat32(2)
	body.writeFloat32(3)
	body.writeTail(true)
	body.writeBit(false)
	body.writeBit(false)
	body.writeRawEntity(raw2)
	body.writeBit(true)
	body.writeFloat32(10)
	body.writeFloat32(20)
	body.writeFloat32(30)
	body.writeTail(false)
	body.writeByte(0)
	body.writeByte(0)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 9000)
	if err != nil || added != 2 {
		t.Fatalf("Parse() added=%d err=%v", added, err)
	}
	if got := p.Samples[uint64(raw1)][0].Y; got != 2 {
		t.Fatalf("guided tail record Y = %v", got)
	}
	rawMotion := p.Samples[uint64(raw1)][0].RawMotion
	if rawMotion == nil || !rawMotion.Complete || !rawMotion.HasTail || !rawMotion.HasGuidance {
		t.Fatalf("guided tail raw motion = %+v", rawMotion)
	}
	if rawMotion.VectorBits != [3]uint32{
		math.Float32bits(4), math.Float32bits(5), math.Float32bits(6),
	} {
		t.Fatalf("raw vector bits = %#v", rawMotion.VectorBits)
	}
	if rawMotion.Quantized != [2][3]uint16{{100, 200, 300}, {400, 500, 600}} {
		t.Fatalf("raw quantized vectors = %#v", rawMotion.Quantized)
	}
	if got := p.Samples[uint64(raw2)][0]; got.Y != 20 || got.Z != 30 {
		t.Fatalf("seeker tail record = %+v", got)
	}
	if got := p.Samples[uint64(raw2)][0].RawMotion; got == nil || !got.Complete || got.HasGuidance {
		t.Fatalf("seeker tail raw motion = %+v", got)
	}
}

func seekerTailPacket(raw uint32, guidance bool, seekerValueBits uint32) []byte {
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeBit(guidance)
	body.writeBit(false) // extra tail present
	body.writeRawEntity(raw)
	body.writeBit(true)
	body.writeFloat32(1)
	body.writeFloat32(2)
	body.writeFloat32(3)
	body.writeTailWithSeekerValueBits(guidance, seekerValueBits)
	body.writeByte(0)
	body.writeByte(0)
	return directPositionPacket(rocketPositions, body)
}

func TestSeekerValueRetainedAndAccessible(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float32
	}{
		{name: "zero", value: 0},
		{name: "positive", value: 2.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const raw = uint32(0x001f8c01)
			bits := math.Float32bits(tc.value)
			p, added, err := parseOne(t, seekerTailPacket(raw, false, bits), 1)
			if err != nil || added != 1 {
				t.Fatalf("added=%d err=%v", added, err)
			}
			rawMotion := p.Samples[uint64(raw)][0].RawMotion
			if rawMotion == nil || !rawMotion.Complete || !rawMotion.HasTail || !rawMotion.HasSeekerValue || rawMotion.HasGuidance {
				t.Fatalf("raw motion = %+v", rawMotion)
			}
			if rawMotion.SeekerValueBits != bits {
				t.Fatalf("seeker bits = 0x%08x, want 0x%08x", rawMotion.SeekerValueBits, bits)
			}
			if got, ok := rawMotion.SeekerValue(); !ok || got != tc.value {
				t.Fatalf("SeekerValue() = %v, %v; want %v, true", got, ok, tc.value)
			}

			data, err := json.Marshal(rawMotion)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["seeker_value_bits"]) != fmt.Sprintf(`"0x%08x"`, bits) {
				t.Fatalf("JSON %s has wrong seeker bits", data)
			}
			var jsonValue float32
			if err := json.Unmarshal(fields["seeker_value"], &jsonValue); err != nil || jsonValue != tc.value {
				t.Fatalf("JSON seeker value = %s, err=%v", fields["seeker_value"], err)
			}
		})
	}
}

func TestSeekerValueAccessorRequiresCompleteUnguidedTail(t *testing.T) {
	valid := RawMotion{
		Complete:        true,
		HasTail:         true,
		HasSeekerValue:  true,
		SeekerValueBits: math.Float32bits(1.25),
	}
	for _, tc := range []struct {
		name   string
		motion RawMotion
		want   bool
	}{
		{name: "complete unguided", motion: valid, want: true},
		{name: "incomplete", motion: RawMotion{HasTail: true, HasSeekerValue: true, SeekerValueBits: valid.SeekerValueBits}},
		{name: "tail absent", motion: RawMotion{Complete: true, HasSeekerValue: true, SeekerValueBits: valid.SeekerValueBits}},
		{name: "value absent", motion: RawMotion{Complete: true, HasTail: true}},
		{name: "guided", motion: RawMotion{Complete: true, HasTail: true, HasGuidance: true, HasSeekerValue: true, SeekerValueBits: valid.SeekerValueBits}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := tc.motion.SeekerValue(); ok != tc.want {
				t.Fatalf("SeekerValue() ok = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestSeekerValueRequiresUnguidedBranch(t *testing.T) {
	const raw = uint32(0x001f8c01)
	p, added, err := parseOne(t, seekerTailPacket(raw, true, math.Float32bits(9.5)), 1)
	if err != nil || added != 1 {
		t.Fatalf("added=%d err=%v", added, err)
	}
	rawMotion := p.Samples[uint64(raw)][0].RawMotion
	if rawMotion == nil || !rawMotion.Complete || !rawMotion.HasTail || rawMotion.HasSeekerValue {
		t.Fatalf("guided raw motion = %+v", rawMotion)
	}
	if _, ok := rawMotion.SeekerValue(); ok {
		t.Fatal("guided raw motion exposed a seeker value")
	}
	data, err := json.Marshal(rawMotion)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"seeker_value", "seeker_value_bits"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("JSON without seeker branch contains %q: %s", key, data)
		}
	}
}

func TestInvalidSeekerValuesRemainRawEvidence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bits     uint32
		jsonWant string
	}{
		{name: "NaN", bits: 0x7fc00000, jsonWant: "null"},
		{name: "positive infinity", bits: 0x7f800000, jsonWant: "null"},
		{name: "negative infinity", bits: 0xff800000, jsonWant: "null"},
		{name: "negative", bits: math.Float32bits(-2.5), jsonWant: "-2.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const raw = uint32(0x001f8c01)
			p, added, err := parseOne(t, seekerTailPacket(raw, false, tc.bits), 1)
			if err != nil || added != 1 {
				t.Fatalf("added=%d err=%v", added, err)
			}
			rawMotion := p.Samples[uint64(raw)][0].RawMotion
			if rawMotion == nil || !rawMotion.HasSeekerValue || rawMotion.SeekerValueBits != tc.bits {
				t.Fatalf("raw motion = %+v", rawMotion)
			}
			if _, ok := rawMotion.SeekerValue(); ok {
				t.Fatal("invalid seeker value passed SeekerValue()")
			}
			data, err := json.Marshal(rawMotion)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if got := string(fields["seeker_value"]); got != tc.jsonWant {
				t.Fatalf("JSON seeker value = %s, want %s", got, tc.jsonWant)
			}
			if got, want := string(fields["seeker_value_bits"]), fmt.Sprintf(`"0x%08x"`, tc.bits); got != want {
				t.Fatalf("JSON seeker bits = %s, want %s", got, want)
			}
		})
	}
}

func TestTruncatedSeekerTailDoesNotLeakPartialRecord(t *testing.T) {
	const raw = uint32(0x001f8c01)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeBit(false)
	body.writeBit(false)
	body.writeRawEntity(raw)
	body.writeBit(true)
	body.writeFloat32(1)
	body.writeFloat32(2)
	body.writeFloat32(3)
	body.writeULEB(41)
	for _, v := range []float32{4, 5, 6} {
		body.writeFloat32(v)
	}
	for i := uint16(1); i <= 6; i++ {
		body.writeU16(i * 100)
	}
	body.writeBit(false)
	body.writeByte(0)
	body.writeBits(uint64(math.Float32bits(9.5))&0xffff, 16) // seeker value truncated

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 1)
	if err == nil || added != 0 || len(p.Samples) != 0 {
		t.Fatalf("truncation added=%d samples=%+v err=%v", added, p.Samples, err)
	}
}

func TestTailAbsentLeavesRawMotionOptional(t *testing.T) {
	const raw = uint32(0x001f8c01)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeRawRecord(raw, true, 1, 2, 3)
	body.writeByte(0)
	body.writeByte(0)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 1)
	if err != nil || added != 1 {
		t.Fatalf("added=%d err=%v", added, err)
	}
	if got := p.Samples[uint64(raw)][0].RawMotion; got != nil {
		t.Fatalf("raw motion without tail = %+v", got)
	}
}

func TestRawMotionJSONPreservesNonFiniteBits(t *testing.T) {
	const raw = uint32(0x001f8c01)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeBit(true)
	body.writeBit(false)
	body.writeRawEntity(raw)
	body.writeBit(true)
	body.writeFloat32(1)
	body.writeFloat32(2)
	body.writeFloat32(3)
	body.writeULEB(41)
	body.writeBits(uint64(math.Float32bits(float32(math.NaN()))), 32)
	body.writeBits(uint64(math.Float32bits(float32(math.Inf(1)))), 32)
	body.writeFloat32(6)
	for i := uint16(1); i <= 6; i++ {
		body.writeU16(i * 100)
	}
	body.writeBit(false)
	body.writeByte(0)
	body.writeBit(false) // SACLOS absent
	body.writeByte(0)
	body.writeByte(0)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 1)
	if err != nil || added != 1 {
		t.Fatalf("added=%d err=%v", added, err)
	}
	rawMotion := p.Samples[uint64(raw)][0].RawMotion
	if rawMotion == nil || !rawMotion.Complete {
		t.Fatalf("raw motion = %+v", rawMotion)
	}
	data, err := json.Marshal(rawMotion)
	if err != nil {
		t.Fatalf("Marshal RawMotion with non-finite vector: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"vector_bits":["0x7fc00000","0x7f800000","0x40c00000"]`,
		`"vector":[null,null,6]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("JSON %s missing %s", text, want)
		}
	}
}

func TestMalformedTailDoesNotRetainCurrentRecord(t *testing.T) {
	const raw1 = uint32(0x0020bc02)
	const raw2 = uint32(0x00274401)
	body := &testBitWriter{}
	body.writeByte(2)
	body.writeRawRecord(raw1, true, 1, 2, 3)
	body.writeBit(false)
	body.writeBit(false) // tail promised but body ends after position
	body.writeRawEntity(raw2)
	body.writeBit(true)
	body.writeFloat32(10)
	body.writeFloat32(20)
	body.writeFloat32(30)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 9000)
	if err == nil || added != 1 {
		t.Fatalf("malformed tail added=%d err=%v", added, err)
	}
	if len(p.Samples) != 1 || len(p.Samples[uint64(raw1)]) != 1 {
		t.Fatalf("earlier valid record was not preserved: %+v", p.Samples)
	}
	if _, ok := p.Samples[uint64(raw2)]; ok {
		t.Fatalf("record with malformed tail was retained: %+v", p.Samples)
	}
	if p.Samples[uint64(raw1)][0].RawMotion != nil {
		t.Fatalf("record without an extra tail gained raw motion: %+v", p.Samples[uint64(raw1)][0])
	}
}

func TestTruncatedRawRecordProducesNoPoint(t *testing.T) {
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeBit(true)
	body.writeBit(true)
	body.writeRawEntity(5)
	body.writeBit(true)
	body.writeFloat32(10)
	body.writeFloat32(20) // Z is truncated

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 1)
	if err == nil || added != 0 || len(p.Samples) != 0 {
		t.Fatalf("truncation added=%d samples=%v err=%v", added, p.Samples, err)
	}
}

func TestNestedLengthTruncation(t *testing.T) {
	t.Run("unterminated length", func(t *testing.T) {
		payload := []byte{0x02, 0x58, 0x1a, 0xf1, 0x80}
		p, added, err := parseOne(t, payload, 1)
		if err == nil || !strings.Contains(err.Error(), "projectile bitstream length") {
			t.Fatalf("added=%d err=%v", added, err)
		}
		if len(p.Samples) != 0 {
			t.Fatalf("unexpected samples: %+v", p.Samples)
		}
	})

	t.Run("declared length exceeds packet", func(t *testing.T) {
		w := &testBitWriter{}
		w.writeDirectHeader(rocketPositions)
		w.writeULEB(4096)
		w.writeByte(0)
		p, added, err := parseOne(t, w.data, 1)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("added=%d err=%v", added, err)
		}
		if len(p.Samples) != 0 {
			t.Fatalf("unexpected samples: %+v", p.Samples)
		}
	})
}

func TestUnrelatedObjectAndMessageReturnNil(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "general oid zero", payload: []byte{0x00, 0x58, 0xd0, 0x41}},
		{name: "general oid three", payload: []byte{0x03, 0x58, 0xd0, 0x41}},
		{name: "wrong extended oid", payload: []byte{0xff, 0x5f, 0x01, 0xd0, 0x41}},
		{name: "other extended oid", payload: []byte{0xff, 0x07, 0x01, 0xd0, 0x41}},
		{name: "other message", payload: []byte{0x02, 0x58, 0xa0, 0xf0}},
		{name: "short payload", payload: []byte{0x02}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, added, err := parseOne(t, tc.payload, 1)
			if err != nil || added != 0 || len(p.Samples) != 0 {
				t.Fatalf("added=%d samples=%v err=%v", added, p.Samples, err)
			}
		})
	}
}

func TestParsesMatchingOnlyExactProjectileHeaders(t *testing.T) {
	body := &testBitWriter{}
	body.writeByte(0)
	body.writeByte(0)
	body.writeByte(0)
	direct := directPositionPacket(rocketPositions, body)
	extended := extendedPositionPacket(7, shellPositions, body)
	p := NewPositionParser()
	matcher := packet.NewParserMatcher([]packet.PacketParser{p})
	tests := []struct {
		name    string
		payload []byte
		matches int
	}{
		{name: "direct", payload: direct, matches: 1},
		{name: "extended", payload: extended, matches: 1},
		{name: "other direct oid", payload: []byte{0x00, 0x58, 0x1a, 0xf1}, matches: 0},
		{name: "other extended oid", payload: []byte{0xff, 0x5f, 0x01, 0x1a, 0xf1}, matches: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matcher.Match(&packet.Packet{PacketType: 4, PacketPayload: tc.payload})
			if len(got) != tc.matches {
				t.Fatalf("matches=%d, want %d (%+v)", len(got), tc.matches, got)
			}
			for _, result := range got {
				if result.Err != nil {
					t.Fatalf("matched parser error: %v", result.Err)
				}
			}
		})
	}
}

func TestInvalidRawPositionIsNotRetained(t *testing.T) {
	const raw = uint32(0x0020bc02)
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeRawRecord(raw, true, float32(math.Inf(1)), 0, 0)
	body.writeByte(0)
	body.writeByte(0)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 1)
	if err != nil || added != 0 || len(p.Samples) != 0 {
		t.Fatalf("invalid position added=%d samples=%v err=%v", added, p.Samples, err)
	}
}
