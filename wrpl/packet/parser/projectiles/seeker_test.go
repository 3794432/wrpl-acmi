package projectiles

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func newSeekerBits(length int) []byte {
	return make([]byte, (length+7)/8)
}

// putSeekerLEBits uses the seeker wire convention: each byte is MSB first and
// successive bytes are little-endian.
func putSeekerLEBits(raw []byte, offset, width int, value uint64) {
	for byteIndex := 0; byteIndex*8 < width; byteIndex++ {
		wireByte := byte(value >> uint(byteIndex*8))
		for bit := 0; bit < 8 && byteIndex*8+bit < width; bit++ {
			position := offset + byteIndex*8 + bit
			mask := byte(1 << uint(7-position%8))
			if wireByte&(1<<uint(7-bit)) != 0 {
				raw[position/8] |= mask
			} else {
				raw[position/8] &^= mask
			}
		}
	}
}

func putSeekerMSBBits(raw []byte, offset, width int, value uint64) {
	for bit := 0; bit < width; bit++ {
		position := offset + bit
		mask := byte(1 << uint(7-position%8))
		if value&(1<<uint(width-1-bit)) != 0 {
			raw[position/8] |= mask
		} else {
			raw[position/8] &^= mask
		}
	}
}

func putSeekerFloat(raw []byte, offset int, value float32) {
	putSeekerLEBits(raw, offset, 32, uint64(math.Float32bits(value)))
}

func putSeekerInt16(raw []byte, offset int, value int16) {
	putSeekerLEBits(raw, offset, 16, uint64(uint16(value)))
}

func writeSeekerTestTail(w *testBitWriter, raw []byte, bitLength uint16) {
	w.writeULEB(41)
	for _, value := range []float32{4, 5, 6} {
		w.writeFloat32(value)
	}
	for i := uint16(1); i <= 6; i++ {
		w.writeU16(i * 100)
	}
	w.writeBit(false)
	w.writeByte(0)
	w.writeBits(uint64(math.Float32bits(1.25)), 32)
	w.writeBit(true)
	w.writeU16(bitLength)
	for bit := 0; bit < int(bitLength); bit++ {
		w.writeBit(raw[bit/8]&(1<<uint(7-bit%8)) != 0)
	}
}

func seekerTestPacket(entity uint32, raw []byte, bitLength uint16, includeTrailingCounts bool) []byte {
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeBit(false)
	body.writeBit(false)
	body.writeRawEntity(entity)
	body.writeBit(true)
	body.writeFloat32(10)
	body.writeFloat32(20)
	body.writeFloat32(30)
	writeSeekerTestTail(body, raw, bitLength)
	if includeTrailingCounts {
		body.writeByte(0)
		body.writeByte(0)
	}
	return directPositionPacket(rocketPositions, body)
}

func radarSeekerBits(length int, mode uint64, shift int, estimate [3]float32, los [3]int16) []byte {
	raw := newSeekerBits(length)
	putSeekerMSBBits(raw, 8, 3, mode)
	for i, value := range estimate {
		putSeekerFloat(raw, 11+shift+i*32, value)
	}
	for i, value := range los {
		putSeekerInt16(raw, 340+shift+i*16, value)
	}
	putSeekerLEBits(raw, 454+shift, 16, 1234)
	return raw
}

func TestSeekerStateDecodesFixedLayouts(t *testing.T) {
	tests := []struct {
		name       string
		length     uint16
		mode       uint64
		shift      int
		wantKind   SeekerKind
		wantMode   SeekerMode
		transition bool
	}{
		{name: "radar search", length: 607, mode: 0b001, wantKind: SeekerKindRadar, wantMode: SeekerModeSearch},
		{name: "radar track", length: 607, mode: 0b111, wantKind: SeekerKindRadar, wantMode: SeekerModeTrack},
		{name: "radar acquisition transition", length: 639, mode: 0b100, shift: 32, wantKind: SeekerKindRadar, wantMode: SeekerModeTrack, transition: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const entity = uint32(0x001f8c02)
			estimate := [3]float32{-1200, 340, 560}
			los := [3]int16{32767, 0, 0}
			raw := radarSeekerBits(int(tt.length), tt.mode, tt.shift, estimate, los)
			p, added, err := parseOne(t, seekerTestPacket(entity, raw, tt.length, true), 1)
			if err != nil || added != 1 {
				t.Fatalf("added=%d err=%v", added, err)
			}
			motion := p.Samples[uint64(entity)][0].RawMotion
			if motion == nil || !motion.HasSeekerBlock || !motion.SeekerFlag || motion.SeekerBitLength != tt.length || len(motion.SeekerRawBits) == 0 {
				t.Fatalf("raw seeker metadata = %+v", motion)
			}
			state, ok := motion.SeekerState()
			if !ok || state.Kind != tt.wantKind || state.Mode != tt.wantMode || state.Transition != tt.transition {
				t.Fatalf("state = %+v, ok=%v", state, ok)
			}
			if !state.HasTargetEstimate || state.TargetEstimate != estimate {
				t.Fatalf("target estimate = %v, present=%v", state.TargetEstimate, state.HasTargetEstimate)
			}
			if !state.HasLineOfSight || state.LineOfSight != [3]float32{1, 0, 0} {
				t.Fatalf("line of sight = %v, present=%v", state.LineOfSight, state.HasLineOfSight)
			}
			if !state.HasRange || state.RangeMeters != 1234 {
				t.Fatalf("range = %v, present=%v", state.RangeMeters, state.HasRange)
			}
		})
	}

	t.Run("IR has line of sight but no lock bit", func(t *testing.T) {
		const entity = uint32(0x001f8c03)
		raw := newSeekerBits(283)
		for i, value := range []int16{0, 32767, 0} {
			putSeekerInt16(raw, 58+i*16, value)
		}
		p, added, err := parseOne(t, seekerTestPacket(entity, raw, 283, true), 1)
		if err != nil || added != 1 {
			t.Fatalf("added=%d err=%v", added, err)
		}
		state, ok := p.Samples[uint64(entity)][0].RawMotion.SeekerState()
		if !ok || state.Kind != SeekerKindIR || state.Mode != SeekerModeUnknown {
			t.Fatalf("IR state = %+v, ok=%v", state, ok)
		}
		if !state.HasLineOfSight || state.LineOfSight != [3]float32{0, 1, 0} {
			t.Fatalf("IR line of sight = %v, present=%v", state.LineOfSight, state.HasLineOfSight)
		}
		if state.HasTargetEstimate || state.HasRange {
			t.Fatalf("IR invented unsupported fields: %+v", state)
		}
	})
}

