package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

func TestSourcedGroundEngagementRangeIsModelScoped(t *testing.T) {
	const key = "test_ground_aaa_range"
	old, exists := groundModelNames[key]
	t.Cleanup(func() {
		if exists {
			groundModelNames[key] = old
		} else {
			delete(groundModelNames, key)
		}
	})
	groundModelNames[key] = groundModelLabel{Kind: "anti_aircraft", EngagementRange: 4000, EngagementRangeSource: "synthetic:test"}
	tr := &track{key: trackKey{groundEntity: true, id: groundUnitUIDNamespace}, groundModel: &groundModelInfo{RawModel: "tankModels/" + key, Source: "ecs:unit__className"}}
	props := strings.Join(groundModelProperties(tr), ",")
	if !strings.Contains(props, "EngagementRange="+strconv.Itoa(4000)) || !strings.Contains(props, "WT_EngagementRangeMode=StaticModelReference") || !strings.Contains(props, "WT_EngagementRangeSource=synthetic:test") {
		t.Fatalf("sourced radius missing: %s", props)
	}
	groundModelNames[key] = groundModelLabel{Kind: "anti_aircraft", EngagementRange: 4000}
	if props = strings.Join(groundModelProperties(tr), ","); strings.Contains(props, "EngagementRange=") {
		t.Fatalf("unsourced radius accepted: %s", props)
	}
	tr.groundModel.RawModel = "tankModels/unknown_aaa"
	if props = strings.Join(groundModelProperties(tr), ","); strings.Contains(props, "EngagementRange=") {
		t.Fatalf("radius leaked to unrelated model: %s", props)
	}
}

func TestGroundSpecificNamesAndConflicts(t *testing.T) {
	if got := groundDisplayModelName("germ_88mm_flak36"); got != "8.8 cm FlaK 36" {
		t.Fatalf("generic AAA label hides gun model: %q", got)
	}
	if got := groundDisplayModelName("tankModels/ussr_t_80bvm"); got != "T-80BVM" {
		t.Fatalf("replay model path did not resolve: %q", got)
	}
	if got := groundDisplayModelName("structures/unit_airfield_main_radar_char"); got != "unit_airfield_main_radar_char" {
		t.Fatalf("static generic label hides internal model: %q", got)
	}
	old := modelDisplayNames["test_generic_ground"]
	modelDisplayNames["test_generic_ground"] = "AAA"
	t.Cleanup(func() {
		if old == "" {
			delete(modelDisplayNames, "test_generic_ground")
		} else {
			modelDisplayNames["test_generic_ground"] = old
		}
	})
	if got := groundDisplayModelName("test_generic_ground"); got != "test_generic_ground" {
		t.Fatalf("generic role hides raw model: %q", got)
	}
	models := replayGroundModelEvidence(map[uint64]groundmeta.Info{
		0: {RawModel: "tank", Source: "ecs:unit__className", Storage: "unit_storage__tank"},
		1: {RawModel: "conflict", Conflict: true},
		2: {RawModel: "class-conflict", ClassConflict: true},
	})
	if len(models) != 1 || models[0].Kind != "" {
		t.Fatalf("conflicting identities or missing UID zero: %+v", models)
	}
	models = replayGroundModelEvidence(map[uint64]groundmeta.Info{
		4: {RawModel: "tankModels/uk_rooikat_za_35", Storage: "unit_storage__tank", Source: "ecs:unit__className"},
	})
	if models[4].Kind != "anti_aircraft" || models[4].RawModel != "tankModels/uk_rooikat_za_35" {
		t.Fatalf("specific catalog lost anti-aircraft subtype or raw path: %+v", models[4])
	}
}

