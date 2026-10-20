package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

func TestCountermeasureRecycledEIDKeepsLifetimesAndRealSync(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	const eid = uint64(0x400900)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {
			{
				ID: eid, OwnerEID: 10, ParentUID: 77, HasParent: true,
				WeaponRef: 14<<16 | 0x21, Template: "rocket",
				SpawnSample: projectiles.Sample{Time: 100, X: 1, Y: 2, Z: 3},
				EndTime:     200, HasEnd: true,
			},
			{
				ID: eid, OwnerEID: 11, ParentUID: 77, HasParent: true,
				WeaponRef: 14<<16 | 0x22, Template: "rocket",
				SpawnSample: projectiles.Sample{Time: 300, X: 4, Y: 5, Z: 6},
				EndTime:     400, HasEnd: true,
			},
		},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(eid): {
			{Time: 150, X: 10, Y: 20, Z: 30, FromSync: true},
			{Time: 250, X: 40, Y: 50, Z: 60, FromSync: true},
			{Time: 350, X: 70, Y: 80, Z: 90, FromSync: true},
			{Time: 401, X: 100, Y: 110, Z: 120, FromSync: true},
		},
	}}

	export := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
	if len(export.Tracks) != 3 {
		t.Fatalf("tracks = %d, want aircraft plus two decoy lifetimes", len(export.Tracks))
	}
	if export.TracksAdded != 2 || export.EventsAdded != 2 || export.Constructions != 2 {
		t.Fatalf("counts tracks=%d events=%d records=%d", export.TracksAdded, export.EventsAdded, export.Constructions)
	}
	wantIDs := map[uint64]bool{
		uint64(100)<<32 | eid: true,
		uint64(300)<<32 | eid: true,
	}
	for _, tr := range export.Tracks[1:] {
		if !tr.key.decoy || tr.key.missile {
			t.Fatalf("decoy key = %+v, want decoy and not missile", tr.key)
		}
		if tr.estimated {
			t.Fatal("a construction with real sync samples must never be estimated")
		}
		if !wantIDs[tr.key.id] {
			t.Fatalf("unexpected lifetime ID %#x", tr.key.id)
		}
		delete(wantIDs, tr.key.id)
		if tr.parent != aircraft {
			t.Fatal("decoy did not inherit its actual aircraft parent")
		}
		if len(tr.samples) != 2 {
			t.Fatalf("lifetime %#x samples = %v, want spawn plus one sync", tr.key.id, tr.samples)
		}
	}
	first, second := export.Tracks[1], export.Tracks[2]
	if first.samples[0].t != 100 || first.samples[1].t != 150 {
		t.Fatalf("first lifetime times = %v, want only 100 and 150", adapterTestTimes(first.samples))
	}
	if second.samples[0].t != 300 || second.samples[1].t != 350 {
		t.Fatalf("second lifetime times = %v, want only 300 and 350", adapterTestTimes(second.samples))
	}
	if first.samples[0].x != 1 || first.samples[1].x != 10 {
		t.Fatalf("spawn/first sync coordinates changed: %+v", first.samples)
	}
	for _, record := range export.Records {
		if record.SyncCount != 1 || record.Display != "track+event" {
			t.Fatalf("record sync/display = %d/%q, want 1/track+event", record.SyncCount, record.Display)
		}
	}

	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, export.Tracks, slot, nil, export.Events)
	frames := parseTestACMI(data)
	if !strings.Contains(data, "Type="+countermeasureACMIType) {
		t.Fatalf("decoy type missing from ACMI:\n%s", data)
	}
	create := frameFindingLine(frames, 0.100, "2,Name=Countermeasure")
	if !strings.Contains(create, "Parent=1") || !strings.Contains(create, "Coalition=Blue") || !strings.Contains(create, "Color=Blue") {
		t.Fatalf("decoy creation/link = %q\n%s", create, data)
	}
	if got := frameFindingLine(frames, 0.100, "0,Event=Message|1|"); got != "0,Event=Message|1|"+countermeasureEventText {
		t.Fatalf("release event = %q", got)
	}
	if countObjectLines(frames, "2,") != 2 || countObjectLines(frames, "3,") != 2 {
		t.Fatal("decoy tracks contained samples outside their lifetimes")
	}
	if frameFindingLine(frames, 0.200, "-2") != "-2" || frameFindingLine(frames, 0.400, "-3") != "-3" {
		t.Fatal("decoy explicit destruction times were not used")
	}
}

