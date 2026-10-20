package main

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
)

func TestAircraftDestructionParserRequiresBoundedVictimField(t *testing.T) {
	parser := aircraftDestructionTestParser(t)
	valid := aircraftDestructionTestPacket(1200, 0x345)
	if _, err := parser.Parse(valid); err != nil {
		t.Fatalf("valid destruction parse error: %v", err)
	}
	if len(parser.Deaths) != 1 || parser.Deaths[0] != (aircraftDestruction{Time: 1200, VictimUID: 0x345}) {
		t.Fatalf("valid deaths = %+v", parser.Deaths)
	}

	tests := map[string]*packet.Packet{
		"missing field 3":     aircraftDestructionTestPacketWithoutVictim(1300),
		"sentinel victim":     aircraftDestructionTestPacket(1400, 0x7ff),
		"truncated":           aircraftDestructionTestTruncated(valid),
		"oversized size":      aircraftDestructionTestOversizedSize(1500, 0x456),
		"oversized offset":    aircraftDestructionTestOversizedOffset(1600, 0x567),
		"field overruns body": aircraftDestructionTestFieldOverrun(1700, 0x678),
	}
	for name, pk := range tests {
		before := len(parser.Deaths)
		if _, err := parser.Parse(pk); err != nil {
			t.Fatalf("%s returned an export-blocking error: %v", name, err)
		}
		if len(parser.Deaths) != before {
			t.Fatalf("%s produced a destruction record", name)
		}
	}
	if parser.Skipped < uint64(len(tests)) {
		t.Fatalf("skipped = %d, want at least %d", parser.Skipped, len(tests))
	}
}

func TestAircraftDestructionParserDoesNotRequireKillerOrWeapon(t *testing.T) {
	parser := aircraftDestructionTestParser(t)
	if _, err := parser.Parse(aircraftDestructionTestPacket(2200, 321)); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parser.Deaths) != 1 || parser.Deaths[0].VictimUID != 321 {
		t.Fatalf("deaths without killer/weapon = %+v", parser.Deaths)
	}
}

func TestApplyAircraftDestructionsUsesEarliestValidKill(t *testing.T) {
	samples := []acmiSample{{t: 1000}, {t: 2000}}
	tr := aircraftDestructionTestTrack(true, 10, samples)
	parser := &aircraftDestructionParser{Deaths: []aircraftDestruction{
		{Time: 1800, VictimUID: 10},
		{Time: 1400, VictimUID: 10},
		{Time: 1600, VictimUID: 10},
		{Time: 1400, VictimUID: 10},
	}}
	beforeSamples := append([]acmiSample(nil), tr.samples...)
	originalSlice := tr.samples

	if got := applyAircraftDestructions([]*track{tr}, parser); got != 1 {
		t.Fatalf("applied = %d, want 1", got)
	}
	if !tr.hasEnd || tr.end != 1400 {
		t.Fatalf("end = %d/%v, want earliest valid kill 1400", tr.end, tr.hasEnd)
	}
	if !reflect.DeepEqual(tr.samples, beforeSamples) || &tr.samples[0] != &originalSlice[0] {
		t.Fatal("apply modified or replaced track samples")
	}
	if got := applyAircraftDestructions([]*track{tr}, parser); got != 0 || tr.end != 1400 {
		t.Fatalf("duplicate apply changed lifetime: applied=%d end=%d", got, tr.end)
	}
}

func TestApplyAircraftDestructionsHonorsSampleWindow(t *testing.T) {
	before := aircraftDestructionTestTrack(true, 20, []acmiSample{{t: 1000}, {t: 1500}})
	after := aircraftDestructionTestTrack(true, 21, []acmiSample{{t: 1000}, {t: 1500}})
	later := aircraftDestructionTestTrack(true, 22, []acmiSample{{t: 1000}, {t: 1500}})
	parser := &aircraftDestructionParser{Deaths: []aircraftDestruction{
		{Time: 900, VictimUID: 20},
		{Time: 2000, VictimUID: 21},
		{Time: 900, VictimUID: 22},
		{Time: 1200, VictimUID: 22},
	}}

	applied := applyAircraftDestructions([]*track{before, after, later}, parser)
	if applied != 2 {
		t.Fatalf("applied = %d, want 2", applied)
	}
	if before.hasEnd {
		t.Fatalf("kill before first sample set end=%d", before.end)
	}
	if !after.hasEnd || after.end != 2000 {
		t.Fatalf("kill after last sample end=%d/%v, want 2000/true", after.end, after.hasEnd)
	}
	if !later.hasEnd || later.end != 1200 {
		t.Fatalf("later valid kill after pre-sample death end=%d/%v, want 1200/true", later.end, later.hasEnd)
	}
}

