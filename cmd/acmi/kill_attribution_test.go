package main

import (
	"reflect"
	"testing"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
)

func TestBuildKillTimelineRequiresExactAircraftTimeAndUID(t *testing.T) {
	killer := killAttributionAircraft(10, 3000, "Killer")
	victim := killAttributionAircraft(20, 2000, "Victim")
	valid := killAttributionEntry(2000, 10, 20, "cannon")
	valid.KillerPid = 99
	valid.VictimPid = 99
	tooEarly := killAttributionEntry(1999, 10, 20, "cannon")
	wrongUID := killAttributionEntry(2000, 11, 20, "cannon")
	destroyedOnly := killAttributionEntry(2000, 10, 20, "")
	destroyedOnly.DestroyedWeapon = "cannon"
	missingFlag := killAttributionEntry(2000, 10, 20, "cannon")
	missingFlag.HasVictimUID = false

	events := buildKillTimeline([]*track{killer, victim},
		[]packetkill.KillEntry{valid, tooEarly, wrongUID, destroyedOnly, missingFlag}, nil,
		map[string]string{"air:20": "Shown Victim"})
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one exact-time kill", events)
	}
	event := events[0]
	if event.t != 2000 || event.text != "Killed Shown Victim with cannon" {
		t.Fatalf("event = %+v", event)
	}
	if want := []trackKey{killer.key}; !reflect.DeepEqual(event.keys, want) {
		t.Fatalf("keys = %+v, want killer only %+v", event.keys, want)
	}
}

func TestBuildKillTimelineUIDZeroAndAmbiguity(t *testing.T) {
	killer := killAttributionAircraft(0, 3000, "Killer")
	firstVictim := killAttributionAircraft(20, 2000, "First")
	secondVictim := killAttributionAircraft(20, 2000, "Second")
	firstVictim.entity = &packetecs2.Entity{Template: "aircraft"}
	secondVictim.entity = &packetecs2.Entity{Template: "aircraft"}
	zeroUID := killAttributionEntry(2000, 0, 20, "gun")
	missingFlag := zeroUID
	missingFlag.HasKillerUID = false

	events := buildKillTimeline([]*track{killer, firstVictim, secondVictim}, []packetkill.KillEntry{zeroUID, missingFlag}, nil, nil)
	if len(events) != 0 {
		t.Fatalf("ambiguous victim produced events: %+v", events)
	}

	resolved := zeroUID
	resolved.ResolvedVictim = firstVictim.entity
	events = buildKillTimeline([]*track{killer, firstVictim, secondVictim}, []packetkill.KillEntry{resolved}, nil,
		map[string]string{"air:20": "First"})
	if len(events) != 1 || events[0].text != "Killed First with gun" {
		t.Fatalf("exact resolved victim = %+v", events)
	}
}