func TestCountermeasureWithoutEndStopsAtNextConstructionOfAnyType(t *testing.T) {
	aircraft, _ := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	const eid = uint64(0x400908)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {
			{
				ID: eid, OwnerEID: 10, ParentUID: 77, HasParent: true,
				WeaponRef: 14 << 16, Template: "rocket",
				SpawnSample: projectiles.Sample{Time: 100, X: 1, Y: 2, Z: 3},
				// No ECS destruction record.
			},
			{
				ID: eid, OwnerEID: 11, ParentUID: 77, HasParent: true,
				WeaponRef: 8 << 16, Template: "rocket+arh_shell",
				SpawnSample: projectiles.Sample{Time: 300, X: 4, Y: 5, Z: 6},
				EndTime:     400, HasEnd: true,
			},
		},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(eid): {
			{Time: 150, X: 10, Y: 20, Z: 30, FromSync: true},
			{Time: 350, X: 40, Y: 50, Z: 60, FromSync: true},
		},
	}}

	export := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
	if export.Constructions != 1 || export.TracksAdded != 1 || export.EventsAdded != 1 {
		t.Fatalf("counts constructions=%d tracks=%d events=%d", export.Constructions, export.TracksAdded, export.EventsAdded)
	}
	if export.Tracks[1].key.id != eid {
		t.Fatalf("single CM lifetime ID = %#x, want source EID %#x", export.Tracks[1].key.id, eid)
	}
	if got := adapterTestTimes(export.Tracks[1].samples); !adapterTestEqualTimes(got, []uint32{100, 150}) {
		t.Fatalf("CM times = %v, want only its own spawn and sync", got)
	}
	if export.Records[0].SyncCount != 1 {
		t.Fatalf("CM sync count = %d, want only in-window sync", export.Records[0].SyncCount)
	}

	missiles := decodedMissileTracks(metadata, positions)
	if len(missiles) != 1 {
		t.Fatalf("missile lifetimes = %d, want the replacement missile", len(missiles))
	}
	if got := adapterTestTimes(missiles[0].Samples); !adapterTestEqualTimes(got, []uint32{300, 350}) {
		t.Fatalf("replacement missile times = %v, want 300 and 350", got)
	}
}

func TestCountermeasureOutsideLifetimeSamplesDoNotCreateTrack(t *testing.T) {
	aircraft, _ := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	const eid = uint64(0x400909)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 1000, X: 1, Y: 2, Z: 3},
			// No explicit end record.
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(eid): {
			{Time: 100, X: 10, Y: 20, Z: 30, FromSync: true},
			{Time: 200, X: 40, Y: 50, Z: 60, FromSync: true},
		},
	}}
	export := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
	if export.TracksAdded != 0 || len(export.Tracks) != 1 {
		t.Fatalf("out-of-lifetime sync created %d decoy tracks", export.TracksAdded)
	}
	if export.EventsAdded != 1 || export.Constructions != 1 || export.Records[0].SyncCount != 0 {
		t.Fatalf("out-of-lifetime counts events=%d constructions=%d record=%+v",
			export.EventsAdded, export.Constructions, export.Records)
	}
	if export.Records[0].Display != "event" {
		t.Fatalf("out-of-lifetime display = %q, want event only", export.Records[0].Display)
	}
}

