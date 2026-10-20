package main

import (
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
)

func TestDestroyedAircraftIsRemovedWithoutEndingLaunchedMissile(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "J-11B", "Alpha", 1)
	aircraft.samples = []acmiSample{{t: 1000}, {t: 1100, x: 10}, {t: 1200, x: 20}}
	aircraft.hasEnd, aircraft.end = true, 1100
	tracks := appendMissileTracks([]*track{aircraft}, []missileTrack{{
		ID: 42, Name: "PL-12", ParentUID: 77, HasParent: true,
		Samples: []acmiSample{{t: 1050}, {t: 1150, x: 30}, {t: 1250, x: 60}},
		EndTime: 1300, HasEnd: true,
	}})
	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, tracks, slot, nil, nil)
	frames := parseTestACMI(data)
	if got := frameFindingLine(frames, 0.100, "-1"); got != "-1" {
		t.Fatalf("aircraft deletion at kill time = %q", got)
	}
	if got := frameFindingLine(frames, 0.100, "0,Event=Destroyed|1"); got != "0,Event=Destroyed|1" {
		t.Fatalf("aircraft destruction event = %q", got)
	}
	if frameHasObjectPrefixAfter(frames, 0.100, "1,T=") {
		t.Fatal("destroyed aircraft was updated after deletion")
	}
	if !frameHasObjectPrefixAfter(frames, 0.100, "2,T=") {
		t.Fatal("missile flight was cut off by parent aircraft destruction")
	}
	if got := frameFindingLine(frames, 0.300, "-2"); got != "-2" {
		t.Fatalf("missile deletion must keep its original time, got %q", got)
	}
}
