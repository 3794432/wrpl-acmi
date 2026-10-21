package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func TestBombMetadataDoesNotEnterRocketClassifiers(t *testing.T) {
	for _, trigger := range []uint32{7, 8, countermeasureWeaponTrigger} {
		source := &projectiles.Projectile{Kind: bombProjectileKind, WeaponRef: trigger << 16}
		if guidedProjectile(source) || isCountermeasureProjectile(source) {
			t.Fatalf("bomb with trigger %d entered a rocket classifier", trigger)
		}
	}
}

func TestDecodedBombTracksWithoutMetadataKeepsCompleteWireIDsAndRealPoints(t *testing.T) {
	const (
		firstWire  = uint64(0x0024c401)
		secondWire = uint64(0x00274401)
	)
	positions := &projectiles.PositionParser{BombSamples: map[uint64][]projectiles.Sample{
		firstWire: {
			{Time: 30, X: 3, Y: 4, Z: 5},
			{Time: 10, X: 1, Y: 2, Z: 3},
			{Time: 20, X: 9, Y: 9, Z: 9},
			{Time: 20, X: 2, Y: 3, Z: 4},
		},
		secondWire: {{Time: 50, X: 7, Y: 8, Z: 9}},
	}}

	tracks := decodedBombTracks(nil, positions)
	if len(tracks) != 2 {
		t.Fatalf("decoded bombs=%d, want 2", len(tracks))
	}
	if tracks[0].ID != firstWire || tracks[1].ID != secondWire {
		t.Fatalf("bomb IDs=%#x/%#x, want exact wire IDs %#x/%#x", tracks[0].ID, tracks[1].ID, firstWire, secondWire)
	}
	if got := adapterTestTimes(tracks[0].Samples); !adapterTestEqualTimes(got, []uint32{10, 20, 30}) {
		t.Fatalf("bomb times=%v, want sorted real points [10 20 30]", got)
	}
	if got := tracks[0].Samples[1]; got.x != 2 || got.y != 3 || got.z != 4 {
		t.Fatalf("same-tick point=%+v, want last recorded update", got)
	}
	if tracks[0].HasParent || tracks[0].Source != nil {
		fatalBombMetadataWasInvented(t, tracks[0])
	}
}

func TestDecodedBombTracksUsesBombMetadataLifetimesAndFullEID(t *testing.T) {
	const (
		fullEID = uint64(0x4007e3)
		owner   = uint64(77)
	)
	first := &projectiles.Projectile{
		ID: fullEID, Kind: bombProjectileKind, Name: bombDisplayName,
		ParentUID: owner, HasParent: true,
		SpawnSample: projectiles.Sample{Time: 100, X: 1, Y: 2, Z: 3},
	}
	reusedByRocket := &projectiles.Projectile{
		ID: fullEID, Name: "Missile", SpawnSample: projectiles.Sample{Time: 250},
	}
	second := &projectiles.Projectile{
		ID: fullEID, Kind: bombProjectileKind, Name: bombDisplayName,
		SpawnSample: projectiles.Sample{Time: 300, X: 4, Y: 5, Z: 6},
		EndTime:     400, HasEnd: true,
	}
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		fullEID: {first, reusedByRocket, second},
	}}
	positions := &projectiles.PositionParser{BombSamples: map[uint64][]projectiles.Sample{
		adapterTestWireID(fullEID): {
			{Time: 90, X: -1, Y: -1, Z: -1},
			{Time: 100, X: 10, Y: 11, Z: 12},
			{Time: 150, X: 20, Y: 21, Z: 22},
			{Time: 249, X: -2, Y: -2, Z: -2},
			{Time: 250, X: -4, Y: -4, Z: -4},
			{Time: 300, X: 30, Y: 31, Z: 32},
			{Time: 350, X: 40, Y: 41, Z: 42},
			{Time: 400, X: 50, Y: 51, Z: 52},
			{Time: 401, X: -3, Y: -3, Z: -3},
		},
	}}

	tracks := decodedBombTracks(metadata, positions)
	if len(tracks) != 2 {
		t.Fatalf("decoded reused bombs=%d, want 2 lifetimes", len(tracks))
	}
	wantIDs := map[uint64]bool{
		uint64(100)<<32 | fullEID: true,
		uint64(300)<<32 | fullEID: true,
	}
	for _, tr := range tracks {
		if !wantIDs[tr.ID] {
			t.Fatalf("lifetime ID=%#x is not based on the full EID %#x", tr.ID, fullEID)
		}
		delete(wantIDs, tr.ID)
	}
	if len(wantIDs) != 0 {
		t.Fatalf("missing bomb lifetime IDs: %v", wantIDs)
	}
	if got := adapterTestTimes(tracks[0].Samples); !adapterTestEqualTimes(got, []uint32{100, 150, 249}) {
		t.Fatalf("first bomb times=%v, want real points before reused construction [100 150 249]", got)
	}
	if got := adapterTestTimes(tracks[1].Samples); !adapterTestEqualTimes(got, []uint32{300, 350, 400}) {
		t.Fatalf("second bomb times=%v, want bounded real points [300 350 400]", got)
	}
	if !tracks[0].HasParent || tracks[0].ParentUID != owner {
		t.Fatalf("explicit owner was lost: %+v", tracks[0])
	}
	if tracks[1].HasParent {
		t.Fatal("second lifetime invented a parent")
	}
}