func TestCountermeasureSpawnOnlyGetsEstimatedTrajectory(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	const eid = uint64(0x400901)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, OwnerEID: 10, ParentUID: 77, HasParent: true,
			WeaponRef: 14 << 16, Template: "rocket",
			SpawnSample: projectiles.Sample{Time: 300, X: 1, Y: 2, Z: 3},
			EndTime:     900, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{}}

	export := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
	if len(export.Tracks) != 2 || export.TracksAdded != 1 {
		t.Fatalf("spawn-only CM estimate: tracks=%d added=%d", len(export.Tracks), export.TracksAdded)
	}
	decoy := export.Tracks[1]
	if !decoy.estimated || !decoy.key.decoy || decoy.parent != aircraft {
		t.Fatalf("decoy estimate/parent = %v/%v", decoy.estimated, decoy.parent)
	}
	if got := adapterTestTimes(decoy.samples); !adapterTestEqualTimes(got, []uint32{300, 500, 700, 900}) {
		t.Fatalf("estimated times = %v, want the EndTime-capped 0.6 s path", got)
	}
	if decoy.estimatedVelocity {
		t.Fatal("aircraft has one sample, so velocity must fall back to zero")
	}
	if len(export.Events) != 1 || export.Events[0].text != countermeasureEstimatedEventText {
		t.Fatalf("spawn-only events = %+v", export.Events)
	}
	if len(export.Records) != 1 || export.Records[0].SyncCount != 0 || export.Records[0].Display != "event" {
		t.Fatalf("spawn-only record = %+v", export.Records)
	}

	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, export.Tracks, slot, nil, export.Events)
	frames := parseTestACMI(data)
	create := frameFindingLine(frames, 0.300, "2,")
	if !strings.Contains(create, "Name="+countermeasureEstimatedDisplayName) ||
		!strings.Contains(create, "WT_TrajectorySource="+countermeasureEstimatedSource) ||
		!strings.Contains(create, "WT_Estimated=1") ||
		!strings.Contains(create, "WT_VelocitySource="+countermeasureVelocityNone) ||
		!strings.Contains(create, "Parent=1") {
		t.Fatalf("estimated decoy properties = %q\n%s", create, data)
	}
	if countObjectLines(frames, "2,") != 4 {
		t.Fatalf("estimated object lines = %d, want one per 0.2 s sample", countObjectLines(frames, "2,"))
	}
	if frameFindingLine(frames, 0.900, "-2") != "-2" {
		t.Fatal("estimated decoy was not deleted at its EndTime")
	}
	if got := frameFindingLine(frames, 0.300, "0,Event=Message|1|"); got != "0,Event=Message|1|"+countermeasureEstimatedEventText {
		t.Fatalf("spawn-only event = %q", got)
	}
}

func TestCountermeasureEstimatedTrajectoryUsesParentVelocity(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	aircraft.samples = []acmiSample{
		{t: 1000, x: 0, y: 100, z: 0},
		{t: 2000, x: 100, y: 100, z: 0},
	}
	const eid = uint64(0x40090b)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 1500, X: 10, Y: 100, Z: 20},
			EndTime:     1700, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{}}

	export := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
	if export.TracksAdded != 1 {
		t.Fatalf("estimated tracks = %d, want 1", export.TracksAdded)
	}
	decoy := export.Tracks[1]
	if !decoy.estimated || !decoy.estimatedVelocity {
		t.Fatalf("estimate flags = %v/%v, want true/true", decoy.estimated, decoy.estimatedVelocity)
	}
	if got := adapterTestTimes(decoy.samples); !adapterTestEqualTimes(got, []uint32{1500, 1700}) {
		t.Fatalf("estimated times = %v, want spawn and EndTime", got)
	}
	wantX := 10 + 100*(1-math.Exp(-countermeasureEstimateDragK*0.2))/countermeasureEstimateDragK
	wantY := 100 - 0.5*countermeasureEstimateGravity*0.2*0.2
	if math.Abs(decoy.samples[0].x-10) > 1e-9 || math.Abs(decoy.samples[0].y-100) > 1e-9 {
		t.Fatalf("first estimated sample = %+v, want the release point", decoy.samples[0])
	}
	if math.Abs(decoy.samples[1].x-wantX) > 1e-6 || math.Abs(decoy.samples[1].y-wantY) > 1e-6 {
		t.Fatalf("second estimated sample = (%.6f,%.6f), want (%.6f,%.6f)",
			decoy.samples[1].x, decoy.samples[1].y, wantX, wantY)
	}
	if decoy.samples[1].x <= decoy.samples[0].x || decoy.samples[1].y >= decoy.samples[0].y {
		t.Fatalf("estimate did not follow +X aircraft velocity and gravity: %+v", decoy.samples)
	}

	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, export.Tracks, slot, nil, export.Events)
	create := frameFindingLine(parseTestACMI(data), 0.500, "2,")
	if !strings.Contains(create, "WT_VelocitySource="+countermeasureVelocityFromTrack) ||
		!strings.Contains(create, "WT_Estimated=1") || !strings.Contains(create, "Parent=1") {
		t.Fatalf("velocity provenance missing: %q", create)
	}
}

