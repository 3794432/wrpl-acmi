package projectiles

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
)

const (
	generalObjectID         = 0x5802
	extendedGeneralObjectID = 0x0fff
	rocketPositions         = 0xf11a
	shellPositions          = 0xf0db
	maxCoordinate           = 10_000_000.0
	maxULEBBytes            = 10
	// WeaponSync stores each class count in one byte. Keep the limit explicit
	// so a future wider count cannot silently drive an unbounded allocation.
	maxWeaponRecordsPerClass = 1<<8 - 1
)

// Sample is one projectile position emitted by a WeaponSync MPI message.
type Sample struct {
	Time     uint32
	X        float64
	Y        float64
	Z        float64
	FromSync bool
	// RawMotion is the fixed-size scalar prefix of the optional position tail.
	// It is nil when the record has no extra tail. The field names deliberately
	// describe wire values; they do not assign them a velocity interpretation.
	RawMotion *RawMotion
}

// RawMotion retains the three float and six uint16 values read directly from
// the optional WeaponSync tail. Complete is true only after the entire tail,
// including any seeker branch, has been consumed successfully. The seeker
// timer float is retained by its exact bits; the remaining seeker payload is
// consumed but not retained.
type RawMotion struct {
	Complete    bool
	HasGuidance bool
	HasTail     bool
	// VectorBits preserves the exact float32 wire payload. It is the canonical
	// representation because NaN and infinity are valid bit patterns and cannot
	// be emitted as ordinary JSON numbers.
	VectorBits      [3]uint32
	Quantized       [2][3]uint16
	HasSeekerValue  bool
	SeekerValueBits uint32
}

func (m RawMotion) VectorValues() [3]float32 {
	var values [3]float32
	for i, bits := range m.VectorBits {
		values[i] = math.Float32frombits(bits)
	}
	return values
}

// SeekerValue returns the raw seeker timer value only when the complete,
// unguided tail contains a finite non-negative value. The wire value is already
// seconds; 1/48 second is its quantization step, so callers must not divide it
// by 48 again.
func (m RawMotion) SeekerValue() (float32, bool) {
	if !m.Complete || !m.HasTail || !m.HasSeekerValue || m.HasGuidance {
		return 0, false
	}
	value := math.Float32frombits(m.SeekerValueBits)
	if !finiteFloat32(value) || value < 0 {
		return 0, false
	}
	return value, true
}

// MarshalJSON keeps raw tail evidence serializable when the wire vector
// contains NaN or infinity. Finite values are also exposed numerically for
// correlation checks; every component retains its exact bits as hex.
func (m RawMotion) MarshalJSON() ([]byte, error) {
	type wire struct {
		Complete        bool         `json:"complete"`
		HasGuidance     bool         `json:"hasGuidance"`
		HasTail         bool         `json:"hasTail"`
		HasSeekerValue  bool         `json:"hasSeekerValue"`
		Vector          []any        `json:"vector"`
		VectorBits      [3]string    `json:"vector_bits"`
		Quantized       [2][3]uint16 `json:"quantized"`
		SeekerValue     *any         `json:"seeker_value,omitempty"`
		SeekerValueBits *string      `json:"seeker_value_bits,omitempty"`
	}
	out := wire{
		Complete:       m.Complete,
		HasGuidance:    m.HasGuidance,
		HasTail:        m.HasTail,
		HasSeekerValue: m.HasSeekerValue,
		Vector:         make([]any, len(m.VectorBits)),
		Quantized:      m.Quantized,
	}
	for i, bits := range m.VectorBits {
		out.VectorBits[i] = fmt.Sprintf("0x%08x", bits)
		value := math.Float32frombits(bits)
		if finiteFloat32(value) {
			out.Vector[i] = float64(value)
		}
	}
	if m.HasSeekerValue {
		var value any
		seekerValue := math.Float32frombits(m.SeekerValueBits)
		if finiteFloat32(seekerValue) {
			value = float64(seekerValue)
		}
		bits := fmt.Sprintf("0x%08x", m.SeekerValueBits)
		out.SeekerValue = &value
		out.SeekerValueBits = &bits
	}
	return json.Marshal(out)
}

// PositionParser retains WeaponSync samples by the exact 32-bit entity value
// read from each record. Samples contains class-0 rockets only; BombSamples
// contains class-1 bombs only. Torpedo records are consumed but not stored.
// Keeping bombs in a separate map prevents them from entering missile and
// countermeasure joins. Mapping the raw value to an ECS identity is separate.
type PositionParser struct {
	Samples     map[uint64][]Sample
	BombSamples map[uint64][]Sample
}

func NewPositionParser() *PositionParser {
	return &PositionParser{
		Samples:     make(map[uint64][]Sample),
		BombSamples: make(map[uint64][]Sample),
	}
}

func (p *PositionParser) Name() string { return "projectile_positions" }