func TestDecodedBombTracksFallsBackToWireIDForNonBombOrIncompleteMetadata(t *testing.T) {
	const rawWire = uint64(0x0024c401)
	fullEID, ok := projectileEntityID(rawWire)
	if !ok {
		t.Fatal("test wire did not decode")
	}
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		fullEID: {{ID: fullEID, Kind: "", Name: bombDisplayName, SpawnSample: projectiles.Sample{Time: 1}}},
	}}
	positions := &projectiles.PositionParser{BombSamples: map[uint64][]projectiles.Sample{
		rawWire: {{Time: 20, X: 2, Y: 3, Z: 4}},
	}}

	tracks := decodedBombTracks(metadata, positions)
	if len(tracks) != 1 || tracks[0].ID != rawWire {
		t.Fatalf("fallback bombs=%+v, want exact wire ID %#x", tracks, rawWire)
	}
	if tracks[0].Source != nil {
		t.Fatal("non-bomb construction was attached as bomb metadata")
	}
}

func TestBombEIDAndWireIdentityCanShareNumericIDWithoutBeingDropped(t *testing.T) {
	const fullEID = uint64(0x4007e3)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		fullEID: {{
			ID: fullEID, Kind: bombProjectileKind, Name: bombDisplayName,
			ParentUID: 77, HasParent: true, EndTime: 200, HasEnd: true,
			SpawnSample: projectiles.Sample{Time: 100},
		}},
	}}
	positions := &projectiles.PositionParser{BombSamples: map[uint64][]projectiles.Sample{
		adapterTestWireID(fullEID): {{Time: 150, X: 1, Y: 2, Z: 3}},
		fullEID:                    {{Time: 160, X: 4, Y: 5, Z: 6}},
	}}

	inputs := decodedBombTracks(metadata, positions)
	if len(inputs) != 2 {
		t.Fatalf("decoded numeric-ID collision inputs=%d, want separate EID and wire tracks", len(inputs))
	}
	tracks := appendBombTracks(nil, inputs)
	if len(tracks) != 2 {
		t.Fatalf("appended numeric-ID collision tracks=%d, want 2", len(tracks))
	}
	seen := map[bool]bool{}
	for _, tr := range tracks {
		if tr.key.id != fullEID || !tr.key.bomb {
			t.Fatalf("collision track key=%+v, want the same numeric ID in bomb namespaces", tr.key)
		}
		seen[tr.key.bombWire] = true
	}
	if !seen[false] || !seen[true] {
		t.Fatalf("numeric-ID collision namespaces=%v, want one EID and one wire track", seen)
	}
}

