package main

import (
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
)

func TestMissileModelExportPreservesManualNameAndINSLabel(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "J-11B", "Alpha", 1)
	sample := missileINSTestSample(0.3125, true, true)
	sample.t = 1000
	tracks := appendMissileTracks([]*track{aircraft}, []missileTrack{{
		ID: 42, Name: "PL-12", ParentUID: 77, HasParent: true,
		Samples: []acmiSample{sample}, EndTime: 1200, HasEnd: true,
	}})
	tracks[1].missileModel = &missileModelInfo{
		WeaponID: 32, HasWeaponID: true, Model: "PL-12", InternalName: "cn_pl12",
		Source: "KillReportMatched", Confidence: "Matched",
	}
	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, tracks, slot,
		map[string]string{"missile:42": "Custom missile"}, nil)
	for _, expected := range []string{
		"Name=Custom missile,CallSign=Custom missile", "Parent=1",
		"WT_WeaponID=32", "WT_MissileModel=PL-12", "WT_MissileInternalName=cn_pl12",
		"WT_InertialTime=0.312500", "INS 0.31 s",
	} {
		if !strings.Contains(data, expected) {
			t.Fatalf("ACMI missing %q", expected)
		}
	}
}
