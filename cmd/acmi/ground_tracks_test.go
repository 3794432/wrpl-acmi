package main

import (
	"math"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/game"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

func TestAppendGroundTracksPreservesExistingIDsAndEntityGenerations(t *testing.T) {
	air, _ := newTestTrack(true, 77, "F-16C", "Alpha", 1)
	legacy := &track{key: trackKey{id: 0x400a49}, samples: []acmiSample{{t: 100}}}
	first := &packetecs2.Entity{Template: "tank"}
	second := &packetecs2.Entity{Template: "tank"}
	paths := map[uint64][]game.SpaceTime{
		0x400a49: {{Time: 200, X: 1}, {Time: 200, X: 2}, {Time: 300, X: math.NaN()}},
		0x800a49: {{Time: 400, X: 3}},
	}
	tracks := appendGroundTracks([]*track{air, legacy}, paths, map[uint64]*packetecs2.Entity{0x400a49: first, 0x800a49: second}, nil)
	if len(tracks) != 4 || tracks[0] != air || tracks[1] != legacy {
		t.Fatalf("existing tracks moved or full identities were lost: %+v", tracks)
	}
	if tracks[2].entity != first || tracks[3].entity != second || tracks[2].key == legacy.key {
		t.Fatal("legacy handle or recycled entity generation overwrote a ground track")
	}
	if len(tracks[2].samples) != 1 || tracks[2].samples[0].x != 2 {
		t.Fatal("invalid or superseded ground positions were retained")
	}
}

func TestGroundTrackCreationAndObservedRemoval(t *testing.T) {
	air, slot := newTestTrack(true, 77, "F-16C", "Alpha", 1)
	air.samples = []acmiSample{{t: 1000}, {t: 2000}}
	entity := &packetecs2.Entity{Template: "fortification"}
	entity.Data.AddComponent(&packetecs2.Component{Value: int32(2)}, "team")
	tracks := appendGroundTracks([]*track{air}, map[uint64][]game.SpaceTime{
		0x400a49: {{Time: 500, X: 100, Y: 20, Z: 200}, {Time: 1100, X: 150}, {Time: 1700, X: 999}},
	}, map[uint64]*packetecs2.Entity{0x400a49: entity}, map[uint64]uint32{0x400a49: 1500})
	if got := acmiReferenceBase(tracks, nil); got != 1000 {
		t.Fatalf("ground track shifted the existing replay origin to %d", got)
	}
	data := renderTestACMI(t, tracks, slot, nil)
	frames := parseTestACMI(data)
	creation := frameFindingLine(frames, -0.500, "2,")
	if !strings.Contains(creation, "Type=Ground+Static+Building,Coalition=Red,Color=Red") {
		t.Fatalf("ground type/team or creation time lost: %q", creation)
	}
	if got := bombPositionComponentCount(creation); got != 3 {
		t.Fatalf("ground position fabricated an orientation: %d components", got)
	}
	if countObjectLines(frames, "2,") != 2 || frameFindingLine(frames, 0.500, "-2") != "-2" {
		t.Fatal("ground lifecycle did not stop at its recorded end")
	}
	if strings.Contains(data, "Destroyed|2") || strings.Contains(creation, "LockedTarget") {
		t.Fatal("ground removal invented a kill or aircraft radar state")
	}
}

func TestGroundOnlyRecordingHasRealReferenceOrigin(t *testing.T) {
	tracks := appendGroundTracks(nil, map[uint64][]game.SpaceTime{42: {{Time: 1234, X: 1}}}, nil, nil)
	if got := acmiReferenceBase(tracks, nil); got != 1234 {
		t.Fatalf("ground-only reference origin=%d, want 1234", got)
	}
}

func TestGroundReplayUIDZeroIsExplicitAndNotAnAircraft(t *testing.T) {
	air, slot := newTestTrack(true, 0, "F-16C", "Alpha", 1)
	tracks := appendGroundTracks([]*track{air}, map[uint64][]game.SpaceTime{
		groundUnitUIDNamespace: {{Time: 1000, X: 1, Y: 2, Z: 3}},
	}, nil, nil)
	if len(tracks) != 2 || trackName(tracks[1], slot, nil) != "Ground unit 0" {
		t.Fatal("valid ground UID zero was dropped or misnamed")
	}
	data := renderTestACMI(t, tracks, slot, nil)
	if !strings.Contains(data, "WT_IdentitySource=ReplayUnitUID,WT_UnitUID=0") ||
		!strings.Contains(data, "Type=Ground+Vehicle,Coalition=Neutral") {
		t.Fatal("unknown ground metadata was replaced with an aircraft or guessed team")
	}
}

func TestLegacyGroundEntityIndexRetainsMoreThanElevenBits(t *testing.T) {
	const index = uint64(0x12345)
	const packed = index<<8 | 3
	if got := groundEntityIndex(packed); got != uint32(index) {
		t.Fatalf("ground index truncated: %#x, want %#x", got, index)
	}
}
