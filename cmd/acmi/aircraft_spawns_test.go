package main

import (
	"encoding/binary"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
	"math"
	"testing"
)

func aircraftSpawnTestPacket(uid uint16, when uint32, counter byte) *packet.Packet {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint16(b, uid)
	b[2], b[3] = 0x30, 0xf0
	stamp := make([]byte, 4)
	binary.LittleEndian.PutUint32(stamp, math.Float32bits(float32(when)/1000))
	b = append(b, buildOptionalIDField32([]optionalTestField{{number: 1, size: 32, value: stamp}, {number: 5, size: 8, value: []byte{counter}}})...)
	return &packet.Packet{PacketType: 4, CurrentTime: when, PacketPayload: b}
}

func TestAircraftSpawnParserBoundsAndDuplicateCounters(t *testing.T) {
	p := newAircraftSpawnParser()
	for _, pk := range []*packet.Packet{aircraftSpawnTestPacket(42, 1000, 0xff), aircraftSpawnTestPacket(42, 1001, 0xff), aircraftSpawnTestPacket(42, 5000, 0), aircraftSpawnTestPacket(42|0x800, 6000, 1)} {
		p.Parse(pk)
	}
	records := p.normalized()[42]
	if len(records) != 2 || records[0].Time != 1000 || records[1].Counter != 0 {
		t.Fatalf("records=%+v", records)
	}
	for n := 0; n < len(aircraftSpawnTestPacket(42, 1000, 1).PacketPayload); n++ {
		pk := aircraftSpawnTestPacket(42, 1000, 1)
		pk.PacketPayload = pk.PacketPayload[:n]
		q := newAircraftSpawnParser()
		q.Parse(pk)
		if q.count != 0 {
			t.Fatalf("accepted truncated spawn at %d", n)
		}
	}
	for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(-1), 1e30} {
		pk := aircraftSpawnTestPacket(42, 1000, 1)
		binary.LittleEndian.PutUint32(pk.PacketPayload[7:11], math.Float32bits(value))
		q := newAircraftSpawnParser()
		q.Parse(pk)
		if q.count != 0 {
			t.Fatalf("accepted timestamp %v", value)
		}
	}
	pk := aircraftSpawnTestPacket(42, 1000, 1)
	pk.CurrentTime = 10000
	q := newAircraftSpawnParser()
	q.Parse(pk)
	if q.count != 0 {
		t.Fatal("accepted inconsistent timestamp")
	}
}

func TestRepeatedAircraftSpawnsWithSameECSEntity(t *testing.T) {
	entity := &packetecs2.Entity{Template: "aircraft"}
	fm := &packetfm.PacketFlightModelParser{}
	for _, when := range []uint32{1000, 1100, 1200, 5000, 5100, 5200, 9000, 9100} {
		fm.Results = append(fm.Results, packetfm.FMUpdatePacket{CurrentTime: when, Entries: []*packetfm.FMEntry{{UID: 42, ResolvedEntity: entity, Data: &packetfm.FMData{PosY: 100}}}})
	}
	// A short record carries no position and cannot invent a zero-position sortie.
	fm.Results = append(fm.Results, packetfm.FMUpdatePacket{CurrentTime: 9200, Entries: []*packetfm.FMEntry{{UID: 42, ResolvedEntity: entity, Data: &packetfm.FMData{Unk0: true, Unk1: true}}}})
	spawns := newAircraftSpawnParser()
	for i, when := range []uint32{1000, 5000, 9000} {
		spawns.Parse(aircraftSpawnTestPacket(42, when, byte(i)))
	}
	tracks := collectTracks(fm, nil, nil, nil, spawns)
	if len(tracks) != 3 {
		t.Fatalf("got %d sorties", len(tracks))
	}
	deaths := &aircraftDestructionParser{Deaths: []aircraftDestruction{{Time: 1150, VictimUID: 42}, {Time: 5150, VictimUID: 42}}}
	if applyAircraftDestructions(tracks, deaths) != 2 || tracks[2].hasEnd {
		t.Fatal("death leaked into later sortie")
	}
	if tracks[0].end != 1150 || tracks[1].end != 5150 || len(tracks[2].samples) != 2 {
		t.Fatal("sortie data lost")
	}
	for i, tr := range tracks {
		if tr.key.sortie != uint32(i) || tr.entity != entity {
			t.Fatal("wrong identity")
		}
	}
	if parent := aircraftParentAt(tracks, 42, 5100); parent != tracks[1] {
		t.Fatal("parent not second sortie")
	}
	if parent := aircraftParentAt(tracks, 42, 4000); parent != nil {
		t.Fatal("dead UID used as parent")
	}
}

