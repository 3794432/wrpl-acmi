package main

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func bombModelTestTrack(id, token, owner uint64, end uint32) *track {
	return &track{key: trackKey{bomb: true, id: id}, name: bombDisplayName,
		samples: []acmiSample{{t: 1000, x: 1, y: 2, z: 3}, {t: 1500, x: 4, y: 5, z: 6}},
		end:     end, hasEnd: true,
		projectileSource: &projectiles.Projectile{ID: id, Kind: bombProjectileKind, Template: "bomb",
			LeadingULEB: token, WeaponRef: 9 << 16, OwnerEID: 100 + owner,
			ParentUID: owner, HasParent: true, SpawnSample: projectiles.Sample{Time: 1000}, EndTime: end, HasEnd: true}}
}

func TestBombModelCatalogueAndOff(t *testing.T) {
	table, err := loadBombModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"us_gbu_39": "GBU-39", "us_gbu_53": "GBU-53", "su_fab_500m_62t": "FAB-500M-62", "us_agm_154a1_jsow": "AGM-154A1"} {
		if entry, ok := bombAlias(table, key); !ok || entry.Model != want || entry.Source == "" {
			t.Fatalf("%s = %+v, want %s", key, entry, want)
		}
	}
	if table, err := loadBombModels("off"); err != nil || table != nil {
		t.Fatalf("off = %+v, %v", table, err)
	}
}

func TestBombNamesMatchReportAndPropagateOnlySameReplayToken(t *testing.T) {
	table := bombModelTestTable(t)
	first := bombModelTestTrack(1, 10, 11, 2000) // No leading-minus-129 assumption.
	same := bombModelTestTrack(2, 10, 12, 4000)
	other := bombModelTestTrack(3, 20, 12, 5000)
	missile := missileNameTestTrack(4, missileNameTestSource(11, 2000, 10, 7, "rocket"), true)
	tracks := []*track{first, same, other, missile}
	before := append([]acmiSample(nil), first.samples...)
	kills := &missileNameKillParser{Kills: []missileNameKill{{Time: 2000, KillerUID: 11, Weapon: "us_gbu_39"}}}
	if got := applyBombModelNames(tracks, kills, table); got != 2 {
		t.Fatalf("named = %d, want 2", got)
	}
	if first.bombModel.Source != "KillReportMatched" || same.bombModel.Source != "ReplayWeaponTokenFromKillReport" || first.bombModel.Model != "GBU-39" || same.bombModel.Model != "GBU-39" || other.bombModel.Model != "" {
		t.Fatalf("identity mismatch: %+v %+v %+v", first.bombModel, same.bombModel, other.bombModel)
	}
	if !reflect.DeepEqual(before, first.samples) || first.end != 2000 || missile.bombModel != nil || missile.name != "Missile" {
		t.Fatal("naming changed flight, lifetime or missile objects")
	}
	if name := trackName(first, nil, map[string]string{"bomb:1": "Manual"}); name != "Manual" {
		t.Fatalf("manual override lost: %q", name)
	}
	data := renderTestACMI(t, tracks[:3], nil, nil)
	if !strings.Contains(data, "Name=GBU-39,CallSign=GBU-39") || !strings.Contains(data, "WT_BombModelConfidence=Matched") || !strings.Contains(data, "Type=Weapon+Bomb") {
		t.Fatalf("native naming or evidence missing: %.1000s", data)
	}
}

func TestBombSalvoAndMixedReportsStayConservative(t *testing.T) {
	table := bombModelTestTable(t)
	for _, sameToken := range []bool{true, false} {
		a, b := bombModelTestTrack(1, 10, 11, 2000), bombModelTestTrack(2, 10, 11, 2000)
		if !sameToken {
			b.projectileSource.LeadingULEB = 20
		}
		applyBombModelNames([]*track{a, b}, &missileNameKillParser{Kills: []missileNameKill{{Time: 2000, KillerUID: 11, Weapon: "us_gbu_39"}}}, table)
		if sameToken {
			if a.bombModel.Model != "GBU-39" || b.bombModel.Model != "GBU-39" || a.bombModel.Confidence != "Inferred" {
				t.Fatal("same-token salvo should name the type without attributing an individual hit")
			}
		} else if a.bombModel.Model != "" || b.bombModel.Model != "" {
			t.Fatal("overlapping different bomb tokens were guessed")
		}
	}
	a, b := bombModelTestTrack(1, 10, 11, 2000), bombModelTestTrack(2, 10, 12, 4000)
	applyBombModelNames([]*track{a, b}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 2000, KillerUID: 11, Weapon: "us_gbu_39"}, {Time: 4000, KillerUID: 12, Weapon: "us_gbu_53"}}}, table)
	if a.bombModel.Model != "" || b.bombModel.Model != "" || a.bombModel.ModelConflict == "" || len(a.bombModel.ModelCandidates) != 2 {
		t.Fatal("conflicting token identities were promoted")
	}
}

