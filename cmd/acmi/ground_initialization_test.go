package main

import (
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
)

func groundInitializationTestTrack() *track {
	tr := groundIdentityTestTrack(42, []uint32{100, 150, 200, 300})
	tr.samples[0].x, tr.samples[1].x = 50, 50
	return tr
}

func TestGroundInitializationDropsOnlyStationaryPreConstructionObject(t *testing.T) {
	info := groundIdentityTestInfo(42, 1000, 200, "ussr_t_80bvm", 1)
	metadata := &groundmeta.Parser{All: []groundmeta.Info{info}, Ends: map[uint64]uint32{1000: 350}}
	for _, identify := range []bool{true, false} {
		original := groundInitializationTestTrack()
		out := applyGroundIdentityHistory([]*track{original}, metadata, identify)
		if len(out) != 1 || !equalTimes(groundIdentityTestTimes(out[0]), []uint32{200, 300}) || !out[0].hasEnd || out[0].end != 350 || out[0].retired {
			t.Fatalf("initialization object leaked or real lifetime lost: %+v", out)
		}
		if len(original.samples) != 4 {
			t.Fatal("original source samples mutated")
		}
		air, slot := newTestTrack(true, 77, "F-16C", "Pilot", 1)
		air.samples = []acmiSample{{t: 100}}
		data := renderTestACMI(t, append([]*track{air}, out...), slot, nil)
		if strings.Count(data, "0,Event=Destroyed|") != 1 || !strings.Contains(data, "#0.250\n") {
			t.Fatal("real destruction event lost")
		}
	}
}

func TestGroundInitializationPreservesMovingAndUncoveredUnknownTracks(t *testing.T) {
	info := groundIdentityTestInfo(42, 1000, 200, "ussr_t_80bvm", 1)
	metadata := &groundmeta.Parser{All: []groundmeta.Info{info}}
	moving := groundInitializationTestTrack()
	moving.samples[1].x++
	out := applyGroundIdentityHistory([]*track{moving}, metadata, true)
	if len(out) != 2 || !equalTimes(groundIdentityTestTimes(out[0]), []uint32{100, 150}) {
		t.Fatal("real pre-construction motion discarded")
	}
	stationary := groundInitializationTestTrack()
	out = applyGroundIdentityHistory([]*track{stationary}, &groundmeta.Parser{}, true)
	if len(out) != 1 || out[0] != stationary {
		t.Fatal("unknown unit without later identity discarded")
	}
	info.HasConstructionTime = false
	out = applyGroundIdentityHistory([]*track{stationary}, &groundmeta.Parser{All: []groundmeta.Info{info}}, true)
	if len(out) != 1 || out[0] != stationary {
		t.Fatal("missing construction time guessed")
	}
	info.HasConstructionTime = true
	info.ClassModel, info.StorageModel = "ussr_t_80bvm", "us_m1a2_abrams"
	out = applyGroundIdentityHistory([]*track{stationary}, &groundmeta.Parser{All: []groundmeta.Info{info}}, true)
	if len(out) != 2 {
		t.Fatal("conflicting metadata suppressed unknown prefix")
	}
}

func TestGroundInitializationPreservesVictimReportsAndKnownStationaryVehicles(t *testing.T) {
	info := groundIdentityTestInfo(42, 1000, 200, "ussr_t_80bvm", 1)
	metadata := &groundmeta.Parser{All: []groundmeta.Info{info}}
	kills := []packetkill.KillEntry{{HasVictimUID: true, VictimUid: 42, CurrentTime: 150}}
	out := applyGroundIdentityHistoryWithKills([]*track{groundInitializationTestTrack()}, metadata, true, kills)
	if len(out) != 2 {
		t.Fatal("victim evidence suppressed as initialization")
	}
	applyGroundKillLifetimes(out, kills)
	if !out[0].hasEnd || out[0].end != 150 || out[0].retired {
		t.Fatal("real early death lost")
	}
	for _, wrong := range []packetkill.KillEntry{{VictimUid: 42, CurrentTime: 150}, {HasVictimUID: true, VictimUid: 43, CurrentTime: 150}, {HasVictimUID: true, VictimUid: 42, CurrentTime: 200}} {
		out = applyGroundIdentityHistoryWithKills([]*track{groundInitializationTestTrack()}, metadata, true, []packetkill.KillEntry{wrong})
		if len(out) != 1 {
			t.Fatal("unrelated report retained phantom prefix")
		}
	}
	info.ConstructionTime = 0
	stationary := groundInitializationTestTrack()
	for i := range stationary.samples {
		stationary.samples[i].x = 50
	}
	out = applyGroundIdentityHistory([]*track{stationary}, &groundmeta.Parser{All: []groundmeta.Info{info}, Ends: map[uint64]uint32{1000: 350}}, true)
	if len(out) != 1 || len(out[0].samples) != 4 || !out[0].hasEnd || out[0].end != 350 {
		t.Fatal("known stationary vehicle/death discarded")
	}
}
