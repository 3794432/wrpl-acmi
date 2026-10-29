package main

import (
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func samModelTestTrack(ammo ...samAmmunition) *track {
	return &track{key: trackKey{missile: true, id: 123}, name: "SAM (launcher)",
		samples:          []acmiSample{{t: 100, x: 100, y: 200, z: 300}, {t: 200, x: 110, y: 220, z: 330}},
		projectileSource: &projectiles.Projectile{ID: 123, Template: "rocket", WeaponRef: 19 << 16},
		surfaceLaunch:    &surfaceMissileLaunch{profile: samLauncherProfile{Missiles: ammo, CandidatesComplete: true}}}
}

func TestSAMReviewedNamesCoverAllAmmoAndKeepUpgradeAmbiguity(t *testing.T) {
	for model, profile := range samLaunchers.Units {
		for _, ammo := range profile.Missiles {
			if _, ok := samMissileNames.Names[ammo.BulletName]; !ok {
				t.Fatalf("%s has no sourced name for %s", model, ammo.BulletName)
			}
		}
	}
	tr := samModelTestTrack()
	tr.surfaceLaunch.profile = samLaunchers.Units["ussr_zprk_2s6"]
	if n := applySurfaceMissileModels([]*track{tr}, samMissileNames.Names); n != 0 || len(tr.missileModel.ModelCandidates) < 2 {
		t.Fatal("2S6 default and improved missiles were collapsed into one model")
	}
}

func TestSAMModelUniqueConfigurationAndManualName(t *testing.T) {
	tr := samModelTestTrack(samAmmunition{BulletName: "asset_bullet", Guidance: "radar"})
	names := map[string]samMissileName{"asset_bullet": {Model: "5V55R", Source: "lang/units_weaponry.csv: asset_bullet"}}
	if n := applySurfaceMissileModels([]*track{tr}, names); n != 1 {
		t.Fatalf("adopted %d", n)
	}
	if tr.name != "5V55R" || tr.missileModel.InternalName != "asset_bullet" || tr.missileModel.Confidence != "Inferred" || tr.missileModel.Source != "LauncherConfigurationSingleAmmo" {
		t.Fatalf("wrong model evidence: %+v", tr.missileModel)
	}
	_, slot := newTestTrack(true, 1, "F-16C", "Player", 1)
	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, []*track{tr}, slot, map[string]string{"missile:123": "Manual name"}, nil)
	for _, field := range []string{"Name=Manual name,CallSign=Manual name", "WT_MissileModel=5V55R", "WT_MissileInternalName=asset_bullet", "WT_MissileModelSource=LauncherConfigurationSingleAmmo", "WT_MissileModelConfidence=Inferred", "WT_SAMModelEvidence=StaticLauncherConfiguration"} {
		if !strings.Contains(data, field) {
			t.Fatalf("missing %s", field)
		}
	}
}

func TestSAMModelAmbiguousIncompleteAndUntranslatedRemainUnknown(t *testing.T) {
	names := map[string]samMissileName{
		"base":    {Model: "Base missile", Source: "exact language key"},
		"upgrade": {Model: "Improved missile", Source: "exact language key"},
	}
	for _, mode := range []string{"multiple ammo", "incomplete", "missing name", "unsourced name", "incompatible seeker template", "same label different ammo"} {
		t.Run(mode, func(t *testing.T) {
			tr := samModelTestTrack(samAmmunition{BulletName: "base", Guidance: "saclos"})
			localNames := map[string]samMissileName{}
			for key, value := range names {
				localNames[key] = value
			}
			switch mode {
			case "multiple ammo":
				tr.surfaceLaunch.profile.Missiles = append(tr.surfaceLaunch.profile.Missiles, samAmmunition{BulletName: "upgrade", Guidance: "saclos"})
			case "incomplete":
				tr.surfaceLaunch.profile.CandidatesComplete = false
			case "missing name":
				delete(localNames, "base")
			case "unsourced name":
				localNames["base"] = samMissileName{Model: "Guess"}
			case "incompatible seeker template":
				tr.projectileSource.Template = "rocket+arh_shell"
			case "same label different ammo":
				tr.surfaceLaunch.profile.Missiles = append(tr.surfaceLaunch.profile.Missiles, samAmmunition{BulletName: "upgrade", Guidance: "saclos"})
				localNames["upgrade"] = localNames["base"]
			}
			if n := applySurfaceMissileModels([]*track{tr}, localNames); n != 0 || tr.missileModel.Model != "" || tr.name != "SAM (launcher)" || len(tr.missileModel.ModelCandidates) == 0 {
				t.Fatalf("uncertain configuration picked a model: %+v", tr.missileModel)
			}
		})
	}
}

func TestSAMModelPreservesKillIdentityAndCoalescesDuplicateMounts(t *testing.T) {
	ammo := samAmmunition{BulletName: "bullet", Guidance: "radar", WeaponPath: "first.blkx"}
	tr := samModelTestTrack(ammo)
	names := map[string]samMissileName{"bullet": {Model: "Configured missile", Source: "language key"}}
	tr.name = "Recorded missile"
	tr.missileModel = &missileModelInfo{Model: "Recorded missile", InternalName: "recorded", Source: "KillReportMatched", Confidence: "Matched"}
	if n := applySurfaceMissileModels([]*track{tr}, names); n != 0 || tr.name != "Recorded missile" || tr.missileModel.Source != "KillReportMatched" || tr.missileModel.Confidence != "Matched" {
		t.Fatal("static configuration replaced a kill-report identity")
	}
	tr = samModelTestTrack(ammo, samAmmunition{BulletName: "bullet", Guidance: "radar", WeaponPath: "second.blkx"})
	if n := applySurfaceMissileModels([]*track{tr}, names); n != 1 || tr.name != "Configured missile" {
		t.Fatal("duplicate mounts made identical ammunition ambiguous")
	}
}
