package packetmovement

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/game"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

const (
	groundUIDNamespace       uint64 = 1 << 63
	maxGroundUnits                  = 1 << 11
	maxGroundPacketBytes            = 1 << 20
	maxGroundDeltaCompressed        = 16 << 10
	maxGroundDeltaRecord            = 64 << 10
	maxGroundHistoryTotal           = 32 << 20
)

// GroundKey returns the explicit UID namespace key used when a synchronized
// ground unit has no ECS construction metadata.
func GroundKey(uid uint16) uint64 { return groundUIDNamespace | uint64(uid) }

func (p *PositionRetainerParser) parseGroundPositions(pk *packet.Packet) (any, error) {
	r := danet.NewBitReader(pk.PacketPayload[4:])
	bodyBits, err := readCompressedLimited(r)
	if err != nil {
		return nil, fmt.Errorf("reading ground message bit length: %w", err)
	}
	alignReader(r)
	remaining := len(r.Data)*8 - r.BitOffset
	if r.BitOffset < 0 || bodyBits > uint64(maxGroundPacketBytes*8) || bodyBits > uint64(remaining) {
		return nil, fmt.Errorf("ground message length %d bits exceeds remaining %d bits", bodyBits, remaining)
	}
	body, err := r.ReadBits(int(bodyBits))
	if err != nil {
		return nil, fmt.Errorf("reading ground message body: %w", err)
	}
	g := danet.NewBitReader(body)
	lower, err := g.ReadU16LE()
	if err != nil {
		return nil, fmt.Errorf("reading ground UID lower bound: %w", err)
	}
	upper, err := g.ReadU16LE()
	if err != nil {
		return nil, fmt.Errorf("reading ground UID upper bound: %w", err)
	}
	if lower >= upper || upper > maxGroundUnits {
		return nil, fmt.Errorf("invalid ground UID range [%d,%d)", lower, upper)
	}
	if _, err = g.ReadU32LE(); err != nil {
		return nil, fmt.Errorf("reading ground sync time: %w", err)
	}
	if _, err = g.ReadU16LE(); err != nil {
		return nil, fmt.Errorf("reading ground reliability order: %w", err)
	}

	samples := make([]game.SpaceTime, 0)
	for uid := lower; uid < upper; uid++ {
		hasUnit, err := readBitStrict(g)
		if err != nil {
			return samples, fmt.Errorf("reading ground UID %d presence: %w", uid, err)
		}
		if !hasUnit {
			continue
		}
		if _, err = readBitStrict(g); err != nil {
			return samples, err
		}
		hasExtra, err := readBitStrict(g)
		if err != nil {
			return samples, err
		}
		if hasExtra {
			if _, err = readBitsStrict(g, 4); err != nil {
				return samples, err
			}
		}
		val2, err := readBitStrict(g)
		if err != nil {
			return samples, err
		}
		val3, err := readBitStrict(g)
		if err != nil {
			return samples, err
		}
		if val2 && val3 {
			continue
		}

		if _, err = readBitStrict(g); err != nil {
			return samples, err
		}
		val1, err := readBitStrict(g)
		if err != nil {
			return samples, err
		}
		turretCount, err := readBitsStrict(g, 8)
		if err != nil {
			return samples, err
		}
		compressed, err := readBitStrict(g)
		if err != nil {
			return samples, err
		}

		var vehicleData []byte
		if compressed {
			vehicleData, err = p.readGroundDelta(g, uid)
		} else {
			return samples, fmt.Errorf("UID %d uses unsupported uncompressed vehicle body", uid)
		}
		if err != nil {
			return samples, fmt.Errorf("reading ground UID %d delta: %w", uid, err)
		}
		if pos, ok := parseVehiclePosition(vehicleData, val1); ok {
			pos.Time = pk.CurrentTime
			key := GroundKey(uid)
			p.GroundPaths[key] = append(p.GroundPaths[key], pos)
			p.associateGroundEntity(uid, key)
			samples = append(samples, pos)
		}

		if compressed {
			// The outer stream continues after the delta bytes.
			hasPoint4, err := readBitStrict(g)
			if err != nil {
				return samples, err
			}
			if hasPoint4 {
				if _, err = readBitsStrict(g, 4*32); err != nil {
					return samples, err
				}
			}
			hasTurretData, err := readBitStrict(g)
			if err != nil {
				return samples, err
			}
			if hasTurretData {
				if err = skipGroundTurrets(g, int(turretCount)); err != nil {
					return samples, err
				}
			}
			hasSensor, err := readBitStrict(g)
			if err != nil {
				return samples, err
			}
			if hasSensor {
				hasSeeker, err := readBitStrict(g)
				if err != nil {
					return samples, err
				}
				if hasSeeker {
					if err = skipSeekerData(g); err != nil {
						return samples, err
					}
				}
			}
			alignReader(g)
		}
	}
	return samples, nil
}

