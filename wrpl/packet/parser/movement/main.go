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

package packetmovement

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/game"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

type PositionRetainerParser struct {
	// Paths keeps the historical packed-identity keys used by existing
	// collectors. New callers should use GroundPaths for synchronized ground
	// movement.
	Paths map[uint64][]game.SpaceTime

	// GroundPaths contains GroundModelPositions samples. Ground units without
	// ECS construction metadata are keyed explicitly in the UID namespace:
	// (uint64(1)<<63)|uid. UID zero is a valid ground unit.
	GroundPaths map[uint64][]game.SpaceTime
	// GroundEntities contains construction instances associated with a ground
	// UID. The manager only retains the latest index instance, so this map is a
	// snapshot taken from the matching construction event.
	GroundEntities map[uint64]*packetecs2.Entity
	// GroundEnds contains only destruction times observed for an associated EID.
	GroundEnds map[uint64]uint32

	ecs               *packetecs2.PacketECSParser
	groundEnabled     bool
	deltaHistories    map[uint16]*groundDeltaHistory
	deltaHistoryBytes int
	groundEIDToUID    map[uint64]uint16
}

func NewPositionRetainerParser() *PositionRetainerParser {
	return newPositionRetainerParser(nil, false)
}

// NewPositionRetainerParserWithECS enables ground synchronization and binds
// construction instances by the full EID carried in each ECS message. The ECS
// parser must run before this parser when both match the same packet.
func NewPositionRetainerParserWithECS(ecs *packetecs2.PacketECSParser) *PositionRetainerParser {
	return newPositionRetainerParser(ecs, true)
}

func newPositionRetainerParser(ecs *packetecs2.PacketECSParser, groundEnabled bool) *PositionRetainerParser {
	return &PositionRetainerParser{
		Paths:          map[uint64][]game.SpaceTime{},
		GroundPaths:    map[uint64][]game.SpaceTime{},
		GroundEntities: map[uint64]*packetecs2.Entity{},
		GroundEnds:     map[uint64]uint32{},
		ecs:            ecs,
		groundEnabled:  groundEnabled,
		deltaHistories: map[uint16]*groundDeltaHistory{},
		groundEIDToUID: map[uint64]uint16{},
	}
}

func (p *PositionRetainerParser) Name() string {
	return "paths"
}

func (p *PositionRetainerParser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	groundChecks := [][]packet.ParsingCondition{{
		packet.NewParsingCondition(0, 0x02),
		packet.NewParsingCondition(1, 0x58),
		packet.NewParsingCondition(2, 0x73),
		packet.NewParsingCondition(3, 0xf0),
	}, {
		packet.NewParsingCondition(0, 0x02),
		packet.NewParsingCondition(1, 0x58),
		packet.NewParsingCondition(2, 0x74),
		packet.NewParsingCondition(3, 0xf0),
	}}
	legacyChecks := [][]packet.ParsingCondition{{
		packet.NewParsingCondition(0, 0xff),
		packet.NewParsingCondition(1, 0x0f),
		packet.NewParsingCondition(5, 0xa3),
		packet.NewParsingCondition(6, 0xf0),
		packet.NewParsingCondition(10, 0x00),
		packet.NewParsingCondition(11, 0x00),
		packet.NewParsingCondition(13, 0x13),
	}, {
		packet.NewParsingCondition(0, 0xff),
		packet.NewParsingCondition(1, 0x0f),
		packet.NewParsingCondition(4, 0xa3),
		packet.NewParsingCondition(5, 0xf0),
		packet.NewParsingCondition(9, 0x00),
		packet.NewParsingCondition(10, 0x00),
		packet.NewParsingCondition(12, 0x13),
	}}
	checks := legacyChecks
	ret := map[byte][][]packet.ParsingCondition{4: checks}
	if p.groundEnabled {
		ret[4] = append(checks, groundChecks...)
		ret[6] = nil
	}
	return ret
}

func (p *PositionRetainerParser) Parse(pk *packet.Packet) (any, error) {
	if pk == nil {
		return nil, nil
	}
	switch pk.PacketType {
	case 4:
		if isGroundMPI(pk.PacketPayload) {
			return p.parseGroundPositions(pk)
		}
		return p.parseLegacyPosition(pk)
	case 6:
		return p.observeECS(pk)
	default:
		return nil, nil
	}
}

func isGroundMPI(payload []byte) bool {
	return len(payload) >= 4 && payload[0] == 0x02 && payload[1] == 0x58 &&
		(payload[2] == 0x73 || payload[2] == 0x74) && payload[3] == 0xf0
}

func (p *PositionRetainerParser) parseLegacyPosition(pk *packet.Packet) (any, error) {
	payload := pk.PacketPayload
	if len(payload) < 40 {
		return nil, nil
	}
	if !matchesLegacy(payload) {
		return nil, nil
	}
	r := danet.NewBitReader(payload[2:])
	eid, err := r.ReadCompressed()
	if err != nil {
		return nil, fmt.Errorf("reading legacy position EID at bit offset %d: %w", r.BitOffset, err)
	}
	byteOffset := r.BitOffset / 8
	const payloadEnd = 35
	if byteOffset < 0 || len(payload) < payloadEnd+byteOffset {
		return nil, fmt.Errorf("legacy position payload is truncated: need %d bytes, have %d", payloadEnd+byteOffset, len(payload))
	}
	st := game.SpaceTime{
		Time: pk.CurrentTime,
		X:    math.Float64frombits(binary.LittleEndian.Uint64(payload[11+byteOffset:])),
		Y:    math.Float64frombits(binary.LittleEndian.Uint64(payload[19+byteOffset:])),
		Z:    math.Float64frombits(binary.LittleEndian.Uint64(payload[27+byteOffset:])),
	}
	if !validPosition(st) {
		return nil, fmt.Errorf("legacy position contains a non-finite coordinate")
	}
	p.Paths[eid] = append(p.Paths[eid], st)
	return st, nil
}

func matchesLegacy(payload []byte) bool {
	if len(payload) <= 13 {
		return false
	}
	if payload[0] != 0xff || payload[1] != 0x0f {
		return false
	}
	return (payload[5] == 0xa3 && payload[6] == 0xf0 && payload[10] == 0 && payload[11] == 0 && payload[13] == 0x13) ||
		(payload[4] == 0xa3 && payload[5] == 0xf0 && payload[9] == 0 && payload[10] == 0 && payload[12] == 0x13)
}

func validPosition(st game.SpaceTime) bool {
	return !math.IsNaN(st.X) && !math.IsInf(st.X, 0) &&
		!math.IsNaN(st.Y) && !math.IsInf(st.Y, 0) &&
		!math.IsNaN(st.Z) && !math.IsInf(st.Z, 0)
}