func (p *PositionParser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	return map[byte][][]packet.ParsingCondition{
		4: {
			{
				packet.NewParsingCondition(0, 0x02),
				packet.NewParsingCondition(1, 0x58),
			},
			{
				packet.NewParsingCondition(0, 0xff),
				packet.NewParsingCondition(1, 0x0f),
			},
		},
	}
}

// Parse consumes ShellsData/ShellsDataServerReplay (Rocket2/Rocket1). The
// WeaponSync body has one byte-counted group for rockets (class 0) and one for
// bombs (class 1), followed by torpedoes (class 2). A point is stored only
// after its entire record, including any optional tail, has decoded
// successfully. Earlier valid records remain available on a later record
// error.
func (p *PositionParser) Parse(pk *packet.Packet) (any, error) {
	if p.Samples == nil {
		p.Samples = make(map[uint64][]Sample)
	}
	if p.BombSamples == nil {
		p.BombSamples = make(map[uint64][]Sample)
	}
	r := danet.NewBitReader(pk.PacketPayload)
	recognized, err := consumeObjectHeader(r)
	if err != nil {
		return nil, err
	}
	if !recognized {
		return nil, nil
	}
	messageID, err := r.ReadU16LE()
	if err != nil {
		return nil, fmt.Errorf("reading MPI message id: %w", err)
	}
	if messageID != rocketPositions && messageID != shellPositions {
		return nil, nil
	}
	// BSMessage serializes a nested BitStream: a compressed bit length followed
	// by byte-aligned data (danet::BitStream::Read(BitStream&)).
	bits, err := readULEB(r, "projectile bitstream length")
	if err != nil {
		return nil, err
	}
	r.AlignToByteBoundary()
	if r.BitOffset < 0 || r.BitOffset > len(r.Data)*8 {
		return nil, fmt.Errorf("projectile bitstream starts beyond packet: bit %d of %d", r.BitOffset, len(r.Data)*8)
	}
	available := len(r.Data)*8 - r.BitOffset
	if bits > uint64(available) {
		return nil, fmt.Errorf("projectile bitstream length %d bits exceeds %d available", bits, available)
	}
	body, err := r.ReadBits(int(bits))
	if err != nil {
		return nil, fmt.Errorf("reading projectile bitstream: %w", err)
	}
	r = danet.NewBitReader(body)

	added := 0
	for class := 0; class < 3; class++ {
		count, err := r.ReadByte()
		if err != nil {
			return added, fmt.Errorf("reading projectile class %d count: %w", class, err)
		}
		// The wire count is one byte, so each class contributes at most 255
		// records per packet. No slice is sized from any larger untrusted value.
		if int(count) > maxWeaponRecordsPerClass {
			return added, fmt.Errorf("projectile class %d count %d exceeds limit %d", class, count, maxWeaponRecordsPerClass)
		}
		for i := 0; i < int(count); i++ {
			sample, ok, err := parseWeapon(r, pk.CurrentTime)
			if err != nil {
				return added, fmt.Errorf("class %d record %d: %w", class, i, err)
			}
			if !ok {
				continue
			}
			key := uint64(sample.entity)
			switch class {
			case 0:
				p.Samples[key] = append(p.Samples[key], sample.Sample)
				added++
			case 1:
				p.BombSamples[key] = append(p.BombSamples[key], sample.Sample)
				added++
			}
		}
	}
	return added, nil
}

type weaponSample struct {
	Sample
	entity uint32
}

