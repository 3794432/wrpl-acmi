package main

import (
	"sort"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
)

const (
	aircraftDestructionPacketType       = byte(4)
	aircraftDestructionMaxPacketBytes   = 1 << 16
	aircraftDestructionMaxFieldBits     = uint32(1 << 20)
	aircraftDestructionMaxRecords       = 1 << 16
	aircraftDestructionVictimField      = uint8(3)
	aircraftDestructionUnknownVictimUID = uint16(0x7ff)
)

type aircraftDestruction struct {
	Time      uint32
	VictimUID uint64
}

// aircraftDestructionParser uses the bounded field layout carried by F058
// reports. It deliberately avoids ECS lookups so a hostile replay cannot turn
// lifecycle resolution into an unbounded allocation path.
type aircraftDestructionParser struct {
	Deaths  []aircraftDestruction
	Skipped uint64
}

func newAircraftDestructionParser() *aircraftDestructionParser {
	return &aircraftDestructionParser{}
}

func (p *aircraftDestructionParser) Name() string { return "aircraft_destructions" }

func (p *aircraftDestructionParser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	return map[byte][][]packet.ParsingCondition{
		aircraftDestructionPacketType: {{
			packet.NewParsingCondition(0, 0x02),
			packet.NewParsingCondition(1, 0x58),
			packet.NewParsingCondition(2, 0x58),
			packet.NewParsingCondition(3, 0xf0),
		}},
	}
}

func (p *aircraftDestructionParser) Parse(pk *packet.Packet) (any, error) {
	if p == nil {
		return nil, nil
	}
	death, ok := parseAircraftDestruction(pk)
	if !ok {
		p.Skipped++
		return nil, nil
	}
	if len(p.Deaths) >= aircraftDestructionMaxRecords {
		p.Skipped++
		return nil, nil
	}
	p.Deaths = append(p.Deaths, death)
	return death, nil
}

func parseAircraftDestruction(pk *packet.Packet) (aircraftDestruction, bool) {
	var result aircraftDestruction
	if pk == nil || pk.PacketType != aircraftDestructionPacketType ||
		len(pk.PacketPayload) < 8 || len(pk.PacketPayload) > aircraftDestructionMaxPacketBytes {
		return result, false
	}

	payload := pk.PacketPayload
	if payload[0] != 0x02 || payload[1] != 0x58 || payload[2] != 0x58 || payload[3] != 0xf0 {
		return result, false
	}

	r := danet.NewBitReader(payload[4:])
	offset, err := r.ReadU16LE()
	if err != nil {
		return result, false
	}
	fields, ok := readAircraftDestructionULEB(r, uint64(^uint32(0)))
	if !ok || fields == 0 {
		return result, false
	}
	fieldMask := uint32(fields)
	if fieldMask>>aircraftDestructionVictimField&1 == 0 {
		return result, false
	}

	bodyStart := r.BitOffset
	sizesStart := uint64(offset) * 8
	payloadBits := uint64(len(r.Data)) * 8
	if sizesStart < uint64(bodyStart) || sizesStart > payloadBits {
		return result, false
	}

	var sizes [32]uint32
	r.BitOffset = int(sizesStart)
	for field := uint8(0); field < 32; field++ {
		if fieldMask>>field&1 == 0 {
			continue
		}
		size, ok := readAircraftDestructionFieldSize(r)
		if !ok {
			return result, false
		}
		sizes[field] = size
	}
	if sizes[aircraftDestructionVictimField] < 16 {
		return result, false
	}

	r.BitOffset = bodyStart
	for field := uint8(0); field < 32 && fieldMask != 0; field++ {
		if fieldMask>>field&1 == 0 {
			continue
		}
		fieldStart := r.BitOffset
		if fieldStart < 0 || fieldStart > len(r.Data)*8 {
			return result, false
		}
		remaining := len(r.Data)*8 - fieldStart
		size := sizes[field]
		if size > uint32(remaining) {
			return result, false
		}
		fieldEnd := fieldStart + int(size)
		if fieldEnd > int(sizesStart) {
			return result, false
		}
		if field == aircraftDestructionVictimField {
			raw, err := r.ReadU16LE()
			if err != nil {
				return result, false
			}
			victim := raw & 0x7ff
			if victim == aircraftDestructionUnknownVictimUID {
				return result, false
			}
			result.VictimUID = uint64(victim)
		}
		r.BitOffset = fieldEnd
	}

	result.Time = pk.CurrentTime
	return result, true
}

func readAircraftDestructionFieldSize(r *danet.BitReader) (uint32, bool) {
	headerBytes, err := r.ReadBits(3)
	if err != nil || len(headerBytes) != 1 {
		return 0, false
	}
	switch headerBytes[0] {
	case 1:
		return 1, true
	case 2:
		return 8, true
	case 3:
		return 16, true
	case 4:
		return 32, true
	case 5:
		return 64, true
	case 6:
		return 96, true
	case 7:
		return 128, true
	case 0:
		size, ok := readAircraftDestructionULEB(r, uint64(aircraftDestructionMaxFieldBits))
		return uint32(size), ok
	default:
		return 0, false
	}
}

// readAircraftDestructionULEB rejects truncated and overflowing encodings
// before they can be confused with a smaller field mask or size.
func readAircraftDestructionULEB(r *danet.BitReader, limit uint64) (uint64, bool) {
	maxBytes := 1
	for remaining := limit; remaining > 0x7f; remaining >>= 7 {
		maxBytes++
	}
	var value uint64
	for i := 0; i < maxBytes; i++ {
		part, err := r.ReadByte()
		if err != nil {
			return 0, false
		}
		bits := uint64(part & 0x7f)
		shift := uint(i * 7)
		if value > limit || bits > (limit-value)>>shift {
			return 0, false
		}
		value += bits << shift
		if value > limit {
			return 0, false
		}
		if part&0x80 == 0 {
			return value, true
		}
	}
	return 0, false
}

func applyAircraftDestructions(tracks []*track, parser *aircraftDestructionParser) int {
	if parser == nil || len(parser.Deaths) == 0 {
		return 0
	}

	byVictim := make(map[uint64][]uint32)
	for _, death := range parser.Deaths {
		if death.VictimUID == uint64(aircraftDestructionUnknownVictimUID) {
			continue
		}
		byVictim[death.VictimUID] = append(byVictim[death.VictimUID], death.Time)
	}
	for victim := range byVictim {
		sort.Slice(byVictim[victim], func(i, j int) bool {
			return byVictim[victim][i] < byVictim[victim][j]
		})
	}

	applied := 0
	for _, tr := range tracks {
		if tr == nil || !tr.key.air || tr.key.missile || tr.key.decoy || len(tr.samples) == 0 {
			continue
		}
		deaths := byVictim[tr.key.id]
		firstSample := tr.samples[0].t
		killTime, ok := firstAircraftDestructionAtOrAfter(deaths, firstSample)
		if !ok {
			continue
		}
		if tr.hasEnd && tr.end <= killTime {
			continue
		}
		tr.end = killTime
		tr.hasEnd = true
		tr.retired = false
		applied++
	}
	return applied
}

func firstAircraftDestructionAtOrAfter(deaths []uint32, firstSample uint32) (uint32, bool) {
	for _, death := range deaths {
		if death >= firstSample {
			return death, true
		}
	}
	return 0, false
}

var _ packet.PacketParser = (*aircraftDestructionParser)(nil)
