package main

import (
	"math"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

func withThreatProfile(t *testing.T, key string, p groundThreatProfile) {
	t.Helper()
	old, exists := groundThreats[key]
	groundThreats[key] = p
	t.Cleanup(func() {
		if exists {
			groundThreats[key] = old
		} else {
			delete(groundThreats, key)
		}
	})
}

func TestMixedAirDefenceUsesSourcedWeaponRangesOnly(t *testing.T) {
	const key = "synthetic_mixed_aaa"
	withThreatProfile(t, key, groundThreatProfile{
		Classification: "spaam", ClassificationSource: "synthetic:unitClass",
		Envelopes: []groundThreatEnvelope{
			{Kind: "gun", Range: 2500, Source: "test:gun"},
			{Kind: "gun", Range: 3000, Source: "test:gun_variant"},
			{Kind: "missile", Range: 12000, Source: "test:missile.guidance"},
			{Kind: "radar", Range: 50000, Source: "test:radar"},
			{Kind: "missile", Range: 100000},
			{Kind: "missile", Range: math.Inf(1), Source: "test:invalid"},
			{Kind: "gun", Range: -1, Source: "test:invalid"},
		},
	})
	tr := &track{key: trackKey{groundEntity: true, id: groundUnitUIDNamespace},
		samples: []acmiSample{{t: 1000, x: 100, y: 200, z: 300}}}
	applyGroundModelEvidence([]*track{tr}, replayGroundModelEvidence(map[uint64]groundmeta.Info{
		0: {RawModel: "tankModels/" + key, Source: "ecs:unit__className"},
	}))
	data := renderTestACMI(t, []*track{tr}, nil, nil)
	if got := modelThreatEnvelopes("tankModels/" + strings.ToUpper(key)); len(got) != 2 || got[0].Range != 12000 {
		t.Fatalf("case-normalized lookup failed: %+v", got)
	}
	for _, want := range []string{"Type=Ground+Vehicle+AntiAircraft", "EngagementRange=12000", "EngagementMode=1", "EngagementRange2=3000", "EngagementMode2=1", "WT_EngagementRangeKind=missile", "WT_EngagementRangeKind2=gun", "WT_EngagementRangeMode=StaticModelReference"} {
		if !strings.Contains(data, want) {
			t.Fatalf("missing %q: %s", want, data)
		}
	}
	if strings.Contains(data, "50000") || strings.Contains(data, "100000") || strings.Contains(data, "EngagementRange3=") {
		t.Fatalf("radar/unsourced/extra envelope leaked: %s", data)
	}
}

func TestAirDefenceCatalogNeedsExplicitClassificationAndIdentity(t *testing.T) {
	const key = "synthetic_unclassified_aaa"
	withThreatProfile(t, key, groundThreatProfile{Classification: "spaa", Envelopes: []groundThreatEnvelope{{Kind: "gun", Range: 3000, Source: "test"}}})
	if got := modelThreatEnvelopes("tankModels/" + key); len(got) != 0 {
		t.Fatalf("classification without source accepted: %+v", got)
	}
	withThreatProfile(t, key, groundThreatProfile{Classification: "radar_support", ClassificationSource: "test:radar_role", Envelopes: []groundThreatEnvelope{{Kind: "gun", Range: 50000, Source: "test:radar"}}})
	if got := modelThreatEnvelopes(key); len(got) != 0 {
		t.Fatalf("radar support gained weapon envelope: %+v", got)
	}
	if got := modelThreatEnvelopes("tankModels/not_a_catalog_unit"); len(got) != 0 {
		t.Fatalf("unrelated raw model gained envelope: %+v", got)
	}
	air := &track{key: trackKey{air: true}, groundModel: &groundModelInfo{RawModel: key}}
	if got := groundThreatProperties(air); len(got) != 0 {
		t.Fatalf("ground profile leaked to aircraft: %+v", got)
	}
}

func TestEmbeddedThreatCatalogRangesAreExplicit(t *testing.T) {
	for key, profile := range groundThreats {
		if key != strings.ToLower(key) {
			t.Errorf("%s: catalog key is not canonical", key)
		}
		if profile.ClassificationSource == "" {
			t.Errorf("%s: classification has no source", key)
		}
		if profile.RadarState != "present" && profile.RadarState != "absent" && profile.RadarState != "unknown" {
			t.Errorf("%s: missing radar equipment classification", key)
		}
		if (profile.RadarState == "present" || profile.RadarState == "absent") && strings.TrimSpace(profile.RadarSource) == "" {
			t.Errorf("%s: radar classification has no evidence", key)
		}
		if len(profile.Envelopes) == 0 && profile.MissingReason == "" {
			t.Errorf("%s: missing envelope has no explanation", key)
		}
		if profile.Classification == "radar_support" && len(profile.Envelopes) != 0 {
			t.Errorf("%s: radar-only support has weapon envelope", key)
		}
		for _, envelope := range profile.Envelopes {
			if !validThreatEnvelope(envelope) || envelope.Basis == "" || envelope.FieldPath == "" {
				t.Errorf("%s: invalid or unsourced envelope: %+v", key, envelope)
			}
		}
	}
}

func TestPinnedAirDefenceProfilesKeepCoreRangesAndExcludeAuxiliaries(t *testing.T) {
	for raw, want := range map[string]float64{"fr_crotale_ng": 12000, "sw_crotale_ng": 12000, "uk_rooikat_za_35": 3000, "ussr_zsu_23_4": 2500, "ussr_zsu_37_2": 4000} {
		envelopes := modelThreatEnvelopes("tankModels/" + raw)
		if len(envelopes) != 1 || envelopes[0].Range != want {
			t.Errorf("%s: got %+v, want one radius %v", raw, envelopes, want)
		}
	}
	for _, raw := range []string{"germ_9a35_m", "sw_nomads", "sw_lvrbv_701"} {
		envelopes := modelThreatEnvelopes(raw)
		if len(envelopes) != 1 || envelopes[0].Kind != "missile" {
			t.Errorf("%s: auxiliary MG/smoke gained circle: %+v", raw, envelopes)
		}
	}
	if _, ok := groundThreats["germ_vt_1_2"]; ok {
		t.Error("VT1-2 tank was confused with VT1 missile system")
	}
	if e := modelThreatEnvelopes("us_halftrack_m16"); len(e) != 0 {
		t.Errorf("radarless M16 still has a threat circle: %+v", e)
	}
	if e := modelThreatEnvelopes("ussr_btr_152a"); len(e) != 0 {
		t.Errorf("radarless BTR-152A still has a threat circle: %+v", e)
	}
	for _, raw := range []string{"searchlight", "searchlighttop", "fr_samp_t_fcs"} {
		if got := modelThreatEnvelopes(raw); len(got) != 0 {
			t.Errorf("unarmed %s has weapon circle: %+v", raw, got)
		}
	}
}

func TestThreatCirclesRespectRadarlessGunOnlyPolicy(t *testing.T) {
	gun := groundThreatEnvelope{Kind: "gun", Range: 3000, Source: "test:gun"}
	missile := groundThreatEnvelope{Kind: "missile", Range: 8000, Source: "test:missile"}
	for _, tc := range []struct {
		name, radar, source string
		envelopes           []groundThreatEnvelope
		want                int
	}{
		{"radarless_gun", "absent", "test:complete-unit-without-radar", []groundThreatEnvelope{gun}, 0},
		{"radar_gun", "present", "test:tracking-radar", []groundThreatEnvelope{gun}, 1},
		{"radarless_missile", "absent", "test:optical-only", []groundThreatEnvelope{missile}, 1},
		{"radarless_mixed", "absent", "test:optical-only", []groundThreatEnvelope{gun, missile}, 2},
		{"unknown_gun", "unknown", "", []groundThreatEnvelope{gun}, 1},
		{"unsourced_absence", "absent", "", []groundThreatEnvelope{gun}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withThreatProfile(t, tc.name, groundThreatProfile{
				Classification: "spaa", ClassificationSource: "test:class",
				RadarState: tc.radar, RadarSource: tc.source, Envelopes: tc.envelopes,
			})
			if got := modelThreatEnvelopes(tc.name); len(got) != tc.want {
				t.Fatalf("got %+v, want %d envelopes", got, tc.want)
			}
		})
	}
}
