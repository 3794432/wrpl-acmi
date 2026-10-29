package main

import (
	"reflect"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

func TestEmbeddedAGMNamesAndOptOut(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	for alias, model := range map[string]string{
		"su_kh_29td": "Kh-29TD", "su_kh_38mt": "Kh-38MT",
		"us_agm_65d": "AGM-65D", "us_agm_88c": "AGM-88C",
		"cn_ld10": "LD-10", "us_agm_12c_bullpup": "AGM-12C",
		"us_hellfire_agm_114_k": "AGM-114K", "spike_er": "Spike ER",
		"su_lmur": "LMUR", "us_agm_119a_penguin": "AGM-119A Penguin", "us_agm_65k": "us_agm_65k",
	} {
		entry, ok := table.Aliases[alias]
		if !ok || entry.Model != model || entry.Role != "AGM" || entry.Source == "" {
			t.Fatalf("AGM %s = %+v, want %s", alias, entry, model)
		}
	}
	if table, err := loadMissileModels("off"); err != nil || table != nil {
		t.Fatalf("opt out: %+v, %v", table, err)
	}
}

func TestAGMKillNamesDoNotCrossWeaponRoles(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	// Same owner, weapon ID, template and end time: role must separate them.
	agm := missileAssociationTestTrack(1, 100, 11, 35, missileNameArmamentTriggerAGM, "rocket", 1)
	aam := missileAssociationTestTrack(2, 100, 11, 35, missileNameArmamentTriggerAAM, "rocket", 1)
	kills := &missileNameKillParser{Kills: []missileNameKill{{Time: 2000, KillerUID: 11, Weapon: "su_kh_29td"}}}
	before := append([]acmiSample(nil), agm.samples...)
	applyMissileModelNamesWithAliases([]*track{agm, aam}, kills, table.killAliases())
	if agm.missileModel.Model != "Kh-29TD" || agm.missileModel.Source != "KillReportMatched" || aam.missileModel.Model != "" {
		t.Fatalf("role leak: AGM=%+v AAM=%+v", agm.missileModel, aam.missileModel)
	}
	if !reflect.DeepEqual(before, agm.samples) {
		t.Fatal("model naming altered flight samples")
	}
	applyMissileModelNamesWithAliases([]*track{agm, aam}, &missileNameKillParser{Kills: []missileNameKill{{Time: 2000, KillerUID: 11, Weapon: "su_r_73"}}}, table.killAliases())
	if agm.missileModel.Model != "" || aam.missileModel.Model == "" {
		t.Fatalf("reverse role leak: AGM=%+v AAM=%+v", agm.missileModel, aam.missileModel)
	}
}

func TestAGMOwnerLoadoutRequiresUniqueModel(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	for _, mixed := range []bool{false, true} {
		ammo := []groundmeta.StorageWeapon{{Launcher: "su_kh_29td", Count: 2}}
		if mixed {
			ammo = append(ammo, groundmeta.StorageWeapon{Launcher: "su_kh_38mt", Count: 2})
		}
		tr := missileAssociationTestTrack(1, 100, 11, 35, missileNameArmamentTriggerAGM, "rocket", 1, ammo...)
		applyMissileModelNames([]*track{tr}, nil)
		applyMissileModelAssociations([]*track{tr}, table)
		if mixed {
			if tr.missileModel.Model != "" || len(tr.missileModel.ModelCandidates) != 2 {
				t.Fatalf("mixed AGM loadout guessed: %+v", tr.missileModel)
			}
		} else if tr.missileModel.Model != "Kh-29TD" || tr.missileModel.Source != missileAssociationSourceMatched {
			t.Fatalf("unique AGM missing: %+v", tr.missileModel)
		}
	}
}

func TestAGMAliasCannotNameAAMAndUnknownStoreBlocksUniqueness(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	aam := missileAssociationTestTrack(1, 100, 11, 35, missileNameArmamentTriggerAAM, "rocket", 1,
		groundmeta.StorageWeapon{Launcher: "su_kh_29td", Count: 2})
	unknown := missileAssociationTestTrack(2, 100, 11, 36, missileNameArmamentTriggerAGM, "rocket", 1,
		groundmeta.StorageWeapon{Launcher: "su_kh_29td", Count: 2},
		groundmeta.StorageWeapon{Launcher: "future_agm", Count: 1})
	for _, tr := range []*track{aam, unknown} {
		applyMissileModelNames([]*track{tr}, nil)
		applyMissileModelAssociations([]*track{tr}, table)
		if tr.missileModel.Model != "" {
			t.Fatalf("unsafe unique identity: %+v", tr.missileModel)
		}
	}
}
