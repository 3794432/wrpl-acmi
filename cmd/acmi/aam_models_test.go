package main

import "testing"

func TestFullMissileCatalogNamesIndependentOfReplay(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	for alias, model := range map[string]string{
		"us_aim_120d": "AIM-120D", "us_aim_120d_default": "AIM-120D",
		"us_aim_120b": "AIM-120B", "su_rvv_sd": "R-77-1", "su_r_77": "R-77",
		"br_maa_1": "MAA-1", "su_rvv_ae": "R-77 (RVV-AE)",
		"su_r_13m": "R-13M", "us_aim7p_sparrow_fa_18": "AIM-7P",
		"us_aim_54b": "us_aim_54b", "su_pl5e2": "PL-5E II",
	} {
		t.Run(alias, func(t *testing.T) {
			entry, ok := table.Aliases[alias]
			if !ok || entry.Model != model || entry.Role != "" || entry.Source == "" {
				t.Fatalf("alias %s = %+v", alias, entry)
			}
			// No replay hash or current sample is supplied: any replay with
			// this explicit weapon report can use the same native display.
			tr := missileNameTestTrack(1, missileNameTestSource(77, 1000, 105, 8, "rocket+arh_shell"), true)
			applyMissileModelNamesWithAliases([]*track{tr}, &missileNameKillParser{Kills: []missileNameKill{{Time: 1000, KillerUID: 77, Weapon: alias}}}, table.killAliases())
			if tr.name != model || tr.missileModel.Source != "KillReportMatched" {
				t.Fatalf("display %+v, name %s", tr.missileModel, tr.name)
			}
			if destructionWeaponName(alias) != model {
				t.Fatalf("destruction name for %s", alias)
			}
		})
	}
}

func TestShortMissileTokensPropagateWithoutInventingNumericID(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	anchor := missileNameTestTrack(1, missileNameTestSource(77, 1000, 105, 8, "rocket+arh_shell"), true)
	sibling := missileNameTestTrack(2, missileNameTestSource(88, 3000, 105, 8, "rocket+arh_shell"), true)
	other := missileNameTestTrack(3, missileNameTestSource(88, 3000, 104, 8, "rocket+arh_shell"), true)
	otherTemplate := missileNameTestTrack(4, missileNameTestSource(88, 3000, 105, 8, "rocket"), true)
	applyMissileModelNamesWithAliases([]*track{anchor, sibling, other, otherTemplate}, &missileNameKillParser{Kills: []missileNameKill{{Time: 1000, KillerUID: 77, Weapon: "us_aim_120d"}}}, table.killAliases())
	if anchor.name != "AIM-120D" || sibling.name != "AIM-120D" || sibling.missileModel.HasWeaponID {
		t.Fatalf("short token: %+v / %+v", anchor.missileModel, sibling.missileModel)
	}
	for _, tr := range []*track{other, otherTemplate} {
		if tr.missileModel.Model != "" {
			t.Fatalf("group leak: %+v", tr.missileModel)
		}
	}
	conflicting := missileNameTestTrack(5, missileNameTestSource(99, 5000, 105, 8, "rocket+arh_shell"), true)
	applyMissileModelNamesWithAliases([]*track{anchor, sibling, conflicting}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1000, KillerUID: 77, Weapon: "us_aim_120d"}, {Time: 5000, KillerUID: 99, Weapon: "us_aim_120b"},
	}}, table.killAliases())
	for _, tr := range []*track{anchor, sibling, conflicting} {
		if tr.missileModel.Model != "" {
			t.Fatalf("conflicting reports guessed: %+v", tr.missileModel)
		}
	}
}

func TestZeroMissileTokenDoesNotPropagate(t *testing.T) {
	anchor := missileNameTestTrack(1, missileNameTestSource(77, 1000, 0, 8, "rocket+arh_shell"), true)
	sibling := missileNameTestTrack(2, missileNameTestSource(88, 3000, 0, 8, "rocket+arh_shell"), true)
	applyMissileModelNames([]*track{anchor, sibling}, &missileNameKillParser{Kills: []missileNameKill{{Time: 1000, KillerUID: 77, Weapon: "cn_pl12"}}})
	if anchor.name != "PL-12" || sibling.missileModel.Model != "" {
		t.Fatalf("zero token propagation: %+v / %+v", anchor.missileModel, sibling.missileModel)
	}
}