func TestCountermeasureEstimatedTrajectoryWithoutParentFallsStraightDown(t *testing.T) {
	const eid = uint64(0x40090c)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, ParentUID: 999, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 100, X: 5, Y: 200, Z: 7},
			EndTime:     400, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{}}
	export := prepareCountermeasures(metadata, positions, nil, true)
	if export.TracksAdded != 1 {
		t.Fatalf("orphan estimates = %d, want 1", export.TracksAdded)
	}
	decoy := export.Tracks[0]
	if decoy.parent != nil || decoy.estimatedVelocity {
		t.Fatalf("orphan parent/velocity = %v/%v, want nil/false", decoy.parent, decoy.estimatedVelocity)
	}
	for _, sample := range decoy.samples {
		if sample.x != 5 || sample.z != 7 || sample.y > 200 {
			t.Fatalf("orphan estimate moved horizontally or climbed: %+v", decoy.samples)
		}
	}
	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, export.Tracks, nil, nil, export.Events)
	create := frameFindingLine(parseTestACMI(data), 0.000, "1,")
	if strings.Contains(create, "Parent=") || !strings.Contains(create, "WT_VelocitySource="+countermeasureVelocityNone) ||
		!strings.Contains(create, "Coalition=Neutral,Color=White") {
		t.Fatalf("orphan estimated properties = %q", create)
	}
}

func TestCountermeasureEstimatedTrajectoryWithoutEndStopsAtReplayEnd(t *testing.T) {
	aircraft, _ := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	aircraft.samples = []acmiSample{
		{t: 0, x: 0, y: 100, z: 0},
		{t: 1200, x: 60, y: 100, z: 0},
	}
	const eid = uint64(0x40090d)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 300, X: 0, Y: 100, Z: 0},
			// No EndTime: the replay's last real sample bounds the lifetime.
		}},
	}}
	export := prepareCountermeasures(metadata, &projectiles.PositionParser{}, []*track{aircraft}, true)
	if export.TracksAdded != 1 {
		t.Fatalf("estimated tracks = %d, want 1", export.TracksAdded)
	}
	decoy := export.Tracks[1]
	if got := decoy.samples[len(decoy.samples)-1].t; got != 1200 {
		t.Fatalf("last estimated time = %d, want replay end 1200", got)
	}
	if decoy.end != 1200 || !decoy.hasEnd {
		t.Fatalf("estimated end = %d/%v, want explicit replay end", decoy.end, decoy.hasEnd)
	}
}