func TestCrotaleNameAvoidsTacviewTankMatch(t *testing.T) {
	for _, raw := range []string{"tankModels/fr_crotale_ng", "tankModels/sw_crotale_ng"} {
		tr := &track{
			key:     trackKey{groundEntity: true, id: groundUnitUIDNamespace | 42},
			samples: []acmiSample{{t: 1000, x: 100, y: 200, z: 300}},
		}
		applyGroundModelEvidence([]*track{tr}, replayGroundModelEvidence(map[uint64]groundmeta.Info{
			42: {RawModel: raw, Source: "ecs:unit__className", Storage: "unit_storage__tank"},
		}))
		data := renderTestACMI(t, []*track{tr}, nil, nil)
		for _, want := range []string{"Name=Crotale NG,CallSign=ItO 90M", "Type=Ground+Vehicle+AntiAircraft", "WT_GroundModel=" + raw} {
			if !strings.Contains(data, want) {
				t.Fatalf("%s lacks %q: %s", raw, want, data)
			}
		}
		if got := groundACMIName(tr, "Manual name"); got != "Manual name" {
			t.Fatal("automatic alias replaced a manual name")
		}
	}
	if got := groundDisplayModelName("tankModels/ussr_t_90m_2020"); got != "T-90M" {
		t.Fatalf("real T-90M name changed: %q", got)
	}
}

func TestGroundModelsRequireExplicitUIDAndSource(t *testing.T) {
	air, _ := newTestTrack(true, 0, "F-16C", "Alpha", 1)
	ground := &track{key: trackKey{groundEntity: true, id: groundUnitUIDNamespace}}
	unbound := &track{key: trackKey{groundEntity: true, id: 0x400001}}
	tracks := []*track{air, ground, unbound}
	if got := applyGroundModelEvidence(tracks, map[uint64]groundModelInfo{0: {RawModel: "test_tank"}}); got != 0 {
		t.Fatal("a model without replay provenance was accepted")
	}
	count := applyGroundModelEvidence(tracks, map[uint64]groundModelInfo{
		0: {RawModel: "test_tank", DisplayName: "Test tank", Source: "typed unit metadata", Kind: "armor"},
		1: {RawModel: "other_tank", Source: "typed unit metadata"},
	})
	if count != 1 || ground.groundModel == nil || air.groundModel != nil || unbound.groundModel != nil {
		t.Fatal("ground evidence leaked across aircraft or EID/UID namespaces")
	}
}

func TestGroundModelNamesKeepManualOverrideAndCoordinates(t *testing.T) {
	ground := &track{
		key:  trackKey{groundEntity: true, id: groundUnitUIDNamespace | 42},
		name: "Ground unit 42", samples: []acmiSample{{t: 1000, x: 100, y: 200, z: 300}},
	}
	applyGroundModelEvidence([]*track{ground}, map[uint64]groundModelInfo{
		42: {RawModel: "test_tank", DisplayName: "Test tank, variant", Source: "unit.className", Kind: "armor"},
	})
	data := renderTestACMI(t, []*track{ground}, nil, nil)
	for _, want := range []string{"Name=Test tank\\, variant", "Type=Ground+Vehicle+Armor", "WT_GroundModel=test_tank", "WT_GroundModelSource=unit.className"} {
		if !strings.Contains(data, want) {
			t.Fatalf("model display lacks %q: %s", want, data)
		}
	}
	if ground.samples[0].x != 100 || ground.samples[0].y != 200 || ground.samples[0].z != 300 {
		t.Fatal("naming changed recorded geometry")
	}
	if got := trackName(ground, nil, map[string]string{"ground:9223372036854775850": "Custom tank"}); got != "Custom tank" {
		t.Fatalf("manual name was overwritten: %s", got)
	}
}

func TestGroundModelEvidenceDoesNotGuessUnknownOrAircraftTypes(t *testing.T) {
	ground := &track{key: trackKey{groundEntity: true, id: groundUnitUIDNamespace | 42}, name: "Ground unit 42"}
	if got := applyGroundModelEvidence([]*track{ground}, map[uint64]groundModelInfo{42: {RawModel: "f_16c", Source: "typed record", Kind: "aircraft"}}); got != 0 {
		t.Fatal("aircraft metadata was assigned to a ground unit")
	}
	applyGroundModelEvidence([]*track{ground}, map[uint64]groundModelInfo{42: {RawModel: "unknown_vehicle", Source: "unit.className"}})
	if got := groundTrackType(ground); got != "Ground+Vehicle" {
		t.Fatalf("unknown model was guessed as a subtype: %s", got)
	}
}