func TestBombWithoutConstructionOrObservedEndCannotUseKillTime(t *testing.T) {
	table := bombModelTestTable(t)
	missing := bombModelTestTrack(1, 10, 11, 2000)
	missing.projectileSource.HasEnd = false
	wire := &track{key: trackKey{bomb: true, bombWire: true, id: 2}, parent: &track{key: trackKey{air: true, id: 11}}, samples: []acmiSample{{t: 1000}}}
	large := bombModelTestTrack(3, math.MaxUint32+1, 12, 2000)
	applyBombModelNames([]*track{missing, wire, large}, &missileNameKillParser{Kills: []missileNameKill{{Time: 2000, KillerUID: 11, Weapon: "us_gbu_39"}}}, table)
	if missing.bombModel.Model != "" || wire.bombModel != nil || large.bombModel.Model != "" {
		t.Fatal("invented construction, owner or destruction evidence")
	}
}

func TestBombLoadoutUnknownAndSameNamedVariantsPreventFalseUniqueness(t *testing.T) {
	table := bombModelTestTable(t)
	for _, extra := range []string{"", "future_bomb", "us_gbu_53"} {
		tr := bombModelTestTrack(1, 10, 11, 2000)
		tr.projectileSource.OwnerWeapons = []groundmeta.StorageWeapon{{Launcher: "us_gbu_39", Count: 2}}
		if extra != "" {
			tr.projectileSource.OwnerWeapons = append(tr.projectileSource.OwnerWeapons, groundmeta.StorageWeapon{Launcher: extra, Count: 2})
		}
		applyBombModelNames([]*track{tr}, nil, table)
		if extra == "" && tr.bombModel.Model != "GBU-39" || extra != "" && tr.bombModel.Model != "" {
			t.Fatalf("loadout %q = %+v", extra, tr.bombModel)
		}
	}
	tr := bombModelTestTrack(1, 10, 11, 2000)
	tr.projectileSource.OwnerWeapons = []groundmeta.StorageWeapon{{Launcher: "us_b61_5kt", Count: 1}, {Launcher: "us_b61_30kt", Count: 1}}
	applyBombModelNames([]*track{tr}, nil, table)
	if tr.bombModel.Model != "" {
		t.Fatal("same displayed family name erased different bomb variants")
	}
}

func bombModelTestTable(t *testing.T) *missileModelTable {
	t.Helper()
	table, err := loadBombModels("embedded")
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestBombTableKeepsDefaultVariantsExactAndRejectsMalformedInput(t *testing.T) {
	valid := `{"schema":1,"generated_from":["test"],"aliases":{"fab":{"model":"FAB A","internal_name":"fab","confidence":"confirmed","source":"s"},"fab_default":{"model":"FAB B","internal_name":"fab_default","confidence":"confirmed","source":"s"}},"replays":{}}`
	if _, err := parseBombModelsJSON([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	aliases := map[string]missileWeaponAlias{
		"fab":         {model: "FAB A", internal: "fab", role: "Bomb"},
		"fab_default": {model: "FAB B", internal: "fab_default", role: "Bomb"},
	}
	if alias, ok := missileWeaponAliasForWithTable("fab_default", aliases); !ok || alias.model != "FAB B" {
		t.Fatal("bomb default variant was stripped")
	}
	if _, ok := missileWeaponAliasForWithTable("fab_default_default", aliases); ok {
		t.Fatal("an unlisted bomb alias was invented")
	}
	for _, data := range [][]byte{
		[]byte(valid + `{}`), []byte(`{"schema":2}`),
		[]byte(strings.Replace(valid, `"source":"s"`, `"source":"s","unknown":true`, 1)),
		append([]byte(valid), bytes.Repeat([]byte(" "), missileModelTableMaxFileBytes)...),
	} {
		if _, err := parseBombModelsJSON(data); err == nil {
			t.Fatal("malformed bomb catalogue accepted")
		}
	}
}