func TestCountermeasureEstimateIsDeterministic(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	aircraft.samples = []acmiSample{
		{t: 900, x: 0, y: 100, z: 0},
		{t: 2100, x: 120, y: 100, z: -40},
	}
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		0x40090e: {{
			ID: 0x40090e, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 1000, X: 10, Y: 90, Z: 5},
			EndTime:     1600, HasEnd: true,
		}},
		0x40090f: {{
			ID: 0x40090f, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 1300, X: -5, Y: 95, Z: 8},
			EndTime:     1900, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{}}
	first := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
	second := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
	if !reflect.DeepEqual(adapterTestTimes(first.Tracks[1].samples), adapterTestTimes(second.Tracks[1].samples)) {
		t.Fatal("estimated track order or samples changed between runs")
	}
	header := wrpl.WRPLHeader{StartTime: 1700000000}
	a := renderACMIWithHeader(t, header, first.Tracks, slot, nil, first.Events)
	b := renderACMIWithHeader(t, header, second.Tracks, slot, nil, second.Events)
	if a != b {
		t.Fatal("two exports of the same input produced different ACMI")
	}
}

func TestCountermeasureReleaseVelocityGuardsSpeedAndNaN(t *testing.T) {
	parent := &track{key: trackKey{air: true, id: 1}, samples: []acmiSample{
		{t: 1000, x: 0, y: 0, z: 0},
		{t: 1001, x: 2_000_000, y: 0, z: 0},
	}}
	velocity, ok := countermeasureReleaseVelocity(parent, 1000)
	if !ok {
		t.Fatal("fast finite samples should yield a velocity")
	}
	if got := math.Hypot(velocity[0], math.Hypot(velocity[1], velocity[2])); math.Abs(got-countermeasureEstimateMaxSpeedMPS) > 1e-9 {
		t.Fatalf("speed = %v, want clamp to %v", got, countermeasureEstimateMaxSpeedMPS)
	}
	parent.samples[1].x = math.NaN()
	if velocity, ok := countermeasureReleaseVelocity(parent, 1000); ok || velocity != [3]float64{} {
		t.Fatalf("NaN samples must degrade to zero velocity, got %v/%v", velocity, ok)
	}
}

func TestCountermeasureReleaseVelocityAtExactTurnSampleUsesNearestNeighbor(t *testing.T) {
	parent := &track{key: trackKey{air: true, id: 1}, samples: []acmiSample{
		{t: 500, x: 0, y: 50, z: 0},
		{t: 900, x: 190, y: 50, z: 0},
		{t: 1000, x: 200, y: 50, z: 0},
		{t: 1100, x: 200, y: 50, z: 10},
		{t: 1500, x: 300, y: 50, z: 300},
	}}
	velocity, ok := countermeasureReleaseVelocity(parent, 1000)
	if !ok {
		t.Fatal("exact turn sample should yield a velocity")
	}
	// The equal-distance neighbors make this a tie. The prior sample gives
	// +100 m/s on X, while the window endpoints would give +300 m/s on X/Z.
	want := [3]float64{100, 0, 0}
	if math.Abs(velocity[0]-want[0]) > 1e-9 ||
		math.Abs(velocity[1]-want[1]) > 1e-9 ||
		math.Abs(velocity[2]-want[2]) > 1e-9 {
		t.Fatalf("exact-turn velocity = %v, want nearest prior neighbor %v", velocity, want)
	}
}

func TestCountermeasureEstimatedObjectFallsAndDisappears(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	aircraft.samples = []acmiSample{
		{t: 0, x: 100, y: 1000, z: 100},
		{t: 1000, x: 200, y: 1000, z: 100},
	}
	const eid = uint64(0x400910)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 500, X: 100, Y: 1000, Z: 100},
			EndTime:     900, HasEnd: true,
		}},
	}}
	export := prepareCountermeasures(metadata, &projectiles.PositionParser{}, []*track{aircraft}, true)
	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, export.Tracks, slot, nil, export.Events)
	frames := parseTestACMI(data)
	if got := countObjectLines(frames, "2,"); got != 3 {
		t.Fatalf("estimated object frames = %d, want 3 (0.0/0.2/0.4 s)", got)
	}
	if frameFindingLine(frames, 0.900, "-2") != "-2" {
		t.Fatal("estimated object was not deleted at its shortened lifetime")
	}
	if frameHasObjectPrefixAfter(frames, 0.900, "2,") {
		t.Fatal("estimated object hovered after its deletion")
	}
}

