package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func TestCountermeasurePresentationExport(t *testing.T) {
	const launcher = "countermeasure_large_split_launcher_jet"
	for _, tc := range []struct {
		name string
		ammo []groundmeta.StorageWeapon
		kind string
	}{
		{"flare", []groundmeta.StorageWeapon{{Launcher: launcher, Count: 30}}, "Flare"},
		{"chaff", []groundmeta.StorageWeapon{{Launcher: launcher, Bullet: "countermeasures_launcher_chaff_large", Count: 20}}, "Chaff"},
		{"unknown", nil, "Chaff"},
		{"mixed", []groundmeta.StorageWeapon{{Launcher: launcher, Count: 30}, {Launcher: launcher, Bullet: "countermeasures_launcher_chaff_large", Count: 20}}, "Chaff"},
	} {
		for _, realSync := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/sync=%t", tc.name, realSync), func(t *testing.T) {
				aircraft, slot := newTestTrack(true, 77, "Su-30MK2", "Alpha", 1)
				aircraft.samples = []acmiSample{{t: 0}, {t: 1000, x: 100}}
				const eid = uint64(0x400902)
				source := &projectiles.Projectile{
					ID: eid, OwnerEID: 10, ParentUID: 77, HasParent: true,
					WeaponRef: 14 << 16, Template: "rocket", OwnerWeapons: tc.ammo,
					SpawnSample: projectiles.Sample{Time: 100, X: 1.25, Y: 2.5, Z: 3.75},
					EndTime:     500, HasEnd: true,
				}
				metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{eid: {source}}}
				positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{}}
				if realSync {
					positions.Samples[adapterTestWireID(eid)] = []projectiles.Sample{{Time: 150, X: 4, Y: 5, Z: 6, FromSync: true}}
				}
				identity := countermeasureIdentities(metadata)[source]
				if (tc.name == "unknown" || tc.name == "mixed") && (!strings.HasPrefix(identity.Source, "UnknownAsChaff:") || identity.Confidence != "fallback") {
					t.Fatalf("unresolved ammunition did not retain the explicit Chaff fallback: %+v", identity)
				}
				inputs, _, _ := decodedCountermeasures(metadata, positions)
				if len(inputs) != 1 || inputs[0].identity != identity {
					t.Fatalf("adapter lost identity: %+v, want %+v", inputs, identity)
				}
				export := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
				if export.Constructions != 1 || export.TracksAdded != 1 || export.EventsAdded != 1 || len(export.Tracks) != 2 {
					t.Fatalf("export counts changed: %+v", export)
				}
				decoy := export.Tracks[1]
				if decoy.countermeasure != identity || decoy.key.id != eid || decoy.parent != aircraft || decoy.estimated == realSync || decoy.end != 500 {
					t.Fatalf("identity, lifetime, parent, or trajectory changed: %+v", decoy)
				}
				name, event, typ := tc.kind, tc.kind+" released", countermeasureACMIType
				if tc.kind == "Unknown" {
					name, event = countermeasureDisplayName, countermeasureEventText
				} else {
					typ += "+" + tc.kind
				}
				if !realSync {
					name += " (estimated)"
				}
				data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, export.Tracks, slot, nil, export.Events)
				frames := parseTestACMI(data)
				create := frameFindingLine(frames, 0.100, "2,")
				for _, property := range []string{
					"Name=" + name, "CallSign=" + name, "Type=" + typ,
					"Parent=1", "Coalition=Blue", "WT_CountermeasureType=" + tc.kind,
					"WT_CountermeasureTypeSource=" + identity.Source,
					"WT_CountermeasureTypeConfidence=" + identity.Confidence,
				} {
					if !strings.Contains(","+create+",", ","+property+",") {
						t.Fatalf("missing %q in %q", property, create)
					}
				}
				if strings.Contains(create, "WT_Estimated=1") == realSync {
					t.Fatalf("trajectory estimation marker does not match sync evidence: %q", create)
				}
				if got := frameFindingLine(frames, 0.100, "0,Event=Message|1|"); got != "0,Event=Message|1|"+event {
					t.Fatalf("release event = %q, want %q", got, event)
				}
				if frameFindingLine(frames, 0.500, "-2") != "-2" {
					t.Fatal("decoy lifetime end changed")
				}

				path := filepath.Join(t.TempDir(), "countermeasures.csv")
				if err := writeCountermeasureLog(path, export.Records); err != nil {
					t.Fatal(err)
				}
				csvData, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := csv.NewReader(strings.NewReader(string(csvData))).ReadAll()
				if err != nil || len(rows) != 2 || len(rows[0]) != 22 || len(rows[1]) != 22 {
					t.Fatalf("CSV shape: %v, %v", rows, err)
				}
				wantHeader := []string{"eid", "eid_hex", "lifetime_id", "spawn_ms", "end_ms", "has_end", "owner_eid", "parent_uid", "has_parent", "weapon_ref", "weapon_ref_hex", "template", "spawn_x", "spawn_y", "spawn_z", "sync_samples", "display"}
				if !reflect.DeepEqual(rows[0][:17], wantHeader) || !reflect.DeepEqual(rows[0][17:], []string{"kind", "type_source", "type_confidence", "launcher", "bullet"}) {
					t.Fatalf("CSV column order changed: %v", rows[0])
				}
				syncs, display := "0", "event"
				if realSync {
					syncs, display = "1", "track+event"
				}
				wantOriginal := []string{fmt.Sprint(eid), "400902", fmt.Sprint(eid), "100", "500", "true", "10", "77", "true", "917504", "E0000", "rocket", "1.25", "2.5", "3.75", syncs, display}
				if !reflect.DeepEqual(rows[1][:17], wantOriginal) {
					t.Fatalf("original CSV values changed: %v", rows[1][:17])
				}
				wantIdentity := []string{tc.kind, identity.Source, identity.Confidence, identity.Launcher, identity.Bullet}
				if !reflect.DeepEqual(rows[1][17:], wantIdentity) {
					t.Fatalf("CSV identity = %v, want %v", rows[1][17:], wantIdentity)
				}
			})
		}
	}
}

