package main

import (
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
)

func TestSAMLatestVariantPolicyCoversReviewedUnresolvedConfigurations(t *testing.T) {
	expected := map[string]string{
		"ussr_zprk_2s6":         "152mm_zur_9m311_1m",
		"il_spyder_aio":         "il_derby_er_sam",
		"cn_hq_11":              "cn_fm_3000",
		"sw_nasams_launcher":    "us_amraam_er",
		"us_sl_amraam_launcher": "us_aim_120c_7_sam",
		"jp_type_81_tansam":     "160mm_sam_arh_1c",
		"germ_wiesel_2_adwc":    "70mm_fim_92k",
		"sw_asrad_r":            "105mm_bolide",
	}
	for unit, want := range expected {
		if got := preferredSAMVariant(samLaunchers.Units[unit], false); got != want {
			t.Fatalf("%s selected %s, want %s", unit, got, want)
		}
	}
	unresolved := 0
	for unit, profile := range samLaunchers.Units {
		tr := samModelTestTrack()
		tr.surfaceLaunch.profile = profile
		applySurfaceMissileModels([]*track{tr}, samMissileNames.Names)
		if tr.missileModel.Model != "" {
			continue
		}
		unresolved++
		if n := applySurfaceMissileFallbacks([]*track{tr}, samMissileNames.Names); n != 1 || tr.missileModel.Source != samVariantFallbackSource || tr.missileModel.Confidence != "Assumed" {
			t.Fatalf("%s has no assumed fallback: %+v", unit, tr.missileModel)
		}
		found := false
		for _, ammo := range profile.Missiles {
			found = found || ammo.BulletName == tr.missileModel.InternalName
		}
		if !found || len(tr.missileModel.ModelCandidates) == 0 {
			t.Fatalf("%s invented ammo or dropped candidates", unit)
		}
	}
	if unresolved != 25 {
		t.Fatalf("reviewed unresolved configurations %d, want 25", unresolved)
	}
}

func TestSAMFallbackPreservesEvidenceAndDoesNotChangeSeekerState(t *testing.T) {
	tr := samModelTestTrack()
	tr.surfaceLaunch.profile = samLaunchers.Units["il_spyder_aio"]
	applySurfaceMissileModels([]*track{tr}, samMissileNames.Names)
	applySurfaceMissileFallbacks([]*track{tr}, samMissileNames.Names)
	_, slot := newTestTrack(true, 1, "F-16C", "Pilot", 1)
	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, []*track{tr}, slot, nil, nil)
	for _, property := range []string{"Name=I-Derby ER", "WT_MissileModelSource=UserLatestSAMVariant", "WT_MissileModelConfidence=Assumed", "WT_SAMModelAssumed=1", "WT_SAMModelEvidence=UserPreferenceFallback"} {
		if !strings.Contains(data, property) {
			t.Fatalf("missing fallback provenance: %s", property)
		}
	}
	if tr.samples[0].x != 100 || tr.samples[1].z != 330 || tr.samples[0].projectile != nil || tr.missileGuidance != nil {
		t.Fatal("display fallback changed motion, recorded seeker state or guidance")
	}
	tr.missileModel.Source, tr.missileModel.Confidence = "KillReportMatched", "Matched"
	if n := applySurfaceMissileFallbacks([]*track{tr}, samMissileNames.Names); n != 0 || tr.missileModel.Source != "KillReportMatched" {
		t.Fatal("fallback replaced known identity")
	}
	unknown := samLauncherProfile{Missiles: []samAmmunition{{BulletName: "new_unknown_a", Guidance: "radar"}, {BulletName: "new_unknown_b", Guidance: "radar"}}}
	if preferredSAMVariant(unknown, false) != "" {
		t.Fatal("unreviewed family was ranked by its spelling")
	}
	if preferredSAMVariant(samLaunchers.Units["ussr_zprk_2s6"], true) != "" {
		t.Fatal("ARH template accepted SACLOS ammo")
	}
}

func TestSAMFallbackModeValidation(t *testing.T) {
	for _, value := range []string{"latest", "off", " LATEST "} {
		if _, err := parseSAMModelFallback(value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := parseSAMModelFallback("arbitrary"); err == nil {
		t.Fatal("invalid fallback mode accepted")
	}
}