func (p *PositionRetainerParser) readGroundDelta(r *danet.BitReader, uid uint16) ([]byte, error) {
	fullDiff, err := readBitStrict(r)
	if err != nil {
		return nil, err
	}
	packetNoDelta, err := readBitsStrict(r, 5)
	if err != nil {
		return nil, err
	}
	nextPacketNo, err := readBitsStrict(r, 13)
	if err != nil {
		return nil, err
	}
	size, err := readCompressedLimited(r)
	if err != nil {
		return nil, err
	}
	alignReader(r)
	if size > maxGroundDeltaCompressed || r.BitOffset < 0 || size > uint64((len(r.Data)*8-r.BitOffset)/8) {
		return nil, fmt.Errorf("delta size %d exceeds remaining bytes", size)
	}
	packed, err := r.ReadBytes(int(size))
	if err != nil {
		return nil, err
	}
	h := p.deltaHistories[uid]
	if h == nil {
		h = newGroundDeltaHistory()
		p.deltaHistories[uid] = h
	}
	return h.apply(fullDiff, packetNoDelta, nextPacketNo, packed, &p.deltaHistoryBytes)
}

func parseVehiclePosition(data []byte, hasDirectionFloats bool) (game.SpaceTime, bool) {
	if len(data) == 0 {
		return game.SpaceTime{}, false
	}
	r := danet.NewBitReader(data)
	if _, err := r.ReadByte(); err != nil {
		return game.SpaceTime{}, false
	}
	hasPosition, err := readBitStrict(r)
	if err != nil || !hasPosition {
		return game.SpaceTime{}, false
	}
	if hasDirectionFloats {
		floats := 4
		if _, err = readBitsStrict(r, floats*32); err != nil {
			return game.SpaceTime{}, false
		}
		history, err := readBitStrict(r)
		if err != nil {
			return game.SpaceTime{}, false
		}
		if history {
			if _, err = readBitsStrict(r, 21*32); err != nil {
				return game.SpaceTime{}, false
			}
		}
	} else if _, err = readBitsStrict(r, 3*16); err != nil {
		return game.SpaceTime{}, false
	}
	if _, err = readBitStrict(r); err != nil {
		return game.SpaceTime{}, false
	}
	full, err := readBitStrict(r)
	if err != nil {
		return game.SpaceTime{}, false
	}
	var x, y, z float32
	if full {
		for i := 0; i < 3; i++ {
			v, err := readFloat32(r)
			if err != nil {
				return game.SpaceTime{}, false
			}
			switch i {
			case 0:
				x = v
			case 1:
				y = v
			case 2:
				z = v
			}
		}
	} else {
		tv1, err := r.ReadU32LE()
		if err != nil {
			return game.SpaceTime{}, false
		}
		tv2, err := r.ReadU32LE()
		if err != nil {
			return game.SpaceTime{}, false
		}
		x = clampUnit(float32(tv1>>10)*float32(4.7683727e-7)-1) * 36000
		y = float32((tv1&0x3ff)<<10|tv2>>22)*float32(0.007629402) - 100
		z = clampUnit(float32(tv2&0x3fffff)*float32(4.7683727e-7)-1) * 36000
	}
	st := game.SpaceTime{X: float64(x), Y: float64(y), Z: float64(z)}
	if !finitePosition(st) || math.Abs(st.X) > 1e6 || math.Abs(st.Y) > 1e6 || math.Abs(st.Z) > 1e6 {
		return game.SpaceTime{}, false
	}
	return st, true
}

func clampUnit(v float32) float32 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func finitePosition(st game.SpaceTime) bool {
	return !math.IsNaN(st.X) && !math.IsInf(st.X, 0) && !math.IsNaN(st.Y) && !math.IsInf(st.Y, 0) && !math.IsNaN(st.Z) && !math.IsInf(st.Z, 0)
}

func readFloat32(r *danet.BitReader) (float32, error) {
	v, err := r.ReadU32LE()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(v), nil
}

func readBitStrict(r *danet.BitReader) (bool, error) {
	if r == nil || r.BitOffset < 0 || r.BitOffset >= len(r.Data)*8 {
		return false, fmt.Errorf("bit offset outside payload")
	}
	b, err := r.ReadBits(1)
	if err != nil {
		return false, err
	}
	return len(b) == 1 && b[0] != 0, nil
}

func readBitsStrict(r *danet.BitReader, n int) (uint32, error) {
	if n < 0 || n > maxGroundPacketBytes*8 {
		return 0, fmt.Errorf("invalid bit count %d", n)
	}
	if r == nil || r.BitOffset < 0 || n > len(r.Data)*8-r.BitOffset {
		return 0, fmt.Errorf("requested %d bits with insufficient payload", n)
	}
	b, err := r.ReadBits(n)
	if err != nil {
		return 0, err
	}
	if n > 32 {
		return 0, nil
	}
	var raw [4]byte
	copy(raw[:], b)
	return binary.LittleEndian.Uint32(raw[:]), nil
}