func TestCountermeasureParentTeamAndMissingParent(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	const linkedEID = uint64(0x400902)
	const orphanEID = uint64(0x400903)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		linkedEID: {{
			ID: linkedEID, OwnerEID: 10, ParentUID: 77, HasParent: true,
			WeaponRef: 14 << 16, Template: "rocket",
			SpawnSample: projectiles.Sample{Time: 100, X: 1, Y: 1, Z: 1},
			EndTime:     200, HasEnd: true,
		}},
		orphanEID: {{
			ID: orphanEID, OwnerEID: 99, ParentUID: 999, HasParent: true,
			WeaponRef: 14 << 16, Template: "rocket",
			SpawnSample: projectiles.Sample{Time: 300, X: 2, Y: 2, Z: 2},
			EndTime:     400, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(linkedEID): {{Time: 150, X: 9, Y: 9, Z: 9, FromSync: true}},
		adapterTestWireID(orphanEID): {{Time: 350, X: 8, Y: 8, Z: 8, FromSync: true}},
	}}
	export := prepareCountermeasures(metadata, positions, []*track{aircraft}, true)
	if len(export.Tracks) != 3 {
		t.Fatalf("tracks = %d, want linked and orphan decoys", len(export.Tracks))
	}
	linked, orphan := export.Tracks[1], export.Tracks[2]
	if linked.parent != aircraft || orphan.parent != nil {
		t.Fatalf("parent links linked=%v orphan=%v", linked.parent, orphan.parent)
	}
	names := map[string]string{fmt.Sprintf("countermeasure:%d", linkedEID): "Flares"}
	if got := trackName(linked, slot, names); got != "Flares" {
		t.Fatalf("countermeasure name override = %q", got)
	}
	if got := trackName(orphan, slot, names); got != countermeasureDisplayName {
		t.Fatalf("default countermeasure name = %q", got)
	}

	data := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, export.Tracks, slot, names, export.Events)
	frames := parseTestACMI(data)
	linkedLine := frameFindingLine(frames, 0.100, "2,")
	if !strings.Contains(linkedLine, "Name=Flares") || !strings.Contains(linkedLine, "Parent=1") ||
		!strings.Contains(linkedLine, "Coalition=Blue") || !strings.Contains(linkedLine, "Type="+countermeasureACMIType) {
		t.Fatalf("linked decoy properties = %q", linkedLine)
	}
	orphanLine := frameFindingLine(frames, 0.300, "3,")
	if strings.Contains(orphanLine, "Parent=") || !strings.Contains(orphanLine, "Coalition=Neutral,Color=White") {
		t.Fatalf("orphan decoy guessed a parent or coalition: %q", orphanLine)
	}
	if got := frameFindingLine(frames, 0.100, "0,Event=Message|1|"); got == "" {
		t.Fatal("linked decoy event was not associated with its parent")
	}
	if got := frameFindingLine(frames, 0.300, "0,Event=Message|"); strings.Contains(got, "|1|") || got == "" {
		t.Fatalf("orphan decoy event guessed a parent: %q", got)
	}
}