func TestUnknownSeekerLengthIsRetainedWithoutHardError(t *testing.T) {
	const entity = uint32(0x001f8c04)
	raw := []byte{0xa0, 0xc0}
	p, added, err := parseOne(t, seekerTestPacket(entity, raw, 12, true), 1)
	if err != nil || added != 1 {
		t.Fatalf("unknown short block added=%d err=%v", added, err)
	}
	motion := p.Samples[uint64(entity)][0].RawMotion
	if motion == nil || !motion.Complete || !motion.HasSeekerBlock || motion.SeekerBitLength != 12 {
		t.Fatalf("unknown block metadata = %+v", motion)
	}
	state, ok := motion.SeekerState()
	if !ok || state.Kind != SeekerKindUnknown || state.Mode != SeekerModeUnknown || state.BitLength != 12 {
		t.Fatalf("unknown state = %+v, ok=%v", state, ok)
	}
	if state.HasTargetEstimate || state.HasLineOfSight || state.HasRange {
		t.Fatalf("unknown layout invented values: %+v", state)
	}
	// danet.ReadBits keeps full bytes high-aligned and right-aligns the final
	// partial byte. The bit length remains the authoritative payload bound.
	if len(state.RawBits) != 2 || state.RawBits[0] != 0xa0 || state.RawBits[1] != 0x0c {
		t.Fatalf("raw bits = %x", state.RawBits)
	}
}

func TestDeclaredSeekerLengthMustFitBeforeRead(t *testing.T) {
	body := &testBitWriter{}
	body.writeByte(1)
	body.writeBit(false)
	body.writeBit(false)
	body.writeRawEntity(0x001f8c05)
	body.writeBit(true)
	body.writeFloat32(1)
	body.writeFloat32(2)
	body.writeFloat32(3)
	body.writeULEB(41)
	for _, value := range []float32{4, 5, 6} {
		body.writeFloat32(value)
	}
	for i := uint16(1); i <= 6; i++ {
		body.writeU16(i * 100)
	}
	body.writeBit(false)
	body.writeByte(0)
	body.writeBits(uint64(math.Float32bits(1.25)), 32)
	body.writeBit(true)
	body.writeU16(5)
	body.writeBits(0b101, 3)

	p, added, err := parseOne(t, directPositionPacket(rocketPositions, body), 1)
	if err == nil || added != 0 || len(p.Samples) != 0 {
		t.Fatalf("truncated block added=%d samples=%+v err=%v", added, p.Samples, err)
	}
	if !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), "remaining bits") {
		t.Fatalf("unexpected truncation error: %v", err)
	}
}

func TestSeekerInvalidNumbersDoNotBecomeDecodedValues(t *testing.T) {
	const entity = uint32(0x001f8c06)
	raw := radarSeekerBits(607, 0b111, 0, [3]float32{float32(math.NaN()), 2, 3}, [3]int16{0, 12000, 0})
	p, added, err := parseOne(t, seekerTestPacket(entity, raw, 607, true), 1)
	if err != nil || added != 1 {
		t.Fatalf("added=%d err=%v", added, err)
	}
	state, ok := p.Samples[uint64(entity)][0].RawMotion.SeekerState()
	if !ok || state.Kind != SeekerKindRadar || state.Mode != SeekerModeTrack {
		t.Fatalf("state = %+v, ok=%v", state, ok)
	}
	if state.HasTargetEstimate || state.HasLineOfSight {
		t.Fatalf("invalid numeric values passed validation: %+v", state)
	}
}

func TestSeekerJSONKeepsRawMetadataWithNonFiniteVector(t *testing.T) {
	motion := RawMotion{
		Complete:        true,
		HasTail:         true,
		HasSeekerValue:  true,
		SeekerValueBits: math.Float32bits(1.25),
		HasSeekerBlock:  true,
		SeekerBitLength: 12,
		SeekerRawBits:   []byte{0xa0, 0x0c},
		VectorBits:      [3]uint32{0x7fc00000, 0x7f800000, math.Float32bits(6)},
	}
	data, err := json.Marshal(motion)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"vector_bits":["0x7fc00000","0x7f800000","0x40c00000"]`,
		`"seeker_bit_length":12`,
		`"seeker_kind":"Unknown"`,
		`"seeker_state":"Unknown"`,
		`"seeker_bits":"a00c"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("JSON %s missing %s", data, want)
		}
	}
}