func TestCountermeasurePresentationConfidenceAndOverrides(t *testing.T) {
	for _, confidence := range []string{"confirmed", "estimated", "fallback", ""} {
		for _, estimated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/trajectory-estimated=%t", confidence, estimated), func(t *testing.T) {
				tr := &track{
					key: trackKey{decoy: true, id: 9}, estimated: estimated,
					samples:        []acmiSample{{t: 100}},
					countermeasure: countermeasureIdentity{Kind: "Chaff", Source: "Evidence,raw", Confidence: confidence},
				}
				name := "Chaff"
				if estimated {
					name += " (estimated)"
				}
				if got := trackName(tr, nil, nil); got != name {
					t.Fatalf("name = %q, want %q", got, name)
				}
				if got := trackName(tr, nil, map[string]string{"countermeasure:9": "Custom decoy"}); got != "Custom decoy" {
					t.Fatalf("manual name override lost: %q", got)
				}
				props := strings.Join(acmiTrackProperties(tr, nil, nil, nil), ",") + ","
				if !strings.Contains(props, "WT_CountermeasureTypeConfidence="+confidence+",") || !strings.Contains(props, "WT_CountermeasureTypeSource=Evidence\\,raw,") {
					t.Fatalf("identity confidence/source altered: %q", props)
				}
			})
		}
	}
	if got := countermeasureIdentityProperties(&track{key: trackKey{air: true}}); len(got) != 0 {
		t.Fatalf("non-decoy received countermeasure properties: %v", got)
	}
	if countermeasureType(nil) != countermeasureACMIType {
		t.Fatal("missing track guessed a subtype")
	}
	for _, estimated := range []bool{false, true} {
		tr := &track{key: trackKey{decoy: true}, estimated: estimated}
		name := "Chaff"
		if estimated {
			name = "Chaff (estimated)"
		}
		if trackName(tr, nil, nil) != name || countermeasureType(tr) != countermeasureACMIType+"+Chaff" {
			t.Fatal("zero-context identity did not use Chaff fallback")
		}
	}
}

func TestCountermeasurePresentationEmptyRecordKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "countermeasures.csv")
	if err := writeCountermeasureLog(path, []countermeasureRecord{{Display: "event"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 2 || len(rows[1]) != 22 {
		t.Fatalf("CSV shape: %v, %v", rows, err)
	}
	if !reflect.DeepEqual(rows[1][17:], []string{"Chaff", "UnknownAsChaff:NoAmmunitionEvidence", "fallback", "", ""}) {
		t.Fatalf("empty identity invented evidence: %v", rows[1][17:])
	}
}
