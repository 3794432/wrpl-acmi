package main

import (
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectAircraftSortiesPreservesModelsAndDeaths(t *testing.T) {
	first := &packetecs2.Entity{}
	first.Data.AddComponent(&packetecs2.Component{Value: "f_16c_block_50"}, "unit__className")
	second := &packetecs2.Entity{}
	second.Data.AddComponent(&packetecs2.Component{Value: "f_15e"}, "unit__className")
	fm := &packetfm.PacketFlightModelParser{}
	for _, frame := range []struct {
		t      uint32
		entity *packetecs2.Entity
	}{{100, first}, {150, first}, {200, first}, {400, second}, {450, second}, {500, second}} {
		fm.Results = append(fm.Results, packetfm.FMUpdatePacket{CurrentTime: frame.t, Entries: []*packetfm.FMEntry{{UID: 42, ResolvedEntity: frame.entity, Data: &packetfm.FMData{PosX: 1, PosY: 100, PosZ: 2}}}})
	}
	tracks := collectTracks(fm, nil, nil, nil)
	if len(tracks) != 2 || tracks[0].key == tracks[1].key || tracks[0].key.id != 42 || tracks[1].key.id != 42 {
		t.Fatalf("sorties = %+v", tracks)
	}
	if tracks[0].entity != first || tracks[1].entity != second {
		t.Fatal("models crossed sorties")
	}
	deaths := &aircraftDestructionParser{Deaths: []aircraftDestruction{{Time: 160, VictimUID: 42}, {Time: 460, VictimUID: 42}}}
	if n := applyAircraftDestructions(tracks, deaths); n != 2 {
		t.Fatalf("applied %d", n)
	}
	if aircraftTrackAt(tracks, 42, 450) != tracks[1] || aircraftTrackAt(tracks, 42, 300) != nil {
		t.Fatal("wrong active sortie")
	}
	path := filepath.Join(t.TempDir(), "sorties.acmi")
	if err := writeACMI(path, wrpl.WRPLHeader{}, tracks, nil, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	ids := acmiObjectIDs(tracks)
	for _, tr := range tracks {
		if !strings.Contains(text, "0,Event=Destroyed|"+ids[tr.key]) || !strings.Contains(text, "-"+ids[tr.key]) {
			t.Fatal("missing destruction for sortie")
		}
	}
	if strings.Count(text, ",T=") != 4 {
		t.Fatalf("positions after deaths exported or second sortie missing: %s", text)
	}
}

func TestAircraftSortieReplacementWithoutDeathIsOnlyRemoved(t *testing.T) {
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		{CurrentTime: 100, Entries: []*packetfm.FMEntry{{UID: 0, ResolvedEntity: &packetecs2.Entity{}, Data: &packetfm.FMData{}}}},
		{CurrentTime: 200, Entries: []*packetfm.FMEntry{{UID: 0, ResolvedEntity: &packetecs2.Entity{}, Data: &packetfm.FMData{}}}},
	}}
	tracks := collectTracks(fm, nil, nil, nil)
	if len(tracks) != 2 || !tracks[0].retired || tracks[0].end != 199 {
		t.Fatal("replacement not bounded")
	}
	path := filepath.Join(t.TempDir(), "replaced.acmi")
	if err := writeACMI(path, wrpl.WRPLHeader{}, tracks, nil, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "Event=Destroyed") {
		t.Fatal("replacement fabricated destruction")
	}
}

func TestKillAttributionResolvesReusedUIDAtEventTime(t *testing.T) {
	first := killAttributionAircraft(10, 200, "First")
	first.samples = []acmiSample{{t: 100}}
	second := killAttributionAircraft(10, 500, "Second")
	second.key.sortie = 1
	second.samples = []acmiSample{{t: 400}}
	victim := killAttributionAircraft(20, 450, "Victim")
	victim.samples = []acmiSample{{t: 400}}
	kill := killAttributionEntry(450, 10, 20, "gun")
	var kills []packetkill.KillEntry
	kills = append(kills, kill)
	events := buildKillTimeline([]*track{first, second, victim}, kills, nil, nil)
	if len(events) != 1 || len(events[0].keys) != 1 || events[0].keys[0] != second.key {
		t.Fatalf("events = %+v", events)
	}
}
