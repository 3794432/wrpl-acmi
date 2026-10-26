package groundmeta

import "testing"

func TestGroundModelIdentityAllowsExplicitUnitDirectory(t *testing.T) {
	for _, pair := range [][2]string{
		{"ussr_pantsyr_s1", "tankModels/ussr_pantsyr_s1"},
		{"sw_crotale_ng", "tankModels/sw_crotale_ng"},
		{"nt_ammo_factory", "structures/nt_ammo_factory"},
		{"us_forrestal", "ships/us_forrestal"},
		{"truck", "lightVehicles/truck"},
		{"tankModels\\ussr_zprk_2s6", "tankModels/ussr_zprk_2s6"},
	} {
		if !sameGroundModel(pair[0], pair[1]) || !sameGroundModel(pair[1], pair[0]) {
			t.Errorf("equivalent models rejected: %q", pair)
		}
	}
}

func TestGroundModelIdentityRetainsRealConflicts(t *testing.T) {
	for _, pair := range [][2]string{
		{"ussr_pantsyr_s1", "tankModels/ussr_zprk_2s6"},
		{"tankModels/unit", "ships/unit"},
		{"unverified/unit", "unit"},
		{"other/unit", "tankModels/unit"},
		{"tankModels/sub/unit", "unit"},
		{"", "tankModels/unit"},
	} {
		if sameGroundModel(pair[0], pair[1]) || sameGroundModel(pair[1], pair[0]) {
			t.Errorf("different models accepted: %q", pair)
		}
	}
}
