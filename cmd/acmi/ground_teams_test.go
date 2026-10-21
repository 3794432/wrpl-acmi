package main

import (
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

func TestGroundTeamsUseExplicitUIDAndTeamWithoutModel(t *testing.T) {
	blue, red, unknown := int32(1), int32(2), int32(3)
	tracks := []*track{}
	for uid := uint64(0); uid < 6; uid++ {
		tracks = append(tracks, &track{
			key:     trackKey{groundEntity: true, id: groundUnitUIDNamespace | uid},
			samples: []acmiSample{{t: 1000, x: 100, y: 200, z: 300}},
		})
	}
	air, _ := newTestTrack(true, 0, "F-16C", "Alpha", 1)
	tracks = append(tracks, air)
	count := applyGroundTeamEvidence(tracks, map[uint64]groundmeta.Info{
		0: {Team: &blue, TeamSource: "ecs:team"},
		1: {Team: &red, TeamSource: "ecs:team", ClassConflict: true},
		2: {Team: &unknown, TeamSource: "ecs:team"},
		3: {Team: &blue},
		4: {Team: &blue, TeamSource: "ecs:team", Conflict: true},
		5: {TeamSource: "ecs:team"},
	})
	if count != 2 || air.groundTeam != 0 || air.groundTeamSource != "" {
		t.Fatal("team evidence crossed UID namespaces or accepted unproven teams")
	}
	for i, want := range []string{"Blue", "Red", "Neutral", "Neutral", "Neutral", "Neutral"} {
		if got := trackCoalition(tracks[i], nil); got != want {
			t.Fatalf("ground UID %d: coalition %s, want %s", i, got, want)
		}
	}
	data := renderTestACMI(t, tracks[:2], nil, nil)
	for _, want := range []string{"Coalition=Blue,Color=Blue", "Coalition=Red,Color=Red", "WT_GroundTeam=1", "WT_GroundTeam=2", "WT_GroundTeamSource=ecs:team"} {
		if !strings.Contains(data, want) {
			t.Fatalf("ACMI lacks %q", want)
		}
	}
	if props := groundTeamProperties(tracks[2]); len(props) != 0 {
		t.Fatalf("unknown team fabricated provenance: %v", props)
	}
}