func TestBombSamplesOutsideMetadataLifetimeAreNotAttached(t *testing.T) {
	const fullEID = uint64(0x4007e4)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		fullEID: {{
			ID: fullEID, Kind: bombProjectileKind, Name: bombDisplayName,
			ParentUID: 77, HasParent: true, EndTime: 200, HasEnd: true,
			SpawnSample: projectiles.Sample{Time: 100},
		}},
	}}
	positions := &projectiles.PositionParser{BombSamples: map[uint64][]projectiles.Sample{
		adapterTestWireID(fullEID): {
			{Time: 50, X: 1}, {Time: 90, X: 2}, {Time: 250, X: 3},
		},
	}}
	tracks := decodedBombTracks(metadata, positions)
	if len(tracks) != 1 || !tracks[0].PositionOnly || len(tracks[0].Samples) != 1 || tracks[0].Samples[0].t != 100 || tracks[0].Samples[0].x != 0 {
		t.Fatalf("out-of-lifetime sync points replaced the construction marker: %+v", tracks)
	}
}

func TestBombConstructionWithoutSyncRemainsExplicitPositionMarker(t *testing.T) {
	for _, knownEnd := range []bool{false, true} {
		t.Run(fmt.Sprint(knownEnd), func(t *testing.T) {
			aircraft, slot := newTestTrack(true, 77, "F-16C", "Alpha", 1)
			aircraft.samples = []acmiSample{{t: 0}, {t: 3000}}
			source := &projectiles.Projectile{
				ID: 42, Kind: bombProjectileKind, ParentUID: 77, HasParent: true,
				SpawnSample: projectiles.Sample{Time: 1000, X: 1, Y: 2000, Z: 3},
				HasEnd:      knownEnd, EndTime: 2000,
			}
			metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{42: {source}}}
			tracks := appendBombTracks([]*track{aircraft}, decodedBombTracks(metadata, nil))
			if len(tracks) != 2 || len(tracks[1].samples) != 1 || !tracks[1].bombPositionOnly || tracks[1].parent != aircraft {
				t.Fatalf("construction marker is missing or invented a path: %+v", tracks)
			}
			wantEnd := uint32(3000)
			if knownEnd {
				wantEnd = 2000
			}
			if end, ok := bombTrackEnd(tracks[1]); !ok || end != wantEnd {
				t.Fatalf("marker end=%d/%v, want %d", end, ok, wantEnd)
			}
			data := renderTestACMI(t, tracks, slot, nil)
			for _, property := range []string{"Type=Weapon+Bomb", "WT_BombTrajectoryAvailable=0", "Label=Release position only", "Parent=1"} {
				if !strings.Contains(data, property) {
					t.Fatalf("marker lacks %q", property)
				}
			}
			frames := parseTestACMI(data)
			if countObjectLines(frames, "2,") != 1 {
				t.Fatal("construction marker emitted a synthetic flight path")
			}
		})
	}
}

func TestAppendBombTracksFollowsExistingKindsAndRequiresExplicitParent(t *testing.T) {
	aircraft, _ := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	decoy := &track{key: trackKey{decoy: true, id: 9}, samples: []acmiSample{{t: 100}}}
	inputs := []bombTrack{
		{
			ID: 1, ParentUID: 77, HasParent: true,
			Samples: []acmiSample{{t: 100, x: 1, y: 2, z: 3}},
		},
		{ID: 2, ParentUID: 77, Samples: []acmiSample{{t: 110, x: 4, y: 5, z: 6}}},
		{ID: 3, HasParent: true, ParentUID: 999, Samples: []acmiSample{{t: 120, x: 7, y: 8, z: 9}}},
	}

	tracks := appendBombTracks([]*track{aircraft, decoy}, inputs)
	if len(tracks) != 5 || tracks[0] != aircraft || tracks[1] != decoy {
		t.Fatalf("existing tracks moved while appending bombs: %+v", tracks)
	}
	if tracks[2].parent != aircraft {
		t.Fatal("explicit owner UID did not resolve to its aircraft")
	}
	if tracks[3].parent != nil {
		t.Fatal("ParentUID without HasParent was treated as an owner")
	}
	if tracks[4].parent != nil {
		t.Fatal("missing aircraft was guessed as a parent")
	}
	for _, tr := range tracks[2:] {
		if !tr.key.bomb || tr.key.missile || tr.key.decoy || tr.key.air {
			t.Fatalf("bomb received a non-bomb track key: %+v", tr.key)
		}
	}
}

