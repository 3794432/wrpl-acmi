package main

import (
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

func TestOptionalDamageCriticalAndSevereFormat(t *testing.T) {
	p := newOptionalEventParser(map[string]bool{"damage": true}, nil)
	damageTestRosterAt(p, 1, 0, 11, "Alice")
	damageTestRosterAt(p, 2, 0, 22, "Bob")
	victim := damageTestAircraft(426, "j_11b", 1, 100)
	attacker := damageTestAircraft(77, "su_30mkk", 2, 100)
	tracks := []*track{victim, attacker}

	critical := damageTestFields(426, 2, "su_30mkk", 77)
	critical = append(critical, optionalTestField{number: 5, value: []byte{1}, size: 1})
	if _, err := p.Parse(damageTestPacket(100, false, critical)); err != nil {
		t.Fatalf("critical damage: %v", err)
	}
	if _, err := p.Parse(damageTestPacket(100, true, damageTestFields(426, 2, "su_30mkk", 77))); err != nil {
		t.Fatalf("severe damage: %v", err)
	}

	events := p.timeline(tracks)
	if len(events) != 2 {
		t.Fatalf("damage events = %+v", events)
	}
	want := []string{
		"J-11B (Alice) critical damaged by ␗Su-30MKK (Bob)",
		"J-11B (Alice) severe damaged by ␗Su-30MKK (Bob)",
	}
	for _, expected := range want {
		found := false
		for _, event := range events {
			if event.text != expected {
				continue
			}
			if len(event.keys) != 2 {
				t.Fatalf("damage keys = %+v, want victim and attacker", event.keys)
			}
			found = true
		}
		if !found {
			t.Fatalf("missing damage text %q in %+v", expected, events)
		}
	}
	for _, event := range events {
		for _, diagnostic := range []string{"slot", "uid", "raw", "fire"} {
			if strings.Contains(strings.ToLower(event.text), diagnostic) {
				t.Fatalf("damage text retained %q diagnostic: %q", diagnostic, event.text)
			}
		}
	}
}

func TestOptionalDamagePackedUIDsUseEntityBits(t *testing.T) {
	p := newOptionalEventParser(map[string]bool{"damage": true}, nil)
	damageTestRosterAt(p, 1, 0, 11, "Alice")
	damageTestRosterAt(p, 2, 0, 22, "Bob")
	victim := damageTestAircraft(426, "j_11b", 1, 100)
	attacker := damageTestAircraft(77, "su_30mkk", 2, 100)
	packedDecoy := damageTestAircraft(uint64(0x8000|426), "packed_not_an_entity", 1, 100)
	tracks := []*track{victim, attacker, packedDecoy}

	fields := damageTestFields(uint16(0x8000|426), 2, "su_30mkk", uint16(0x8000|77))
	if _, err := p.Parse(damageTestPacket(100, false, fields)); err != nil {
		t.Fatalf("packed damage: %v", err)
	}
	events := p.timeline(tracks, map[string]string{"air:426": "J-11B custom"})
	if len(events) != 1 || events[0].text != "J-11B custom (Alice) critical damaged by ␗Su-30MKK (Bob)" {
		t.Fatalf("packed UID damage = %+v", events)
	}
}

func TestOptionalDamageUnknownAndAmbiguousIdentitiesStayUnknown(t *testing.T) {
	p := newOptionalEventParser(map[string]bool{"damage": true}, nil)
	damageTestRosterAt(p, 0, 0, 10, "Alice")

	fields := damageTestFields(optionalDamageUnknownUID, 0, "j_11b", optionalDamageUnknownUID)
	if _, err := p.Parse(damageTestPacket(100, false, fields)); err != nil {
		t.Fatalf("unknown damage: %v", err)
	}
	events := p.timeline(nil)
	if len(events) != 1 || events[0].text != "Unknown vehicle (Unknown player) critical damaged by J-11B (Alice)" || len(events[0].keys) != 0 {
		t.Fatalf("unknown identity = %+v", events)
	}

	// Two simultaneous aircraft with the same victim UID are ambiguous. Neither
	// victim candidate may be attributed, and an unknown attacker stays objectless.
	first := damageTestAircraft(426, "j_11b", 0, 100)
	second := damageTestAircraft(426, "su_30mkk", 0, 100)
	p2 := newOptionalEventParser(map[string]bool{"damage": true}, nil)
	damageTestRosterAt(p2, 0, 0, 10, "Alice")
	if _, err := p2.Parse(damageTestPacket(100, false, damageTestFields(426, 0, "", optionalDamageUnknownUID))); err != nil {
		t.Fatalf("ambiguous damage: %v", err)
	}
	ambiguous := p2.timeline([]*track{first, second})
	if len(ambiguous) != 1 || ambiguous[0].text != "Unknown vehicle (Unknown player) critical damaged by Unknown vehicle (Alice)" || len(ambiguous[0].keys) != 0 {
		t.Fatalf("ambiguous identity = %+v", ambiguous)
	}

	// An unknown roster slot must not be rendered as a slot number or UID.
	if _, err := p2.Parse(damageTestPacket(101, false, damageTestFields(426, 9, "", optionalDamageUnknownUID))); err != nil {
		t.Fatalf("unknown slot damage: %v", err)
	}
	events = p2.timeline([]*track{first, second})
	if len(events) != 2 || !strings.Contains(events[1].text, "by Unknown vehicle (Unknown player)") {
		t.Fatalf("unknown roster slot = %+v", events)
	}
}

func TestOptionalDamageLifecycleAndRosterTime(t *testing.T) {
	p := newOptionalEventParser(map[string]bool{"damage": true}, nil)
	damageTestRosterAt(p, 1, 0, 11, "Alice")
	damageTestRosterAt(p, 1, 50, 12, "Mallory")
	damageTestRosterAt(p, 2, 0, 21, "Bob")
	damageTestRosterAt(p, 2, 60, 22, "Carol")

	victim := damageTestAircraft(426, "j_11b", 1, 20)
	victim.end, victim.hasEnd = 140, true
	attacker := damageTestAircraft(77, "su_30mkk", 2, 20)
	attacker.end, attacker.hasEnd = 140, true
	tracks := []*track{victim, attacker}

	for _, tc := range []struct {
		name string
		t    uint32
		want string
		keys int
	}{
		{"before birth", 10, "Unknown vehicle (Unknown player) critical damaged by ␗Su-30MKK (Bob)", 0},
		{"at destruction", 140, "J-11B (Unknown player) critical damaged by ␗Su-30MKK (Carol)", 1},
		{"after destruction", 141, "Unknown vehicle (Unknown player) critical damaged by ␗Su-30MKK (Carol)", 0},
		{"first roster snapshot", 40, "J-11B (Alice) critical damaged by ␗Su-30MKK (Bob)", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.events = nil
			if _, err := p.Parse(damageTestPacket(tc.t, false, damageTestFields(426, 2, "su_30mkk", 77))); err != nil {
				t.Fatal(err)
			}
			events := p.timeline(tracks)
			if len(events) != 1 || events[0].text != tc.want || len(events[0].keys) != tc.keys {
				t.Fatalf("lifecycle damage = %+v", events)
			}
		})
	}
}

