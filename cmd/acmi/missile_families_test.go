package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func familyTestTrack(weapons ...string) *track {
	var ammo []groundmeta.StorageWeapon
	for _, weapon := range weapons {
		ammo = append(ammo, groundmeta.StorageWeapon{Launcher: weapon, Count: 2})
	}
	tr := missileAssociationTestTrack(1, 100, 77, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1, ammo...)
	applyMissileModelNames([]*track{tr}, nil)
	return tr
}

func TestMissileFamilyCollapsesVariantsWithoutInventingOne(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	tr := familyTestTrack("us_aim_120a", "us_aim_120b", "us_aim_120c_5", "us_aim_120d")
	before := append([]acmiSample(nil), tr.samples...)
	applyMissileModelAssociations([]*track{tr}, table)
	if applyMissileFamilyNames([]*track{tr}, table) != 1 || tr.name != "AIM-120" {
		t.Fatalf("family: %+v", tr.missileModel)
	}
	info := tr.missileModel
	if info.InternalName != "" || info.Family != "AIM-120" || info.Confidence != "Inferred" || info.Source != "ReplayOwnerLoadoutFamily" || len(info.ModelCandidates) != 4 {
		t.Fatalf("variant invented: %+v", info)
	}
	if !reflect.DeepEqual(before, tr.samples) {
		t.Fatal("family naming changed motion")
	}
	if p := strings.Join(missileModelProperties(tr), ","); !strings.Contains(p, "WT_MissileVariantUnresolved=1") || !strings.Contains(p, "WT_MissileModelSource=ReplayOwnerLoadoutFamily") {
		t.Fatal(p)
	}
	tr.missileModel.Model, tr.missileModel.InternalName, tr.name = "AIM-120D", "us_aim_120d", "AIM-120D"
	if applyMissileFamilyNames([]*track{tr}, table) != 0 || tr.name != "AIM-120D" {
		t.Fatal("known variant replaced")
	}
	if applyMissileFamilyNames([]*track{familyTestTrack("us_aim_120a", "us_aim_120b")}, nil) != 0 {
		t.Fatal("opt out ignored")
	}
}

func TestMissileFamilySeekerSeparatesAirToAirFamilies(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		ammo []string
		want string
	}{
		{[]string{"us_aim9l_sidewinder", "us_aim9m_sidewinder", "us_aim_120d"}, "AIM-9"},
		{[]string{"su_r_73", "su_r_73e", "su_r_27er"}, "R-73"},
		{[]string{"us_aim9m_sidewinder", "su_r_73"}, "AAM (IR)"},
	} {
		tr := familyTestTrack(tt.ammo...)
		tr.projectileSource.Template = "rocket"
		missileAssociationAttachSeeker(tr, missileAssociationIRMotion())
		applyMissileModelNames([]*track{tr}, nil)
		applyMissileModelAssociations([]*track{tr}, table)
		applyMissileFamilyNames([]*track{tr}, table)
		guidance, err := parseMissileGuidanceJSON(embeddedMissileGuidanceJSON)
		if err != nil {
			t.Fatal(err)
		}
		applyMissileGuidance([]*track{tr}, guidance)
		if got := trackName(tr, nil, nil); got != tt.want {
			t.Fatalf("%v => %s, info %+v, guidance %+v", tt.ammo, got, tr.missileModel, tr.missileGuidance)
		}
	}
}

func TestCarrierOptionsInferOnlyFamilyAndNeedExactParent(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := newTestTrack(true, 77, "test_f16", "Pilot", 1)
	catalog := missileCarrierCatalog{Families: missileCarriers.Families, Carriers: map[string]missileCarrierProfile{
		"test_f16": {Weapons: []string{"us_aim9m_sidewinder", "us_aim_120d"}, Complete: map[string]bool{"AAM": true}, Scope: "DeclaredWeaponSlotsAndCommonWeapons"},
	}}
	makeTrack := func() *track {
		tr := familyTestTrack()
		tr.parent = parent
		tr.projectileSource.Template = "rocket"
		missileAssociationAttachSeeker(tr, missileAssociationIRMotion())
		return tr
	}
	tr := makeTrack()
	if applyMissileFamilyNamesWithCatalog([]*track{tr}, table, catalog) != 1 || tr.name != "AIM-9" || tr.missileModel.InternalName != "" || tr.missileModel.Source != "CarrierOptionsGuidanceFamily" {
		t.Fatalf("carrier fallback: %+v", tr.missileModel)
	}
	for _, scenario := range []string{"no parent", "incomplete", "mixed", "conflict"} {
		tr := makeTrack()
		current := catalog
		current.Carriers = map[string]missileCarrierProfile{"test_f16": catalog.Carriers["test_f16"]}
		switch scenario {
		case "no parent":
			tr.parent = nil
		case "incomplete":
			profile := current.Carriers["test_f16"]
			profile.Complete = map[string]bool{"AAM": false}
			current.Carriers["test_f16"] = profile
		case "mixed":
			profile := current.Carriers["test_f16"]
			profile.Weapons = []string{"us_aim9m_sidewinder", "su_r_73"}
			current.Carriers["test_f16"] = profile
		case "conflict":
			missileAssociationAttachSeeker(tr, missileAssociationRadarMotion())
		}
		if applyMissileFamilyNamesWithCatalog([]*track{tr}, table, current) != 0 || tr.missileModel.Model != "" {
			t.Fatalf("unsafe %s: %+v", scenario, tr.missileModel)
		}
	}
}

func TestRecordedSeekerClassifiesUnknownAAMWithoutModel(t *testing.T) {
	guidance, err := parseMissileGuidanceJSON(embeddedMissileGuidanceJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		motion *projectiles.RawMotion
		want   string
	}{
		{missileAssociationIRMotion(), "IR"}, {missileAssociationRadarMotion(), "SARH"},
		{&projectiles.RawMotion{Complete: true, HasTail: true, HasSeekerBlock: true, SeekerBitLength: 310, SeekerRawBits: make([]byte, 39)}, "Unknown"},
	} {
		tr := familyTestTrack()
		tr.projectileSource.Template = "rocket"
		missileAssociationAttachSeeker(tr, tt.motion)
		applyMissileGuidance([]*track{tr}, guidance)
		if tr.missileGuidance.Type != tt.want {
			t.Fatalf("got %+v, want %s", tr.missileGuidance, tt.want)
		}
	}
}

func TestSARHCarrierFamiliesIncludingContainerMounts(t *testing.T) {
	table, err := loadMissileModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	for carrier, want := range map[string]string{"fa_18c_early": "AIM-7", "fa_18c_switzerland": "AIM-7", "mig_29_9_12_sniper": "R-27"} {
		t.Run(carrier, func(t *testing.T) {
			profile := missileCarriers.Carriers[carrier]
			if !profile.Complete["AAM"] {
				t.Fatalf("missing carrier weapons: %+v", profile)
			}
			parent, _ := newTestTrack(true, 77, carrier, "Pilot", 1)
			tr := familyTestTrack()
			tr.parent, tr.projectileSource.Template = parent, "rocket"
			missileAssociationAttachSeeker(tr, missileAssociationRadarMotion())
			if applyMissileFamilyNames([]*track{tr}, table) != 1 || tr.name != want || tr.missileModel.InternalName != "" || tr.missileModel.Confidence != "Inferred" {
				t.Fatalf("SARH family: %+v", tr.missileModel)
			}
		})
	}
}