func TestApplyAircraftDestructionsOnlyTouchesMatchingAircraft(t *testing.T) {
	air := aircraftDestructionTestTrack(true, 30, []acmiSample{{t: 100}, {t: 200}})
	ground := aircraftDestructionTestTrack(false, 30, []acmiSample{{t: 100}, {t: 200}})
	missile := aircraftDestructionTestTrack(true, 30, []acmiSample{{t: 100}, {t: 200}})
	missile.key.missile = true
	decoy := aircraftDestructionTestTrack(true, 30, []acmiSample{{t: 100}, {t: 200}})
	decoy.key.decoy = true
	unmatched := aircraftDestructionTestTrack(true, 31, []acmiSample{{t: 100}, {t: 200}})
	earlier := aircraftDestructionTestTrack(true, 32, []acmiSample{{t: 100}})
	earlier.end, earlier.hasEnd = 150, true

	samplesBefore := make([][]acmiSample, 0, 6)
	for _, tr := range []*track{air, ground, missile, decoy, unmatched, earlier} {
		samplesBefore = append(samplesBefore, append([]acmiSample(nil), tr.samples...))
	}
	parser := &aircraftDestructionParser{Deaths: []aircraftDestruction{
		{Time: 180, VictimUID: 30},
		{Time: 180, VictimUID: 32},
	}}

	if got := applyAircraftDestructions([]*track{air, ground, missile, decoy, unmatched, earlier}, parser); got != 1 {
		t.Fatalf("applied = %d, want only the aircraft lifetime", got)
	}
	if !air.hasEnd || air.end != 180 {
		t.Fatalf("aircraft end = %d/%v, want 180/true", air.end, air.hasEnd)
	}
	if ground.hasEnd || missile.hasEnd || decoy.hasEnd || unmatched.hasEnd {
		t.Fatal("non-aircraft, missile, decoy, or unmatched track was ended")
	}
	if !earlier.hasEnd || earlier.end != 150 {
		t.Fatalf("existing earlier end was extended to %d", earlier.end)
	}
	for i, tr := range []*track{air, ground, missile, decoy, unmatched, earlier} {
		if !reflect.DeepEqual(tr.samples, samplesBefore[i]) {
			t.Fatalf("track %d samples changed", i)
		}
	}
}

func aircraftDestructionTestParser(t *testing.T) *aircraftDestructionParser {
	t.Helper()
	return newAircraftDestructionParser()
}

func aircraftDestructionTestTrack(air bool, id uint64, samples []acmiSample) *track {
	return &track{key: trackKey{air: air, id: id}, samples: samples}
}

func aircraftDestructionTestPacket(time uint32, victim uint16) *packet.Packet {
	body := make([]byte, 2)
	binary.LittleEndian.PutUint16(body, victim)
	return aircraftDestructionTestPacketFromParts(time, 1<<3, body, []byte{0x60})
}

func aircraftDestructionTestPacketWithoutVictim(time uint32) *packet.Packet {
	return aircraftDestructionTestPacketFromParts(time, 1<<4, []byte{1, 0}, []byte{0x60})
}

func aircraftDestructionTestPacketFromParts(time uint32, fields uint32, body, sizes []byte) *packet.Packet {
	header := make([]byte, 2)
	header = append(header, aircraftDestructionTestULEB(fields)...)
	offset := len(header) + len(body)
	header[0], header[1] = byte(offset), byte(offset>>8)
	payload := []byte{0x02, 0x58, 0x58, 0xf0}
	payload = append(payload, header...)
	payload = append(payload, body...)
	payload = append(payload, sizes...)
	return &packet.Packet{PacketType: 4, CurrentTime: time, PacketPayload: payload}
}

func aircraftDestructionTestTruncated(pk *packet.Packet) *packet.Packet {
	payload := append([]byte(nil), pk.PacketPayload[:len(pk.PacketPayload)-2]...)
	return &packet.Packet{PacketType: 4, CurrentTime: pk.CurrentTime, PacketPayload: payload}
}

func aircraftDestructionTestOversizedSize(time uint32, victim uint16) *packet.Packet {
	body := make([]byte, 2)
	binary.LittleEndian.PutUint16(body, victim)
	sizes := append([]byte{0}, aircraftDestructionTestULEB(aircraftDestructionMaxFieldBits+1)...)
	return aircraftDestructionTestPacketFromParts(time, 1<<3, body, sizes)
}

func aircraftDestructionTestOversizedOffset(time uint32, victim uint16) *packet.Packet {
	pk := aircraftDestructionTestPacket(time, victim)
	binary.LittleEndian.PutUint16(pk.PacketPayload[4:6], 0xffff)
	return pk
}

func aircraftDestructionTestFieldOverrun(time uint32, victim uint16) *packet.Packet {
	body := make([]byte, 2)
	binary.LittleEndian.PutUint16(body, victim)
	sizes := append([]byte{0}, aircraftDestructionTestULEB(64)...)
	return aircraftDestructionTestPacketFromParts(time, 1<<3, body, sizes)
}

func aircraftDestructionTestULEB(value uint32) []byte {
	var out []byte
	for {
		part := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			part |= 0x80
		}
		out = append(out, part)
		if value == 0 {
			return out
		}
	}
}