func TestCountermeasuresFalseIsByteIdenticalToLegacyWriter(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	missileTracks := appendMissileTracks([]*track{aircraft}, []missileTrack{{
		ID: 9, Name: "AIM-9M", ParentUID: 77, HasParent: true,
		Samples: []acmiSample{{t: 500, x: 10, y: 20, z: 30}},
	}})
	const eid = uint64(0x400904)
	const estimatedEID = uint64(0x400911)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 200, X: 1, Y: 2, Z: 3},
		}},
		estimatedEID: {{
			ID: estimatedEID, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 600, X: 7, Y: 8, Z: 9},
			EndTime:     900, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(eid): {{Time: 220, X: 4, Y: 5, Z: 6, FromSync: true}},
	}}
	export := prepareCountermeasures(metadata, positions, missileTracks, false)
	if export.TracksAdded != 0 || export.EventsAdded != 0 || len(export.Records) != 0 {
		t.Fatalf("disabled export added data: %+v", export)
	}
	if !reflect.DeepEqual(export.Tracks, missileTracks) {
		t.Fatal("disabled export changed the legacy track slice")
	}
	header := wrpl.WRPLHeader{StartTime: 1700000000}
	legacy := renderACMIWithHeader(t, header, missileTracks, slot, nil, nil)
	disabled := renderACMIWithHeader(t, header, export.Tracks, slot, nil, export.Events)
	if legacy != disabled {
		t.Fatal("countermeasures=false output differs from the legacy writer")
	}
	if !strings.Contains(disabled, "0,DataSource=War Thunder replay via wrpl-inspector\n") {
		t.Fatal("DataSource header changed")
	}
	if strings.Contains(disabled, "Event=Message") || strings.Contains(disabled, countermeasureACMIType) {
		t.Fatal("disabled output contains countermeasure additions")
	}
}

func TestCountermeasureEarlierThanLegacyKeepsReferenceBaseAndOldIDs(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	aircraft.samples = []acmiSample{{t: 1600, x: 1, y: 2, z: 3}}
	tracks := appendMissileTracks([]*track{aircraft}, []missileTrack{{
		ID: 9, Name: "AIM-9M", ParentUID: 77, HasParent: true,
		Samples: []acmiSample{{t: 1500, x: 10, y: 20, z: 30}},
	}})
	const eid = uint64(0x400905)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 200, X: 1, Y: 1, Z: 1},
			EndTime:     400, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(eid): {{Time: 220, X: 2, Y: 2, Z: 2, FromSync: true}},
	}}
	export := prepareCountermeasures(metadata, positions, tracks, true)
	header := wrpl.WRPLHeader{StartTime: 1700000000}
	data := renderACMIWithHeader(t, header, export.Tracks, slot, nil, export.Events)
	wantRef := time.Unix(1700000000, 0).UTC().Add(1500 * time.Millisecond).Format("2006-01-02T15:04:05Z")
	if !strings.Contains(data, "0,ReferenceTime="+wantRef+"\n") {
		t.Fatalf("ReferenceTime moved: want %s\n%s", wantRef, data[:min(len(data), 400)])
	}
	frames := parseTestACMI(data)
	if frameFindingLine(frames, -1.300, "3,") == "" {
		t.Fatalf("earlier decoy was not emitted at the correct negative offset\n%s", data)
	}
	if frameFindingLine(frames, 0.000, "2,") == "" || frameFindingLine(frames, 0.100, "1,") == "" {
		t.Fatal("legacy missile or aircraft time/ID moved")
	}
	ids := map[string]bool{}
	for _, id := range declaredACMIIDs(data) {
		ids[id] = true
	}
	if len(ids) != 3 || !ids["1"] || !ids["2"] || !ids["3"] {
		t.Fatalf("ACMI IDs = %v, want legacy 1,2 and appended decoy 3", declaredACMIIDs(data))
	}
}

