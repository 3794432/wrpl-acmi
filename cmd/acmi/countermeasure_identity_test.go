package main

import (
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
	"testing"
)

func TestCountermeasureIdentityRequiresAmmoEvidence(t *testing.T) {
	const launcher = "countermeasure_large_split_launcher_jet"
	for _, tc := range []struct {
		name string
		ammo []groundmeta.StorageWeapon
		want string
	}{
		{"default flare", []groundmeta.StorageWeapon{{Launcher: launcher, Count: 30}}, "Flare"},
		{"named chaff", []groundmeta.StorageWeapon{{Launcher: launcher, Bullet: "countermeasures_launcher_chaff_large", Count: 20}}, "Chaff"},
		{"mixed", []groundmeta.StorageWeapon{{Launcher: launcher, Count: 30}, {Launcher: launcher, Bullet: "countermeasures_launcher_chaff_large", Count: 20}}, ""},
		{"empty stock", []groundmeta.StorageWeapon{{Launcher: launcher, Count: 0}}, ""},
		{"unknown ammo", []groundmeta.StorageWeapon{{Launcher: launcher, Bullet: "not-a-known-bullet", Count: 5}}, ""},
		{"unknown launcher", []groundmeta.StorageWeapon{{Launcher: "countermeasure_unreviewed", Count: 5}}, ""},
		{"no evidence", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Repeat with several emitter indexes: they do not encode subtype.
			for _, index := range []uint32{0, 1, 2, 65535} {
				s := &projectiles.Projectile{WeaponRef: 14<<16 | index, OwnerWeapons: tc.ammo}
				got := countermeasureSingleAmmoIdentity(s)
				if got.Kind != tc.want {
					t.Fatalf("index %d: %+v, want %q", index, got, tc.want)
				}
			}
		})
	}
}

func TestCountermeasureAmmoCatalogTypes(t *testing.T) {
	if countermeasureAmmo.Commit != "f7952e4291e9fd7b6fe38e84020dddef925c3457" {
		t.Fatal("unreviewed catalogue revision")
	}
	if len(countermeasureAmmo.Launchers) != 4 {
		t.Fatal("unexpected launcher coverage")
	}
	for launcher, bullets := range countermeasureAmmo.Launchers {
		if bullets["bullet"] != "Flare" {
			t.Fatalf("%s default is not proven flare", launcher)
		}
		for _, kind := range bullets {
			if kind != "Chaff" && kind != "Flare" {
				t.Fatal("invalid native decoy subtype")
			}
		}
	}
}

func TestUnknownCountermeasuresDefaultToChaffWithoutInference(t *testing.T) {
	s := &projectiles.Projectile{WeaponRef: 14<<16 | 1, LeadingULEB: 185, Template: "rocket", OwnerWeapons: []groundmeta.StorageWeapon{
		{Launcher: "countermeasure_large_split_launcher_jet", Count: 30},
		{Launcher: "countermeasure_large_split_launcher_jet", Bullet: "countermeasures_launcher_chaff_large", Count: 20},
	}}
	meta := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{1: {s}}}
	got := countermeasureIdentities(meta)[s]
	if got.Kind != "Chaff" || got.Confidence != "fallback" || got.Source != "UnknownAsChaff:MixedAmmunition" {
		t.Fatalf("bad unknown policy: %+v", got)
	}
	// A different registry id and emitter index cannot turn the fallback into
	// a claimed identification. No motion/flag/ordinal heuristic is enabled.
	s.WeaponRef = 14<<16 | 65535
	s.LeadingULEB = 186
	if other := countermeasureIdentities(meta)[s]; other != got {
		t.Fatalf("raw index inferred subtype: %+v", other)
	}
}