func TestAppendingBombsDoesNotRenumberExistingACMIObjects(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	aircraft.samples[0].t = 100
	missile := &track{
		key: trackKey{missile: true, id: 42}, name: "R-73M",
		samples: []acmiSample{{t: 200, x: 1}}, parent: aircraft,
	}
	decoy := &track{
		key:     trackKey{decoy: true, id: 9},
		samples: []acmiSample{{t: 150, x: 2}}, parent: aircraft,
	}
	before := renderTestACMI(t, []*track{aircraft, missile, decoy}, slot, nil)
	after := renderTestACMI(t, appendBombTracks([]*track{aircraft, missile, decoy}, []bombTrack{{
		ID: 3, Samples: []acmiSample{{t: 300, x: 3}},
	}}), slot, nil)
	beforeIDs := declaredACMIIDs(before)
	afterIDs := declaredACMIIDs(after)
	if len(beforeIDs) != 3 || len(afterIDs) != 4 {
		t.Fatalf("declared IDs before=%v after=%v", beforeIDs, afterIDs)
	}
	for i := range beforeIDs {
		if beforeIDs[i] != afterIDs[i] {
			t.Fatalf("existing object %d renumbered from %s to %s", i+1, beforeIDs[i], afterIDs[i])
		}
	}
	if afterIDs[3] != "4" {
		t.Fatalf("bomb ID=%s, want the next ID after existing objects", afterIDs[3])
	}
}

func TestACMIReferenceIgnoresBombBeforeLegacyAndUsesBombOnlyFallback(t *testing.T) {
	aircraft := &track{key: trackKey{air: true, id: 1}, samples: []acmiSample{{t: 100}}}
	bomb := &track{key: trackKey{bomb: true, id: 2}, samples: []acmiSample{{t: 50}, {t: 80}}}
	if got := acmiReferenceBase([]*track{aircraft, bomb}, nil); got != 100 {
		t.Fatalf("reference base=%d, want legacy aircraft time 100", got)
	}
	if got := acmiReferenceBase([]*track{bomb}, nil); got != 50 {
		t.Fatalf("bomb-only reference base=%d, want first real point 50", got)
	}
	decoy := &track{key: trackKey{decoy: true, id: 3}, samples: []acmiSample{{t: 60}, {t: 90}}}
	if got := acmiReferenceBase([]*track{decoy, bomb}, nil); got != 60 {
		t.Fatalf("decoy fallback changed to %d by an earlier bomb", got)
	}
}