func TestCountermeasureLogContainsEveryConstruction(t *testing.T) {
	const count = 1022
	history := make(map[uint64][]*projectiles.Projectile, count)
	for i := 0; i < count; i++ {
		eid := uint64(0x500000 + i)
		history[eid] = []*projectiles.Projectile{{
			ID: eid, OwnerEID: uint64(100 + i), ParentUID: uint64(1000 + i), HasParent: true,
			WeaponRef: 14 << 16, Template: "rocket",
			SpawnSample: projectiles.Sample{Time: uint32(i * 10), X: float64(i), Y: 2, Z: 3},
			EndTime:     uint32(i*10 + 5), HasEnd: true,
		}}
	}
	metadata := &projectiles.MetadataParser{History: history}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{}}
	export := prepareCountermeasures(metadata, positions, nil, true)
	if export.Constructions != count || export.EventsAdded != count || export.TracksAdded != count {
		t.Fatalf("counts constructions=%d events=%d tracks=%d", export.Constructions, export.EventsAdded, export.TracksAdded)
	}
	for _, tr := range export.Tracks {
		if !tr.estimated || !tr.key.decoy || len(tr.samples) != 2 {
			t.Fatalf("construction without sync did not get a two-sample estimate: %+v", tr)
		}
	}
	path := filepath.Join(t.TempDir(), "countermeasures.csv")
	if err := writeCountermeasureLog(path, export.Records); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != count+1 {
		t.Fatalf("CSV rows = %d, want header plus every %d construction", len(rows), count)
	}
	for _, row := range rows[1:] {
		if row[16] != "event" || row[15] != "0" {
			t.Fatalf("spawn-only CSV display/sync = %q/%q", row[16], row[15])
		}
	}
}

func TestCountermeasureLogCarriesSyncAndDisplayEvidence(t *testing.T) {
	const eid = uint64(0x400906)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, OwnerEID: 10, ParentUID: 77, HasParent: true,
			WeaponRef: 14<<16 | 0x33, Template: "rocket+arh_shell",
			SpawnSample: projectiles.Sample{Time: 100, X: 1.25, Y: 2.5, Z: 3.75},
			EndTime:     200, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(eid): {{Time: 150, X: 4, Y: 5, Z: 6, FromSync: true}},
	}}
	export := prepareCountermeasures(metadata, positions, nil, true)
	path := filepath.Join(t.TempDir(), "countermeasures.csv")
	if err := writeCountermeasureLog(path, export.Records); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("CSV rows = %d, want header plus one construction", len(rows))
	}
	row := rows[1]
	if row[12] != "1.25" || row[13] != "2.5" || row[14] != "3.75" {
		t.Fatalf("spawn XYZ = %v", row[12:15])
	}
	if row[15] != "1" || row[16] != "track+event" {
		t.Fatalf("sync/display = %q/%q", row[15], row[16])
	}
	if row[7] != "77" || row[8] != "true" || row[9] != fmt.Sprintf("%d", uint32(14<<16|0x33)) {
		t.Fatalf("parent/WeaponRef = %v", []string{row[7], row[8], row[9]})
	}
}

func TestCountermeasureTracksAreExcludedFromMissileFlightLog(t *testing.T) {
	aircraft, _ := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	tracks := appendMissileTracks([]*track{aircraft}, []missileTrack{{
		ID: 9, Name: "AIM-9M", ParentUID: 77, HasParent: true,
		Samples: []acmiSample{{t: 500, x: 10, y: 20, z: 30}},
	}})
	const eid = uint64(0x400907)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		eid: {{
			ID: eid, ParentUID: 77, HasParent: true, WeaponRef: 14 << 16,
			SpawnSample: projectiles.Sample{Time: 200, X: 1, Y: 2, Z: 3},
			EndTime:     400, HasEnd: true,
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(eid): {{Time: 220, X: 4, Y: 5, Z: 6, FromSync: true}},
	}}
	export := prepareCountermeasures(metadata, positions, tracks, true)
	path := filepath.Join(t.TempDir(), "missile_flight.csv")
	if err := writeMissileFlightLog(path, export.Tracks); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("missile flight rows = %d, want header plus one missile sample", len(rows))
	}
	for _, row := range rows[1:] {
		if row[0] == fmt.Sprintf("%d", eid) {
			t.Fatal("countermeasure was included in missile flight CSV")
		}
	}
}

func renderTestACMIWithEvents(t *testing.T, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string, events []acmiEvent) string {
	t.Helper()
	return renderACMIWithHeader(t, header, tracks, slot, names, events)
}

func renderACMIWithHeader(t *testing.T, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string, events []acmiEvent) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "countermeasures.acmi")
	if err := writeACMIWithEvents(path, header, tracks, slot, names, events); err != nil {
		t.Fatalf("writing ACMI: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