// consumeObjectHeader accepts only the object IDs selected by ParsesMatching:
// GeneralSync (0x5802) and its extended 0x0fff form.
func consumeObjectHeader(r *danet.BitReader) (bool, error) {
	oid, err := r.ReadU16LE()
	if err != nil {
		return false, nil
	}
	switch oid {
	case generalObjectID:
		return true, nil
	case extendedGeneralObjectID:
		if _, err := readULEB(r, "extended MPI object id"); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func parseWeapon(r *danet.BitReader, time uint32) (weaponSample, bool, error) {
	hasGuidance, err := readBool(r)
	if err != nil {
		return weaponSample{}, false, fmt.Errorf("reading guidance flag: %w", err)
	}
	noExtraTail, err := readBool(r)
	if err != nil {
		return weaponSample{}, false, fmt.Errorf("reading tail flag: %w", err)
	}
	// WeaponSync uses BitStream::Read(EntityId), a fixed 32-bit field. The
	// replay's packed handle layout does not match packet.ReadEID.
	rawEntity, err := r.ReadU32LE()
	if err != nil {
		return weaponSample{}, false, fmt.Errorf("reading projectile entity value: %w", err)
	}
	isRaw, err := readBool(r)
	if err != nil {
		return weaponSample{}, false, fmt.Errorf("reading position encoding flag: %w", err)
	}

	var x, y, z float64
	if isRaw {
		x, err = readFloat32(r)
		if err == nil {
			y, err = readFloat32(r)
		}
		if err == nil {
			z, err = readFloat32(r)
		}
		if err != nil {
			return weaponSample{}, false, fmt.Errorf("reading raw position: %w", err)
		}
	} else {
		compressed1, readErr := r.ReadU32LE()
		if readErr != nil {
			return weaponSample{}, false, fmt.Errorf("reading compressed X: %w", readErr)
		}
		compressed2, readErr := r.ReadU32LE()
		if readErr != nil {
			return weaponSample{}, false, fmt.Errorf("reading compressed YZ: %w", readErr)
		}
		x, y, z = decodeCompressedPosition(compressed1, compressed2)
	}

	var rawMotion *RawMotion
	if !noExtraTail {
		rawMotion, err = consumeWeaponTail(r, hasGuidance)
		if err != nil {
			return weaponSample{}, false, err
		}
	}
	if !validPosition(x, y, z) {
		return weaponSample{}, false, nil
	}
	return weaponSample{
		Sample: Sample{
			Time:      time,
			X:         x,
			Y:         y,
			Z:         z,
			FromSync:  true,
			RawMotion: rawMotion,
		},
		entity: rawEntity,
	}, true, nil
}

func consumeWeaponTail(r *danet.BitReader, hasGuidance bool) (*RawMotion, error) {
	if _, err := readULEB(r, "tail zig-zag value"); err != nil {
		return nil, fmt.Errorf("reading tail value: %w", err)
	}
	raw := &RawMotion{HasGuidance: hasGuidance, HasTail: true}
	for i := 0; i < 3; i++ {
		value, err := r.ReadU32LE()
		if err != nil {
			return nil, fmt.Errorf("reading tail vector: %w", err)
		}
		raw.VectorBits[i] = value
	}
	for i := 0; i < 6; i++ {
		value, err := r.ReadU16LE()
		if err != nil {
			return nil, fmt.Errorf("reading tail quantized vector: %w", err)
		}
		raw.Quantized[i/3][i%3] = value
	}
	hasValue, err := readBool(r)
	if err != nil {
		return nil, fmt.Errorf("reading tail value flag: %w", err)
	}
	if hasValue {
		if _, err := r.ReadU16LE(); err != nil {
			return nil, fmt.Errorf("reading tail value: %w", err)
		}
	}
	flags, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("reading tail flags: %w", err)
	}
	for flag := byte(1); flag <= 8; flag <<= 1 {
		if flags&flag == 0 {
			continue
		}
		if _, err := readULEB(r, "tail compressed value"); err != nil {
			return nil, fmt.Errorf("reading tail compressed value: %w", err)
		}
	}
	if !hasGuidance {
		valueBits, err := r.ReadU32LE()
		if err != nil {
			return nil, fmt.Errorf("reading seeker value: %w", err)
		}
		raw.HasSeekerValue = true
		raw.SeekerValueBits = valueBits
		if _, err := readBool(r); err != nil {
			return nil, fmt.Errorf("reading seeker flag: %w", err)
		}
		size, err := r.ReadU16LE()
		if err != nil {
			return nil, fmt.Errorf("reading seeker size: %w", err)
		}
		if _, err := r.ReadBits(int(size)); err != nil {
			return nil, fmt.Errorf("reading seeker payload: %w", err)
		}
	} else {
		saclos, err := readBool(r)
		if err != nil {
			return nil, fmt.Errorf("reading SACLOS flag: %w", err)
		}
		if saclos {
			return nil, fmt.Errorf("SACLOS guidance payload is not decoded")
		}
	}
	raw.Complete = true
	return raw, nil
}

// readULEB bounds compressed integers to ten bytes and rejects 64-bit overflow.
func readULEB(r *danet.BitReader, what string) (uint64, error) {
	var value uint64
	for i := 0; i < maxULEBBytes; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("reading %s: %w", what, err)
		}
		if i == maxULEBBytes-1 && b&0xfe != 0 {
			return 0, fmt.Errorf("%s overflows uint64", what)
		}
		value |= uint64(b&0x7f) << (i * 7)
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("%s is too long", what)
}

func decodeCompressedPosition(compressed1, compressed2 uint32) (float64, float64, float64) {
	const scale22 = 1.0 / 2_097_152.0 // 1 / 2^21
	const scale20 = 1.0 / 524_288.0   // 1 / 2^19
	x := float64(compressed1>>10)*scale22 - 1
	y := float64(((compressed1&0x3ff)<<10)|(compressed2>>22))*scale20 - 1
	z := float64(compressed2&0x003fffff)*scale22 - 1
	return clampUnit(x) * 15_000, clampUnit(y) * 15_000, clampUnit(z) * 15_000
}

func clampUnit(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func validPosition(x, y, z float64) bool {
	return finiteBounded(x) && finiteBounded(y) && finiteBounded(z)
}

func finiteBounded(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= maxCoordinate
}

func readFloat32(r *danet.BitReader) (float64, error) {
	v, err := r.ReadU32LE()
	if err != nil {
		return 0, err
	}
	return float64(math.Float32frombits(v)), nil
}

func readBool(r *danet.BitReader) (bool, error) {
	b, err := r.ReadBits(1)
	if err != nil {
		return false, err
	}
	return b[0]&1 != 0, nil
}

var _ packet.PacketParser = (*PositionParser)(nil)
