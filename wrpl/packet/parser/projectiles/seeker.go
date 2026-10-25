package projectiles

import "math"

const (
	seekerRadarBits           = 607
	seekerRadarTransitionBits = 639
	seekerIRBits              = 283
)

// SeekerKind identifies only the fixed layouts established for the in-flight
// WeaponSync seeker block. It does not infer guidance from a missile model.
type SeekerKind string

const (
	SeekerKindUnknown SeekerKind = "Unknown"
	SeekerKindRadar   SeekerKind = "Radar"
	SeekerKindIR      SeekerKind = "IR"
)

// SeekerMode is the state carried by a radar block. IR blocks have no lock
// bit, so their mode remains Unknown even while a line of sight is available.
type SeekerMode string

const (
	SeekerModeUnknown SeekerMode = "Unknown"
	SeekerModeSearch  SeekerMode = "Search"
	SeekerModeTrack   SeekerMode = "Track"
)

// SeekerState is a validated interpretation of one opaque seeker block. The
// wire bytes are always retained. Value flags are false when the layout is
// unknown or a field fails finite/range/line-of-sight validation.
type SeekerState struct {
	Kind       SeekerKind
	Mode       SeekerMode
	Transition bool
	BitLength  uint16
	// RawBits uses the exact danet.ReadBits representation: full bytes are
	// high-aligned and the final partial byte is right-aligned. BitLength is
	// the authoritative consumed length.
	RawBits           []byte
	TargetEstimate    [3]float32
	HasTargetEstimate bool
	LineOfSight       [3]float32
	HasLineOfSight    bool
	RangeMeters       uint16
	HasRange          bool
}

// SeekerState returns a copy of the retained block and its safely decoded
// fields. The boolean is true whenever a complete unguided tail contains a
// seeker block, including an unrecognized layout. Unknown layouts expose the
// raw bytes and length while leaving all value flags false.
func (m RawMotion) SeekerState() (SeekerState, bool) {
	if !m.Complete || !m.HasTail || m.HasGuidance || !m.HasSeekerBlock {
		return SeekerState{}, false
	}
	if len(m.SeekerRawBits) < (int(m.SeekerBitLength)+7)/8 {
		return SeekerState{}, false
	}
	state := SeekerState{
		Kind:      SeekerKindUnknown,
		Mode:      SeekerModeUnknown,
		BitLength: m.SeekerBitLength,
		RawBits:   append([]byte(nil), m.SeekerRawBits...),
	}
	switch m.SeekerBitLength {
	case seekerRadarBits:
		state.Kind = SeekerKindRadar
		mode := uint64(0)
		var ok bool
		if mode, ok = readSeekerMSBBits(state.RawBits, state.BitLength, 8, 3); !ok {
			return state, true
		}
		switch mode {
		case 0b111:
			state.Mode = SeekerModeTrack
		case 0b001:
			state.Mode = SeekerModeSearch
		default:
			return state, true
		}
		decodeRadarSeekerValues(&state, 0)
	case seekerRadarTransitionBits:
		state.Kind = SeekerKindRadar
		mode, ok := readSeekerMSBBits(state.RawBits, state.BitLength, 8, 3)
		if !ok || mode != 0b100 {
			return state, true
		}
		state.Mode = SeekerModeTrack
		state.Transition = true
		decodeRadarSeekerValues(&state, 32)
	case seekerIRBits:
		state.Kind = SeekerKindIR
		readSeekerLOS(&state, 58)
	}
	return state, true
}

func decodeRadarSeekerValues(state *SeekerState, shift uint16) {
	var estimate [3]float32
	for i := range estimate {
		bits, ok := readSeekerLEBits(state.RawBits, state.BitLength, int(11+shift)+i*32, 32)
		if !ok {
			return
		}
		estimate[i] = math.Float32frombits(uint32(bits))
	}
	if validSeekerPosition(estimate) {
		state.TargetEstimate = estimate
		state.HasTargetEstimate = true
	}
	readSeekerLOS(state, 340+shift)
	if value, ok := readSeekerLEBits(state.RawBits, state.BitLength, int(454+shift), 16); ok {
		state.RangeMeters = uint16(value)
		state.HasRange = true
	}
}

func readSeekerLOS(state *SeekerState, offset uint16) {
	var los [3]float32
	for i := range los {
		bits, ok := readSeekerLEBits(state.RawBits, state.BitLength, int(offset)+i*16, 16)
		if !ok {
			return
		}
		los[i] = float32(int16(uint16(bits))) / 32767
	}
	norm2 := float64(los[0])*float64(los[0]) + float64(los[1])*float64(los[1]) + float64(los[2])*float64(los[2])
	if !finiteSeekerValues(los[:]) || math.Abs(math.Sqrt(norm2)-1) > 0.02 {
		return
	}
	state.LineOfSight = los
	state.HasLineOfSight = true
}

func validSeekerPosition(values [3]float32) bool {
	if !finiteSeekerValues(values[:]) {
		return false
	}
	for _, value := range values {
		if math.Abs(float64(value)) > maxCoordinate {
			return false
		}
	}
	return true
}

func finiteSeekerValues(values []float32) bool {
	for _, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

// readSeekerLEBits reconstructs a little-endian multi-byte value from an
// arbitrary bit offset. Each wire byte is MSB first; bytes are assembled from
// least significant to most significant.
func readSeekerLEBits(raw []byte, bitLength uint16, offset, width int) (uint64, bool) {
	if width < 1 || width > 32 || offset < 0 || offset+width > int(bitLength) {
		return 0, false
	}
	if len(raw) < (int(bitLength)+7)/8 {
		return 0, false
	}
	var value uint64
	for byteIndex := 0; byteIndex*8 < width; byteIndex++ {
		var wireByte byte
		for bit := 0; bit < 8 && byteIndex*8+bit < width; bit++ {
			position := offset + byteIndex*8 + bit
			if raw[position/8]&(1<<uint(7-position%8)) != 0 {
				wireByte |= 1 << uint(7-bit)
			}
		}
		value |= uint64(wireByte) << uint(byteIndex*8)
	}
	return value, true
}

// readSeekerMSBBits reads a short bit field whose first wire bit is the most
// significant bit of the value, as used by the three-bit radar mode field.
func readSeekerMSBBits(raw []byte, bitLength uint16, offset, width int) (uint64, bool) {
	if width < 1 || width > 32 || offset < 0 || offset+width > int(bitLength) {
		return 0, false
	}
	if len(raw) < (int(bitLength)+7)/8 {
		return 0, false
	}
	var value uint64
	for bit := 0; bit < width; bit++ {
		position := offset + bit
		value <<= 1
		if raw[position/8]&(1<<uint(7-position%8)) != 0 {
			value |= 1
		}
	}
	return value, true
}