func readCompressedLimited(r *danet.BitReader) (uint64, error) {
	if r == nil {
		return 0, fmt.Errorf("nil reader")
	}
	var v uint64
	for i := 0; i < 10; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("compressed value exceeds ten bytes")
}

func alignReader(r *danet.BitReader) { r.BitOffset = (r.BitOffset + 7) &^ 7 }

func skipGroundTurrets(r *danet.BitReader, count int) error {
	if count < 0 || count > 255 {
		return fmt.Errorf("invalid turret count %d", count)
	}
	for i := 0; i < count; i++ {
		hasData, err := readBitStrict(r)
		if err != nil {
			return err
		}
		if !hasData {
			continue
		}
		if _, err = readBitsStrict(r, 8); err != nil {
			return err
		}
		if _, err = readBitsStrict(r, 32); err != nil {
			return err
		}
		if _, err = readBitsStrict(r, 32); err != nil {
			return err
		}
		ammo, err := readBitsStrict(r, 8)
		if err != nil {
			return err
		}
		if _, err = readBitsStrict(r, int(ammo)*16); err != nil {
			return err
		}
	}
	return nil
}

func skipSeekerData(r *danet.BitReader) error {
	size, err := r.ReadU32LE()
	if err != nil {
		return err
	}
	if uint64(size) > maxGroundPacketBytes*8 {
		return fmt.Errorf("seeker data size %d exceeds limit", size)
	}
	_, err = readBitsStrict(r, int(size))
	return err
}

func (p *PositionRetainerParser) associateGroundEntity(uid uint16, key uint64) {
	if p.ecs == nil || p.GroundEntities[key] != nil {
		return
	}
	entity, ok := p.ecs.Mgr.Uid_lookup[int32(uid)]
	if !ok || !isGroundEntity(entity) {
		return
	}
	p.GroundEntities[key] = entity
}

func (p *PositionRetainerParser) observeECS(pk *packet.Packet) (any, error) {
	if pk == nil || len(pk.PacketPayload) == 0 {
		return nil, nil
	}
	if p.ecs == nil {
		return nil, nil
	}
	p.bindGroundConstructions(pk.Seq)
	if pk.PacketPayload[0] != 0x26 {
		return nil, nil
	}
	if len(pk.PacketPayload) < 2 {
		return nil, fmt.Errorf("short ECS destruction packet")
	}
	count := int(pk.PacketPayload[1]) + 1
	if count > (len(pk.PacketPayload)-2)/2 {
		return nil, fmt.Errorf("ECS destruction count %d exceeds remaining payload", count)
	}
	r := danet.NewBitReader(pk.PacketPayload[2:])
	for i := 0; i < count; i++ {
		eid, err := packet.ReadEID(r)
		if err != nil {
			return nil, fmt.Errorf("reading destruction EID %d: %w", i, err)
		}
		if uid, ok := p.groundEIDToUID[eid]; ok {
			key := GroundKey(uid)
			if len(p.GroundPaths[key]) > 0 {
				p.GroundEnds[key] = pk.CurrentTime
			}
		}
	}
	return nil, nil
}

func (p *PositionRetainerParser) bindGroundConstructions(seq uint64) {
	for i := len(p.ecs.Messages) - 1; i >= 0; i-- {
		msg := &p.ecs.Messages[i]
		if msg.PacketSeq != seq {
			continue
		}
		for _, item := range msg.Messages {
			if len(item.Parsed) == 0 {
				continue
			}
			entity, ok := item.Parsed[0].(*packetecs2.Entity)
			if !ok || !isGroundEntity(entity) {
				continue
			}
			uid, ok := packetecs2.GetObjectData[int32](&entity.Data, "uid")
			if !ok || uid < 0 || uid >= maxGroundUnits {
				continue
			}
			key := GroundKey(uint16(uid))
			p.GroundEntities[key] = entity
			p.groundEIDToUID[item.EID] = uint16(uid)
		}
		return
	}
}

func isGroundEntity(entity *packetecs2.Entity) bool {
	if entity == nil {
		return false
	}
	t := strings.ToLower(entity.Template)
	for _, excluded := range []string{"aircraft", "plane", "rocket", "bomb", "missile", "payload", "jettisoned", "torpedo", "shell"} {
		if strings.Contains(t, excluded) {
			return false
		}
	}
	for _, included := range []string{"tank", "vehicle", "ship", "fortification", "infantry", "walker"} {
		if strings.Contains(t, included) {
			return true
		}
	}
	for _, tag := range []string{"unit_tag__tank", "unit_tag__light_vehicle", "unit_tag__fortification", "unit_tag__ship"} {
		if _, ok := entity.Data.GetData(tag); ok {
			return true
		}
	}
	return false
}
