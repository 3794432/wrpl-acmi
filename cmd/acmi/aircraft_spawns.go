package main

import (
	"encoding/binary"
	"math"
	"sort"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

type aircraftSpawn struct {
	Time    uint32
	UID     uint64
	Counter byte
	Entity  *packetecs2.Entity
}

// F030/F061/F093 carry explicit spawn counters. Extended aircraft MPI handles
// resolve against complete construction EIDs, retaining their generation bits.
// No position jump, sampling gap or player-list order is used as a spawn signal.
type aircraftSpawnParser struct {
	Spawns  map[uint64][]aircraftSpawn
	count   int
	ecs     *packetecs2.PacketECSParser
	owners  map[uint64]aircraftSpawnOwner
	pending []aircraftSpawnReference
}
type aircraftSpawnOwner struct {
	uid      uint64
	conflict bool
	entity   *packetecs2.Entity
}
type aircraftSpawnReference struct {
	eid     uint64
	t       uint32
	counter byte
}

func newAircraftSpawnParser(parsers ...*packetecs2.PacketECSParser) *aircraftSpawnParser {
	p := &aircraftSpawnParser{Spawns: map[uint64][]aircraftSpawn{}, owners: map[uint64]aircraftSpawnOwner{}}
	if len(parsers) > 0 {
		p.ecs = parsers[0]
	}
	return p
}
func (*aircraftSpawnParser) Name() string { return "aircraft_spawns" }
func (*aircraftSpawnParser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	return map[byte][][]packet.ParsingCondition{4: nil, 6: nil}
}
func (p *aircraftSpawnParser) Parse(pk *packet.Packet) (any, error) {
	if p == nil || pk == nil {
		return nil, nil
	}
	if pk.PacketType == 6 {
		p.observeConstructions(pk)
		return nil, nil
	}
	if pk.PacketType != 4 || len(pk.PacketPayload) < 7 || len(pk.PacketPayload) > 1<<16 || p.count >= 1<<16 {
		return nil, nil
	}
	b := pk.PacketPayload
	object := binary.LittleEndian.Uint16(b[:2])
	offset := 2
	var eid uint64
	if object&0x7ff == 0x7ff {
		r := danet.NewBitReader(b[2:])
		wire, err := readOptionalCompressed(r, 5)
		if err != nil {
			return nil, nil
		}
		var ok bool
		eid, ok = projectileEntityID(wire)
		if !ok {
			return nil, nil
		}
		offset += r.BitOffset / 8
	}
	if offset+2 > len(b) {
		return nil, nil
	}
	message := binary.LittleEndian.Uint16(b[offset : offset+2])
	offset += 2
	if message == 0xf061 && object>>11 == 0 {
		f, err := decodeOptionalIDField32(b[offset:], map[uint8]bool{1: true})
		if err != nil || len(f[1]) != 1 {
			return nil, nil
		}
		if object&0x7ff == 0x7ff {
			p.pending = append(p.pending, aircraftSpawnReference{eid: eid, t: pk.CurrentTime, counter: f[1][0]})
			p.count++
			return nil, nil
		}
		spawn := aircraftSpawn{Time: pk.CurrentTime, UID: uint64(object & 0x7ff), Counter: f[1][0]}
		p.Spawns[spawn.UID] = append(p.Spawns[spawn.UID], spawn)
		p.count++
		return spawn, nil
	}
	if message == 0xf093 && object == 0x5802 {
		f, err := decodeOptionalIDField32(b[offset:], map[uint8]bool{1: true, 2: true, 6: true})
		if err != nil || len(f[1]) != 2 || len(f[2]) != 4 || len(f[6]) != 1 {
			return nil, nil
		}
		uid := uint64(binary.LittleEndian.Uint16(f[1]))
		if uid >= 0x7ff {
			return nil, nil
		}
		spawn := aircraftSpawn{Time: pk.CurrentTime, UID: uid, Counter: f[6][0]}
		p.Spawns[uid] = append(p.Spawns[uid], spawn)
		p.count++
		return spawn, nil
	}
	if message != 0xf030 || object>>11 != 0 {
		return nil, nil
	}
	f, err := decodeOptionalIDField32(b[offset:], map[uint8]bool{1: true, 5: true})
	if err != nil || len(f[1]) != 4 || len(f[5]) != 1 {
		return nil, nil
	}
	seconds := float64(math.Float32frombits(binary.LittleEndian.Uint32(f[1])))
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > float64(math.MaxUint32)/1000 {
		return nil, nil
	}
	millis := uint32(math.Round(seconds * 1000))
	// A corrupt timestamp cannot move another sortie arbitrarily along the timeline.
	if math.Abs(float64(millis)-float64(pk.CurrentTime)) > 1000 {
		return nil, nil
	}
	spawn := aircraftSpawn{Time: millis, UID: uint64(object), Counter: f[5][0]}
	if object&0x7ff == 0x7ff {
		p.pending = append(p.pending, aircraftSpawnReference{eid: eid, t: millis, counter: spawn.Counter})
		p.count++
		return nil, nil
	}
	p.Spawns[spawn.UID] = append(p.Spawns[spawn.UID], spawn)
	p.count++
	return spawn, nil
}

func (p *aircraftSpawnParser) observeConstructions(pk *packet.Packet) {
	if p.ecs == nil || len(p.ecs.Messages) == 0 {
		return
	}
	last := p.ecs.Messages[len(p.ecs.Messages)-1]
	if last.PacketSeq != pk.Seq || last.PacketTime != pk.CurrentTime || last.Control != 0x24 {
		return
	}
	for _, msg := range last.Messages {
		if msg == nil {
			continue
		}
		for _, value := range msg.Parsed {
			entity, ok := value.(*packetecs2.Entity)
			if !ok || entity == nil {
				continue
			}
			uid, ok := packetecs2.GetObjectData[int32](&entity.Data, "uid")
			if !ok || uid < 0 || uid >= 0x7ff {
				continue
			}
			kind, known := packetecs2.GetObjectData[int32](&entity.Data, "unitType")
			if known && kind != 0 {
				continue
			}
			// Only aircraft constructions can identify an aircraft MPI handle.
			if !strings.Contains(entity.Template, "aircraft") {
				continue
			}
			old, exists := p.owners[msg.EID]
			if exists && old.uid != uint64(uid) {
				old.conflict = true
				p.owners[msg.EID] = old
				continue
			}
			if !exists && len(p.owners) < 1<<16 {
				p.owners[msg.EID] = aircraftSpawnOwner{uid: uint64(uid), entity: entity}
			}
		}
	}
}

func (p *aircraftSpawnParser) normalized() map[uint64][]aircraftSpawn {
	out := map[uint64][]aircraftSpawn{}
	if p == nil {
		return out
	}
	recordsByUID := map[uint64][]aircraftSpawn{}
	for uid, records := range p.Spawns {
		recordsByUID[uid] = append([]aircraftSpawn(nil), records...)
	}
	for _, ref := range p.pending {
		if owner, ok := p.owners[ref.eid]; ok && !owner.conflict {
			recordsByUID[owner.uid] = append(recordsByUID[owner.uid], aircraftSpawn{Time: ref.t, UID: owner.uid, Counter: ref.counter, Entity: owner.entity})
		}
	}
	for uid, records := range recordsByUID {
		s := append([]aircraftSpawn(nil), records...)
		sort.SliceStable(s, func(i, j int) bool { return s[i].Time < s[j].Time })
		for _, spawn := range s {
			a := out[uid]
			// Repeated deliveries of the same counter do not create new sorties.
			if len(a) > 0 && a[len(a)-1].Counter == spawn.Counter {
				if a[len(a)-1].Entity == nil {
					a[len(a)-1].Entity = spawn.Entity
				}
				continue
			}
			if len(a) > 0 && a[len(a)-1].Time == spawn.Time {
				continue
			}
			out[uid] = append(a, spawn)
		}
	}
	return out
}

func aircraftSpawnIndex(spawns []aircraftSpawn, t uint32) int {
	i := sort.Search(len(spawns), func(i int) bool { return spawns[i].Time > t }) - 1
	if i < 0 {
		return 0
	}
	return i
}
