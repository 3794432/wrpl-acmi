/*
	wrpl: War Thunder replay parsing library (golang)
	Copyright (C) 2025 flexcoral

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU Affero General Public License as published
	by the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU Affero General Public License for more details.

	You should have received a copy of the GNU Affero General Public License
	along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package packetkill

import (
	"fmt"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/game"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	ecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	fm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
	packetmovement "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/movement"
)

const (
	killPacketType        = byte(4)
	killMaxPacketBytes    = 1 << 16
	killMaxFieldBits      = uint32(1 << 20)
	killMaxRecords        = 1 << 16
	killUnknownUID        = uint16(0x7ff)
	killVictimField       = uint8(3)
	killKillerField       = uint8(4)
	killPlayerWeaponField = uint8(0xa)
)

type KillEntry struct {
	Seq         uint64
	CurrentTime uint32

	KillerPid              uint32
	HasKillerPID           bool
	KillerUid              uint16
	HasKillerUID           bool
	ResolvedKiller         *ecs2.Entity
	ResolvedKillerPosition *game.SpaceTime

	VictimPid              uint32
	HasVictimPID           bool
	VictimUid              uint16
	HasVictimUID           bool
	ResolvedVictim         *ecs2.Entity
	ResolvedVictimPosition *game.SpaceTime

	PlayerVehicle   string
	PlayerWeapon    string
	DestroyedWeapon string
}

type PacketKillParser struct {
	KeepKills   bool
	Kills       []KillEntry
	ECS         *ecs2.EntityManager
	PathsGround *packetmovement.PositionRetainerParser
	PathsAir    *fm.PacketFlightModelParser
}

func (p *PacketKillParser) Name() string {
	return "kill2"
}

func (p *PacketKillParser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	return map[byte][][]packet.ParsingCondition{
		killPacketType: {{
			packet.NewParsingCondition(0, 0x02),
			packet.NewParsingCondition(1, 0x58),
			packet.NewParsingCondition(2, 0x58),
			packet.NewParsingCondition(3, 0xf0),
		}},
	}
}

func (p *PacketKillParser) Parse(pk *packet.Packet) (any, error) {
	if p == nil {
		return nil, nil
	}
	parsed, err := parseKillEntry(pk)
	if err != nil {
		// A malformed report is omitted from Kills, but the packet parser can
		// continue with the rest of the replay.
		return nil, fmt.Errorf("parsing kill report: %w", err)
	}
	p.resolveEntities(&parsed)
	if p.KeepKills && len(p.Kills) < killMaxRecords {
		p.Kills = append(p.Kills, parsed)
	}
	return parsed, nil
}

// parseKillEntry follows the bounded field layout used by F058 reports. The
// size table is checked before any field body is read so hostile offsets,
// overflowing ULEBs and truncated strings cannot walk into another region.
func parseKillEntry(pk *packet.Packet) (KillEntry, error) {
	var parsed KillEntry
	if pk == nil {
		return parsed, fmt.Errorf("nil packet")
	}
	if pk.PacketType != killPacketType {
		return parsed, fmt.Errorf("unexpected packet type %d", pk.PacketType)
	}
	if len(pk.PacketPayload) < 8 || len(pk.PacketPayload) > killMaxPacketBytes {
		return parsed, fmt.Errorf("invalid payload length %d", len(pk.PacketPayload))
	}
	payload := pk.PacketPayload
	if payload[0] != 0x02 || payload[1] != 0x58 || payload[2] != 0x58 || payload[3] != 0xf0 {
		return parsed, fmt.Errorf("unexpected kill report signature")
	}

	r := danet.NewBitReader(payload[4:])
	offset, err := r.ReadU16LE()
	if err != nil {
		return parsed, fmt.Errorf("reading size-table offset: %w", err)
	}
	fieldsValue, ok := readKillULEB(r, uint64(^uint32(0)))
	if !ok {
		return parsed, fmt.Errorf("invalid field mask")
	}
	fieldMask := uint32(fieldsValue)
	bodyStart := r.BitOffset
	sizesStart := int(offset) * 8
	if sizesStart < bodyStart || sizesStart > len(r.Data)*8 {
		return parsed, fmt.Errorf("size-table offset %d is outside the payload", sizesStart)
	}

	var sizes [32]uint32
	r.BitOffset = sizesStart
	for field := uint8(0); field < 32; field++ {
		if fieldMask>>field&1 == 0 {
			continue
		}
		size, ok := readKillFieldSize(r)
		if !ok {
			return parsed, fmt.Errorf("invalid size for field %d", field)
		}
		sizes[field] = size
	}

	r.BitOffset = bodyStart
	for field := uint8(0); field < 32; field++ {
		if fieldMask>>field&1 == 0 {
			continue
		}
		fieldStart := r.BitOffset
		if fieldStart < bodyStart || fieldStart > sizesStart {
			return parsed, fmt.Errorf("field %d starts outside the body", field)
		}
		size := sizes[field]
		if size > uint32(sizesStart-fieldStart) {
			return parsed, fmt.Errorf("field %d exceeds the body", field)
		}
		fieldEnd := fieldStart + int(size)
		fr := &danet.BitReader{Data: r.Data, BitOffset: fieldStart}

		switch field {
		case 1:
			if size < 32 {
				return parsed, fmt.Errorf("killer PID field is too short")
			}
			parsed.KillerPid, err = fr.ReadU32LE()
			parsed.HasKillerPID = err == nil
		case 2:
			parsed.PlayerVehicle, err = readKillString(fr, size)
		case killVictimField:
			if size < 16 {
				return parsed, fmt.Errorf("victim UID field is too short")
			}
			parsed.VictimUid, err = fr.ReadU16LE()
			parsed.VictimUid &= 0x7ff
			parsed.HasVictimUID = err == nil
		case killKillerField:
			if size < 16 {
				return parsed, fmt.Errorf("killer UID field is too short")
			}
			parsed.KillerUid, err = fr.ReadU16LE()
			parsed.KillerUid &= 0x7ff
			parsed.HasKillerUID = err == nil
		case killPlayerWeaponField:
			parsed.PlayerWeapon, err = readKillString(fr, size)
		case 0xb:
			if size < 32 {
				return parsed, fmt.Errorf("victim PID field is too short")
			}
			parsed.VictimPid, err = fr.ReadU32LE()
			parsed.HasVictimPID = err == nil
		case 0xc:
			parsed.DestroyedWeapon, err = readKillString(fr, size)
		}
		if err != nil {
			return parsed, fmt.Errorf("reading field %d: %w", field, err)
		}
		if fr.BitOffset > fieldEnd {
			return parsed, fmt.Errorf("field %d read past its boundary", field)
		}
		r.BitOffset = fieldEnd
	}

	parsed.Seq = pk.Seq
	parsed.CurrentTime = pk.CurrentTime
	return parsed, nil
}

func readKillFieldSize(r *danet.BitReader) (uint32, bool) {
	header, err := r.ReadBits(3)
	if err != nil || len(header) != 1 {
		return 0, false
	}
	switch header[0] {
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
		size, ok := readKillULEB(r, uint64(killMaxFieldBits))
		return uint32(size), ok
	default:
		return 0, false
	}
}

// readKillULEB derives its byte bound from limit, then checks every partial
// value before shifting. This rejects unterminated and overflowing encodings.
func readKillULEB(r *danet.BitReader, limit uint64) (uint64, bool) {
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
		if shift >= 64 || value > limit || bits > (limit-value)>>shift {
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

func readKillString(r *danet.BitReader, fieldBits uint32) (string, error) {
	if fieldBits < 8 {
		return "", fmt.Errorf("string field is too short")
	}
	length, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	if uint64(length)*8+8 > uint64(fieldBits) {
		return "", fmt.Errorf("string length %d exceeds field size %d", length, fieldBits)
	}
	raw, err := r.ReadBytes(int(length))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (p *PacketKillParser) resolveEntities(parsed *KillEntry) {
	if parsed.HasVictimUID && parsed.VictimUid != killUnknownUID {
		parsed.ResolvedVictim = p.entityByUID(parsed.VictimUid)
		parsed.ResolvedVictimPosition = p.entityPosition(parsed.VictimUid, parsed.ResolvedVictim)
	}
	if parsed.HasKillerUID && parsed.KillerUid != killUnknownUID {
		parsed.ResolvedKiller = p.entityByUID(parsed.KillerUid)
		parsed.ResolvedKillerPosition = p.entityPosition(parsed.KillerUid, parsed.ResolvedKiller)
	}
}

func (p *PacketKillParser) entityByUID(uid uint16) *ecs2.Entity {
	if p == nil || p.ECS == nil {
		return nil
	}
	entity, ok := p.ECS.GetEntityByUid(int32(uid))
	if !ok {
		return nil
	}
	return entity
}

// entityPosition preserves the historical ground-path and latest-air-frame
// position lookups. Resolution is optional: an absent ECS entity or empty path
// leaves the corresponding pointer nil without rejecting the raw kill fields.
func (p *PacketKillParser) entityPosition(uid uint16, resolved *ecs2.Entity) *game.SpaceTime {
	if resolved == nil {
		return nil
	}
	var result *game.SpaceTime
	if p.PathsGround != nil && p.ECS != nil {
		for eid, path := range p.PathsGround.Paths {
			if len(path) == 0 {
				continue
			}
			eid2 := ((uint64(uint64(eid)&0xff) << uint64(0x16)) | (uint64(eid) >> uint64(0x8))) & 0x7FF
			eid2 = uint64(ecs2.EntityID(uint32(eid2)).Index())
			entity := p.ECS.Entities[uint32(eid2)]
			if entity == nil || entity != resolved {
				continue
			}
			position := path[len(path)-1]
			result = &position
			break
		}
	}
	if p.PathsAir != nil && len(p.PathsAir.Results) > 0 {
		latest := p.PathsAir.Results[len(p.PathsAir.Results)-1]
		for _, entry := range latest.Entries {
			if entry == nil || entry.Data == nil || entry.UID != uint64(uid) {
				continue
			}
			candidate := &game.SpaceTime{
				Time: latest.CurrentTime,
				X:    float64(entry.Data.PosX),
				Y:    float64(entry.Data.PosY),
				Z:    float64(entry.Data.PosZ),
			}
			if result == nil || result.Time < candidate.Time {
				result = candidate
			}
		}
	}
	return result
}