func TestOptionalDamageAttackerModelUsesExactUIDTrack(t *testing.T) {
	p := newOptionalEventParser(map[string]bool{"damage": true}, nil)
	damageTestRosterAt(p, 0, 0, 10, "Alice")
	damageTestRosterAt(p, 2, 0, 22, "Bob")
	victim := damageTestAircraft(426, "j_11b", 0, 100)
	attacker := damageTestAircraft(77, "su_30mkk", 2, 100)
	if _, err := p.Parse(damageTestPacket(100, false, damageTestFields(426, 2, "", 77))); err != nil {
		t.Fatalf("damage without vehicle field: %v", err)
	}
	events := p.timeline([]*track{victim, attacker})
	if len(events) != 1 || events[0].text != "J-11B (Alice) critical damaged by ␗Su-30MKK (Bob)" {
		t.Fatalf("attacker track model = %+v", events)
	}

	// A duplicate attacker UID is ambiguous and must not supply a model.
	p.events = nil
	if _, err := p.Parse(damageTestPacket(101, false, damageTestFields(426, 2, "", 77))); err != nil {
		t.Fatalf("ambiguous attacker damage: %v", err)
	}
	duplicate := damageTestAircraft(77, "j_11b", 1, 100)
	events = p.timeline([]*track{victim, attacker, duplicate})
	if len(events) != 1 || events[0].text != "J-11B (Alice) critical damaged by Unknown vehicle (Bob)" || len(events[0].keys) != 1 {
		t.Fatalf("ambiguous attacker model = %+v", events)
	}

	// The UID track and event PID identify different players. Do not attach the
	// track or use it as a model fallback; the event PID still names the attacker.
	p.events = nil
	conflicting := damageTestAircraft(77, "su_30mkk", 0, 100)
	if _, err := p.Parse(damageTestPacket(102, false, damageTestFields(426, 2, "", 77))); err != nil {
		t.Fatalf("conflicting attacker damage: %v", err)
	}
	events = p.timeline([]*track{victim, conflicting})
	if len(events) != 1 || events[0].text != "J-11B (Alice) critical damaged by Unknown vehicle (Bob)" || len(events[0].keys) != 1 {
		t.Fatalf("conflicting attacker identity = %+v", events)
	}
}