func TestBuildKillTimelineGroundKillerAndVictimIdentity(t *testing.T) {
	killer := killAttributionAircraft(10, 3000, "Killer")
	groundKiller := killAttributionGround(groundUnitUIDNamespace|42, 2000, "SPAA")
	groundVictim := killAttributionGround(groundUnitUIDNamespace|7, 2000, "Target Tank")
	entry := killAttributionEntry(2000, 42, 7, "autocannon")

	events := buildKillTimeline([]*track{killer, groundKiller, groundVictim}, []packetkill.KillEntry{entry}, nil, nil)
	if len(events) != 1 || events[0].text != "Killed Target Tank with autocannon" {
		t.Fatalf("explicit ground identities = %+v", events)
	}
	if !reflect.DeepEqual(events[0].keys, []trackKey{groundKiller.key}) {
		t.Fatalf("ground killer keys = %+v", events[0].keys)
	}

	resolvedGroundKiller := &track{key: trackKey{id: 55}, entity: &packetecs2.Entity{Template: "ground"},
		samples: []acmiSample{{t: 1000}}, name: "Resolved SPAA"}
	resolvedEntry := killAttributionEntry(2000, 55, 7, "autocannon")
	resolvedEntry.ResolvedKiller = resolvedGroundKiller.entity
	events = buildKillTimeline([]*track{killer, resolvedGroundKiller, groundVictim}, []packetkill.KillEntry{resolvedEntry}, nil, nil)
	if len(events) != 1 || !reflect.DeepEqual(events[0].keys, []trackKey{resolvedGroundKiller.key}) {
		t.Fatalf("resolved ECS ground killer = %+v", events)
	}

	legacyKiller := &track{key: trackKey{id: 42}, samples: []acmiSample{{t: 1000}}}
	legacyVictim := &track{key: trackKey{id: 7}, samples: []acmiSample{{t: 1000}}, hasEnd: true, end: 2000}
	if got := buildKillTimeline([]*track{killer, legacyKiller, legacyVictim}, []packetkill.KillEntry{entry}, nil, nil); len(got) != 0 {
		t.Fatalf("legacy numeric ids were treated as ECS identities: %+v", got)
	}

	explicitVictim := &track{key: trackKey{id: 9}, entity: &packetecs2.Entity{Template: "ground"}, samples: []acmiSample{{t: 1000}}, hasEnd: true, end: 2000, name: "Explicit Ground"}
	explicit := killAttributionEntry(2000, 10, 9, "bomb")
	explicit.ResolvedVictim = explicitVictim.entity
	events = buildKillTimeline([]*track{killer, explicitVictim}, []packetkill.KillEntry{explicit}, nil, nil)
	if len(events) != 1 || events[0].text != "Killed Explicit Ground with bomb" {
		t.Fatalf("explicit ECS ground victim = %+v", events)
	}
}

func TestBuildKillTimelineGroundEndMustMatchKillTime(t *testing.T) {
	killer := killAttributionAircraft(10, 3000, "Killer")
	ground := killAttributionGround(groundUnitUIDNamespace|7, 2500, "Target Tank")
	entry := killAttributionEntry(2000, 10, 7, "gun")
	if got := buildKillTimeline([]*track{killer, ground}, []packetkill.KillEntry{entry}, nil, nil); len(got) != 0 {
		t.Fatalf("conflicting ground end produced events: %+v", got)
	}

	ground.end = 2000
	if got := buildKillTimeline([]*track{killer, ground}, []packetkill.KillEntry{entry}, nil, nil); len(got) != 1 {
		t.Fatalf("matching ground end events = %+v", got)
	}
	ground.hasEnd = false
	if got := buildKillTimeline([]*track{killer, ground}, []packetkill.KillEntry{entry}, nil, nil); len(got) != 1 {
		t.Fatalf("ground without an observed end events = %+v", got)
	}
}

func TestBuildKillTimelineSkipsPosthumousKiller(t *testing.T) {
	killer := killAttributionAircraft(10, 1500, "Killer")
	victim := killAttributionAircraft(20, 2000, "Victim")
	entry := killAttributionEntry(2000, 10, 20, "missile")
	if got := buildKillTimeline([]*track{killer, victim}, []packetkill.KillEntry{entry}, nil, nil); len(got) != 0 {
		t.Fatalf("posthumous killer produced an event: %+v", got)
	}

	killer.end = 2000
	if got := buildKillTimeline([]*track{killer, victim}, []packetkill.KillEntry{entry}, nil, nil); len(got) != 1 {
		t.Fatalf("killer at its end should remain attributable: %+v", got)
	}
}

func killAttributionAircraft(uid uint64, end uint32, name string) *track {
	samples := []acmiSample{{t: 1000}}
	if end >= 1000 {
		samples = append(samples, acmiSample{t: end})
	}
	return &track{key: trackKey{air: true, id: uid}, samples: samples, name: name, hasEnd: end != 0, end: end}
}

func killAttributionGround(id uint64, end uint32, name string) *track {
	tr := &track{key: trackKey{groundEntity: true, id: id}, samples: []acmiSample{{t: 1000}}, name: name}
	if end != 0 {
		tr.hasEnd, tr.end = true, end
	}
	return tr
}

func killAttributionEntry(time uint32, killer, victim uint16, weapon string) packetkill.KillEntry {
	return packetkill.KillEntry{
		CurrentTime:  time,
		KillerUid:    killer,
		HasKillerUID: true,
		VictimUid:    victim,
		HasVictimUID: true,
		PlayerWeapon: weapon,
	}
}