func TestACMIWritesBombDynamicsWithExplicitCreateAndDelete(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	aircraft.samples[0].t = 100
	tracks := appendBombTracks([]*track{aircraft}, []bombTrack{{
		ID: 42, ParentUID: 77, HasParent: true,
		Samples: []acmiSample{
			{t: 75, x: 10, y: 20, z: 30, yaw: 40},
			{t: 50, x: 1, y: 2, z: 3, yaw: 10},
		},
	}})

	data := renderTestACMI(t, tracks, slot, nil)
	frames := parseTestACMI(data)
	beforeFrames := data[:strings.Index(data, "\n#")+1]
	if testHasLinePrefix(beforeFrames, "2,") {
		t.Fatal("bomb was declared as a static object before its first real point")
	}
	create := frameFindingLine(frames, -0.050, "2,")
	wantPrefix := "2,Name=Bomb,CallSign=Bomb,Parent=1,Type=Weapon+Bomb,Coalition=Blue,Color=Blue,"
	if !strings.HasPrefix(create, wantPrefix) {
		t.Fatalf("bomb creation line=%q, want prefix %q", create, wantPrefix)
	}
	if got := bombPositionComponentCount(create); got != 3 {
		t.Fatalf("bomb T has %d components, want only lon/lat/alt", got)
	}
	if got := frameFindingLine(frames, -0.025, "-2"); got != "-2" {
		t.Fatalf("bomb deletion=%q, want -2 at final real point", got)
	}
	if countObjectLines(frames, "2,") != 2 {
		t.Fatalf("bomb emitted synthetic points; object lines=%d", countObjectLines(frames, "2,"))
	}
	for _, forbidden := range []string{"WT_Guidance", "WT_MissileModel", "Label=INS"} {
		if strings.Contains(data, forbidden) {
			t.Fatalf("bomb entered missile-only export logic: found %q", forbidden)
		}
	}

	logPath := filepath.Join(t.TempDir(), "flight.csv")
	if err := writeMissileFlightLog(logPath, tracks); err != nil {
		t.Fatalf("writing missile flight log: %v", err)
	}
	rows := readBombTestCSV(t, logPath)
	if len(rows) != 1 {
		t.Fatalf("missile flight log contains %d bomb rows, want header only", len(rows)-1)
	}
}

func TestACMIOnePointBombCreatesAndDeletesAtSameTime(t *testing.T) {
	tracks := appendBombTracks(nil, []bombTrack{{
		ID: 7, Samples: []acmiSample{{t: 1000, x: 1, y: 2, z: 3}},
	}})
	frames := renderAndParseACMI(t, tracks, nil, nil)
	if len(frames) != 1 || frames[0].label != "0.000" {
		t.Fatalf("one-point bomb frames=%+v, want one zero-time frame", frames)
	}
	create := frameFindingLine(frames, 0, "1,")
	if create == "" || frameFindingLine(frames, 0, "-1") != "-1" {
		t.Fatalf("one-point bomb did not create and delete at the same time: %+v", frames[0].lines)
	}
	if got := bombPositionComponentCount(create); got != 3 {
		t.Fatalf("one-point bomb T has %d components, want lon/lat/alt only", got)
	}
}

func TestBombTrackListAndManualNameUseBombIdentity(t *testing.T) {
	bomb := &track{
		key:     trackKey{bomb: true, id: 42},
		samples: []acmiSample{{t: 100, x: 1}, {t: 200, x: 2}},
	}
	path := filepath.Join(t.TempDir(), "tracks.csv")
	if err := writeTrackList(path, []*track{bomb}, nil, map[string]string{"bomb:42": "GBU Test"}); err != nil {
		t.Fatalf("writing track list: %v", err)
	}
	rows := readBombTestCSV(t, path)
	if len(rows) != 2 {
		t.Fatalf("track list rows=%d, want header plus bomb", len(rows))
	}
	if got := rows[1][1]; got != "bomb" {
		t.Fatalf("CSV kind=%q, want bomb", got)
	}
	if got := rows[1][4]; got != "GBU Test" {
		t.Fatalf("custom bomb name=%q, want GBU Test", got)
	}
	data := renderTestACMI(t, []*track{bomb}, nil, map[string]string{"bomb:42": "GBU Test"})
	if !strings.Contains(data, "Name=GBU Test,CallSign=GBU Test") || !strings.Contains(data, "Type=Weapon+Bomb") {
		t.Fatalf("custom bomb ACMI name/type missing:\n%s", data)
	}
}

func fatalBombMetadataWasInvented(t *testing.T, tr bombTrack) {
	t.Helper()
	t.Fatalf("metadata-free bomb invented provenance: %+v", tr)
}

func readBombTestCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("reading CSV: %v", err)
	}
	return rows
}

func bombPositionComponentCount(line string) int {
	_, position, ok := strings.Cut(line, "T=")
	if !ok {
		return 0
	}
	return len(strings.Split(position, "|"))
}