func TestOptionalDamageObservesSlotsWithoutJoinMessages(t *testing.T) {
	p := newOptionalEventParser(map[string]bool{"damage": true}, nil)
	matcher := packet.NewParserMatcher([]packet.PacketParser{p})
	for _, pk := range []*packet.Packet{
		optionalSlotPacket(t, 10, 1, 11, "Alice", 0),
		optionalSlotPacket(t, 20, 2, 22, "Bob", 0),
	} {
		if results := matcher.Match(pk); len(results) != 1 || results[0].Err != nil {
			t.Fatalf("damage-only slot dispatch = %+v", results)
		}
	}
	if len(p.events) != 0 || len(p.timeline(nil)) != 0 {
		t.Fatalf("damage-only slot parsing emitted join events: %+v", p.events)
	}
	if entry, ok := p.optionalRosterAt(1, 15); !ok || entry.name != "Alice" {
		t.Fatalf("damage-only roster = %+v, %t", entry, ok)
	}

	victim := damageTestAircraft(426, "j_11b", 1, 0)
	if _, err := p.Parse(damageTestPacket(15, false, damageTestFields(426, 9, "", optionalDamageUnknownUID))); err != nil {
		t.Fatalf("damage-only event: %v", err)
	}
	events := p.timeline([]*track{victim})
	if len(events) != 1 || !strings.HasPrefix(events[0].text, "J-11B (Alice) critical damaged by") || len(events[0].keys) != 1 {
		t.Fatalf("damage-only identity = %+v", events)
	}
}

func TestOptionalDamageDeduplicatesDisplayedReports(t *testing.T) {
	p := newOptionalEventParser(map[string]bool{"damage": true}, nil)
	damageTestRosterAt(p, 1, 0, 11, "Alice")
	damageTestRosterAt(p, 2, 0, 22, "Bob")
	victim := damageTestAircraft(426, "j_11b", 1, 100)
	attacker := damageTestAircraft(77, "su_30mkk", 2, 100)
	// An otherwise identical report can omit the attacker entity reference.
	// Both arrival orders retain one message with the complete exact references.
	for _, order := range [][]uint16{{77, optionalDamageUnknownUID}, {optionalDamageUnknownUID, 77}} {
		p.events = nil
		for _, uid := range order {
			if _, err := p.Parse(damageTestPacket(100, false, damageTestFields(426, 2, "su_30mkk", uid))); err != nil {
				t.Fatal(err)
			}
		}
		events := p.timeline([]*track{victim, attacker})
		if len(events) != 1 || len(events[0].keys) != 2 {
			t.Fatalf("duplicate damage reports: %+v", events)
		}
	}
}

func damageTestPacket(when uint32, severe bool, fields []optionalTestField) *packet.Packet {
	prefix := []byte{0x02, 0x58, 0x56, 0xf0}
	if severe {
		prefix[2], prefix[3] = 0x57, 0xf1
	}
	payload := append(prefix, buildOptionalIDField32(fields)...)
	return &packet.Packet{CurrentTime: when, PacketType: 4, PacketPayload: payload}
}

func damageTestFields(victimUID uint16, attackerPID uint32, attackerVehicle string, attackerUID uint16) []optionalTestField {
	pid := make([]byte, 4)
	binaryLittleEndianUint32(pid, attackerPID)
	fields := []optionalTestField{
		{number: 1, value: uint16Bytes(victimUID), size: 16},
		{number: 2, value: pid, size: 32},
	}
	if attackerVehicle != "" {
		value := append([]byte{byte(len(attackerVehicle))}, []byte(attackerVehicle)...)
		fields = append(fields, optionalTestField{number: 3, value: value, size: len(value) * 8})
	}
	fields = append(fields, optionalTestField{number: 4, value: uint16Bytes(attackerUID), size: 16})
	return fields
}

func uint16Bytes(value uint16) []byte {
	return []byte{byte(value), byte(value >> 8)}
}

func binaryLittleEndianUint32(out []byte, value uint32) {
	out[0], out[1], out[2], out[3] = byte(value), byte(value>>8), byte(value>>16), byte(value>>24)
}

func damageTestRosterAt(p *optionalEventParser, slot int, t uint32, playerID uint64, name string) {
	entry := optionalRosterEntry{known: true, playerID: playerID, name: name}
	p.roster[slot] = entry
	p.rosterLog[slot] = append(p.rosterLog[slot], optionalRosterChange{t: t, entry: entry})
}

func damageTestAircraft(id uint64, className string, playerSlot int, start uint32) *track {
	tr := &track{key: trackKey{air: true, id: id}, samples: []acmiSample{{t: start}}}
	if className == "" && playerSlot < 0 {
		return tr
	}
	tr.entity = &packetecs2.Entity{}
	if className != "" {
		tr.entity.Data.AddComponent(&packetecs2.Component{Value: className}, "unit__className")
	}
	if playerSlot >= 0 {
		tr.entity.Data.AddComponent(&packetecs2.Component{Value: int32(playerSlot)}, "unit__playerId")
	}
	return tr
}