func TestExtendedAircraftSpawnRequiresFullConstructionIdentity(t *testing.T) {
	entity := &packetecs2.Entity{Template: "aircraft+player_unit"}
	entity.Data.AddComponent(&packetecs2.Component{Value: int32(438)}, "uid")
	const eid = uint64(1<<22 | 3068)
	ecs := &packetecs2.PacketECSParser{Messages: []packetecs2.ParsedPacketECS{{PacketSeq: 1, PacketTime: 1000, Control: 0x24, Messages: []*packetecs2.Message{{EID: eid, Parsed: []any{entity}}}}}}
	p := newAircraftSpawnParser(ecs)
	p.Parse(&packet.Packet{PacketType: 6, Seq: 1, CurrentTime: 1000})
	makePacket := func(wire uint64, when uint32, counter byte) *packet.Packet {
		b := []byte{0xff, 0x07}
		w := optionalTestBitWriter{}
		w.variable(wire)
		b = append(b, w.raw()...)
		b = append(b, 0x61, 0xf0)
		b = append(b, buildOptionalIDField32([]optionalTestField{{number: 1, size: 8, value: []byte{counter}}})...)
		return &packet.Packet{PacketType: 4, CurrentTime: when, PacketPayload: b}
	}
	p.Parse(makePacket(3068<<10|1, 1000, 17))
	p.Parse(makePacket(3068<<10|1, 5000, 18))
	// Same array index, different generation: no association to the old aircraft.
	p.Parse(makePacket(3068<<10|4, 7000, 19))
	records := p.normalized()[438]
	if len(records) != 2 || records[0].Time != 1000 || records[1].Time != 5000 {
		t.Fatalf("records=%+v", records)
	}
	other := &packetecs2.Entity{Template: "aircraft"}
	other.Data.AddComponent(&packetecs2.Component{Value: int32(999)}, "uid")
	ecs.Messages = append(ecs.Messages, packetecs2.ParsedPacketECS{PacketSeq: 2, PacketTime: 8000, Control: 0x24, Messages: []*packetecs2.Message{{EID: eid, Parsed: []any{other}}}})
	p.Parse(&packet.Packet{PacketType: 6, Seq: 2, CurrentTime: 8000})
	if len(p.normalized()[438]) != 0 {
		t.Fatal("conflicting exact identity was guessed")
	}
}

func TestAircraftSpawnStateDeduplicatesF093AfterF061(t *testing.T) {
	p := newAircraftSpawnParser()
	p.Parse(&packet.Packet{PacketType: 4, CurrentTime: 1000, PacketPayload: append([]byte{42, 0, 0x61, 0xf0}, buildOptionalIDField32([]optionalTestField{{number: 1, size: 8, value: []byte{17}}})...)})
	b := append([]byte{2, 0x58, 0x93, 0xf0}, buildOptionalIDField32([]optionalTestField{{number: 1, size: 16, value: []byte{42, 0}}, {number: 2, size: 32, value: []byte{144, 0, 0, 0}}, {number: 6, size: 8, value: []byte{17}}})...)
	p.Parse(&packet.Packet{PacketType: 4, CurrentTime: 3000, PacketPayload: b})
	if r := p.normalized()[42]; len(r) != 1 || r[0].Time != 1000 {
		t.Fatalf("duplicate state created sortie: %+v", r)
	}
}
