package main

import (
	"bytes"
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

func radarTestTarget(kind uint8, present bool, raw uint32) packetfm.FMTargetData {
	return packetfm.FMTargetData{Unk0: kind, Unk18: present, Unk19: raw}
}

func radarTestLock(uid uint64) packetfm.FMTargetData {
	return radarTestTarget(6, true, 0xffff0000|uint32(uid))
}

func radarTestFrame(t uint32, uid uint64, data *packetfm.FMData) packetfm.FMUpdatePacket {
	return packetfm.FMUpdatePacket{
		CurrentTime: t,
		Entries: []*packetfm.FMEntry{{
			HasUID: true,
			UID:    uid,
			Data:   data,
		}},
	}
}

func radarTestFrameTargets(t uint32, uid uint64, targets []packetfm.FMTargetData) packetfm.FMUpdatePacket {
	return radarTestFrame(t, uid, &packetfm.FMData{TargetsData: targets})
}

func radarTestEmptyFrame(t uint32, uid uint64) packetfm.FMUpdatePacket {
	return radarTestFrameTargets(t, uid, []packetfm.FMTargetData{})
}

func radarTestSample(t uint32, x float64) acmiSample {
	return acmiSample{t: t, x: x, y: 1000, z: 0}
}

func radarTestTrack(uid uint64, samples ...acmiSample) *track {
	sort.Slice(samples, func(i, j int) bool { return samples[i].t < samples[j].t })
	return &track{key: trackKey{air: true, id: uid}, samples: samples}
}

func radarTestPoseSample(t uint32, east, up, north, yaw, pitch, roll float64) acmiSample {
	return acmiSample{t: t, x: east, y: up, z: north, yaw: yaw, pitch: pitch, roll: roll}
}

func parseRadarTestProps(props string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(props, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			out[kv[0]] = kv[1]
		}
	}
	return out
}

func requireRadarTestAngle(t *testing.T, props, name string, want float64) {
	t.Helper()
	value, ok := parseRadarTestProps(props)[name]
	if !ok {
		t.Fatalf("%s missing from %q", name, props)
	}
	got, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("%s = %q in %q: %v", name, value, props, err)
	}
	if math.Abs(got-want) > 0.006 {
		t.Fatalf("%s = %.4f, want %.4f; props=%q", name, got, want, props)
	}
}

func requireRadarTestEmptyAngle(t *testing.T, props, name string) {
	t.Helper()
	value, ok := parseRadarTestProps(props)[name]
	if !ok || value != "" {
		t.Fatalf("%s must be present and empty, got %q in %q", name, value, props)
	}
}

func radarTestPrepare(t *testing.T, fm *packetfm.PacketFlightModelParser, tracks []*track) *radarLockExport {
	t.Helper()
	x := newRadarLockExport(fm)
	if x == nil {
		t.Fatal("newRadarLockExport returned nil")
	}
	x.prepare(tracks, acmiObjectIDs(tracks))
	return x
}

func radarTestProps(x *radarLockExport, uid uint64, t uint32, tr *track) string {
	return strings.Join(x.lineProperties(uid, t, tr), ",")
}

func renderRadarTestACMI(t *testing.T, tracks []*track, radar *radarLockExport) string {
	t.Helper()
	return writeRadarTestACMIAt(t, filepath.Join(t.TempDir(), "radar.acmi"), tracks, radar)
}

func writeRadarTestACMIAt(t *testing.T, path string, tracks []*track, radar *radarLockExport) string {
	t.Helper()
	err := writeACMIWithContext(
		path,
		wrpl.WRPLHeader{StartTime: 1700000000},
		tracks,
		&packetslot.PacketSlotParser{},
		nil,
		nil,
		"",
		acmiSourceContext{},
		acmiWriterContext{radarLocks: radar},
	)
	if err != nil {
		t.Fatalf("writing ACMI: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCollectRadarLockStatesStrictKindAndFieldBoundaries(t *testing.T) {
	targets := []packetfm.FMTargetData{
		radarTestTarget(6, true, 0xffff0007),
		radarTestTarget(3, true, 0xffff0008),  // track file, not a unit designation
		radarTestTarget(6, true, 0x0000ffff),  // high half is not the unit marker
		radarTestTarget(6, false, 0xffff0009), // payload bit absent
		radarTestTarget(6, true, 0xffff0000),  // uid zero is a real unit
		radarTestTarget(6, true, 0xffff07ff),  // packet terminator, not a unit
		radarTestTarget(6, true, 0xffff0800),  // outside the 11-bit FM uid space
		radarTestTarget(6, true, 0x1234ffff),  // arbitrary bytes are not scanned
		radarTestLock(7),                      // duplicate
	}
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, targets),
	}}
	states := collectRadarLockStates(fm)
	if len(states[5]) != 1 {
		t.Fatalf("states = %d, want 1", len(states[5]))
	}
	if got := states[5][0].uids; len(got) != 2 || got[0] != 7 || got[1] != 0 {
		t.Fatalf("targets = %v, want [7 0]", got)
	}
}

func TestRadarLockUIDZeroResolvesAndSelfDesignationIsDropped(t *testing.T) {
	source := radarTestTrack(5, radarTestSample(1000, 0))
	target := radarTestTrack(0, radarTestSample(1000, 1000))
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(0)}),
	}}
	x := radarTestPrepare(t, fm, []*track{source, target})
	if got := radarTestProps(x, 5, 1000, source); !strings.Contains(got, "LockedTarget="+x.ids[0]) {
		t.Fatalf("UID 0 was not resolved as a target: %q", got)
	}

	self := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 0, []packetfm.FMTargetData{radarTestLock(0)}),
	}}
	if states := collectRadarLockStates(self); len(states[0]) != 1 || len(states[0][0].uids) != 0 {
		t.Fatalf("UID 0 self-designation was not dropped: %+v", states[0])
	}
}

func TestCollectRadarLockStatesNilRetainsAndExplicitEmptyClears(t *testing.T) {
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		radarTestFrame(2000, 5, &packetfm.FMData{TargetsData: nil}),
		radarTestEmptyFrame(3000, 5),
		radarTestFrame(4000, 5, nil),
	}}
	states := collectRadarLockStates(fm)
	if len(states[5]) != 2 {
		t.Fatalf("states = %d, want only the complete nil/empty distinction", len(states[5]))
	}
	if states[5][0].t != 1000 || len(states[5][0].uids) != 1 || states[5][0].uids[0] != 7 {
		t.Fatalf("first state = %+v", states[5][0])
	}
	if states[5][1].t != 3000 || len(states[5][1].uids) != 0 {
		t.Fatalf("explicit empty state = %+v", states[5][1])
	}

	tracks := []*track{
		radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0), radarTestSample(3000, 0)),
		radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(2000, 1000), radarTestSample(3000, 1000)),
	}
	x := radarTestPrepare(t, fm, tracks)
	if got := radarTestProps(x, 5, 1000, tracks[0]); !strings.Contains(got, "LockedTarget=") {
		t.Fatalf("lock properties = %q", got)
	}
	if got := radarTestProps(x, 5, 2000, tracks[0]); got != "" {
		t.Fatalf("nil TargetsData changed state at 2.000: %q", got)
	}
	if got := radarTestProps(x, 5, 3000, tracks[0]); !strings.Contains(got, "LockedTargetMode=0") || !strings.Contains(got, "LockedTarget=0") {
		t.Fatalf("explicit empty TargetsData did not clear: %q", got)
	}
}

func TestCollectRadarLockStatesUsesLastStateAtSameTime(t *testing.T) {
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(8)}),
	}}
	states := collectRadarLockStates(fm)
	if len(states[5]) != 1 {
		t.Fatalf("states = %d, want 1", len(states[5]))
	}
	if got := states[5][0].uids; len(got) != 1 || got[0] != 8 {
		t.Fatalf("same-time state = %v, want [8]", got)
	}
}

func TestRadarLockSlotsDeduplicateReorderAndClear(t *testing.T) {
	tracks := []*track{
		radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0), radarTestSample(3000, 0)),
		radarTestTrack(7, radarTestSample(1000, 7000), radarTestSample(2000, 7000), radarTestSample(3000, 7000)),
		radarTestTrack(8, radarTestSample(1000, 8000), radarTestSample(2000, 8000), radarTestSample(3000, 8000)),
		radarTestTrack(9, radarTestSample(1000, 9000), radarTestSample(2000, 9000), radarTestSample(3000, 9000)),
	}
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7), radarTestLock(8)}),
		radarTestFrameTargets(2000, 5, []packetfm.FMTargetData{
			radarTestLock(8), radarTestLock(7), radarTestLock(8), radarTestLock(7),
		}),
		radarTestEmptyFrame(3000, 5),
	}}
	x := radarTestPrepare(t, fm, tracks)
	ids := x.ids
	self := tracks[0]

	first := radarTestProps(x, 5, 1000, self)
	if !strings.Contains(first, "LockedTarget="+ids[7]) || !strings.Contains(first, "LockedTarget2="+ids[8]) {
		t.Fatalf("first lock slots = %q", first)
	}
	if !strings.Contains(first, "LockedTargetMode=1") {
		t.Fatalf("first lock missing mode: %q", first)
	}

	second := radarTestProps(x, 5, 2000, self)
	if !strings.Contains(second, "LockedTarget="+ids[8]) || !strings.Contains(second, "LockedTarget2="+ids[7]) {
		t.Fatalf("reordered slots = %q", second)
	}
	if strings.Count(second, "LockedTarget=") != 1 || strings.Count(second, "LockedTarget2=") != 1 {
		t.Fatalf("duplicate targets leaked into slots: %q", second)
	}

	third := radarTestProps(x, 5, 3000, self)
	if !strings.Contains(third, "LockedTarget=0") || !strings.Contains(third, "LockedTarget2=0") || !strings.Contains(third, "LockedTargetMode=0") {
		t.Fatalf("unlock did not clear both slots: %q", third)
	}
	if !strings.Contains(third, "LockedTargetRange=0") {
		t.Fatalf("unlock did not clear the old range: %q", third)
	}

	var many []packetfm.FMTargetData
	manyTracks := []*track{self}
	for uid := uint64(100); uid < 112; uid++ {
		many = append(many, radarTestLock(uid))
		manyTracks = append(manyTracks, radarTestTrack(uid, radarTestSample(1000, float64(uid))))
	}
	capped := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, many),
	}}, manyTracks)
	slots, resolved := capped.resolveSlotsAt(5, manyUIDs(many), 1000)
	if len(resolved) != maxTacviewLockedTargets {
		t.Fatalf("resolved %d targets, want %d", len(resolved), maxTacviewLockedTargets)
	}
	if slots[maxTacviewLockedTargets-1] == "" {
		t.Fatalf("last Tacview slot was not filled: %v", slots)
	}
}

func TestRadarLockPropertiesClearRangeOnTargetChangeWithoutFreshRange(t *testing.T) {
	var prev [maxTacviewLockedTargets]string
	var next [maxTacviewLockedTargets]string
	prev[0], next[0] = "2", "3"
	props := strings.Join(radarLockProperties(prev, next, true, true, 0, false, true), ",")
	if !strings.Contains(props, "LockedTarget=3") || !strings.Contains(props, "LockedTargetRange=0") {
		t.Fatalf("target-change properties = %q", props)
	}
}

func manyUIDs(targets []packetfm.FMTargetData) []uint64 {
	out := make([]uint64, 0, len(targets))
	for _, target := range targets {
		if uid, ok := radarLockTargetUID(target); ok {
			out = append(out, uid)
		}
	}
	return out
}

func TestRadarLockTargetLifecycleDoesNotLeaveStaleSlots(t *testing.T) {
	t.Run("destroyed target clears at its deletion time", func(t *testing.T) {
		source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0), radarTestSample(3000, 0))
		target := radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(2000, 1000))
		target.hasEnd, target.end = true, 2000
		fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		}}
		x := radarTestPrepare(t, fm, []*track{source, target})
		if got := radarTestProps(x, 5, 1000, source); !strings.Contains(got, "LockedTarget=") {
			t.Fatalf("initial lock = %q", got)
		}
		if len(x.extra) != 1 || x.extra[0].t != 2000 {
			t.Fatalf("extra clears = %+v, want one at 2000", x.extra)
		}
		if !strings.Contains(x.extra[0].line, x.ids[5]+",LockedTarget=0") ||
			!strings.Contains(x.extra[0].line, "LockedTargetMode=0") ||
			!strings.Contains(x.extra[0].line, "LockedTargetRange=0") {
			t.Fatalf("target-deletion clear line = %q", x.extra[0].line)
		}
		if got := radarTestProps(x, 5, 3000, source); got != "" {
			t.Fatalf("lock resumed after target deletion: %q", got)
		}
	})

	t.Run("target before birth is not locked", func(t *testing.T) {
		source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(3000, 0))
		target := radarTestTrack(7, radarTestSample(2000, 1000), radarTestSample(3000, 1000))
		fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
			radarTestFrameTargets(3000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		}}
		x := radarTestPrepare(t, fm, []*track{source, target})
		if got := radarTestProps(x, 5, 1000, source); got != "" {
			t.Fatalf("lock before target birth = %q", got)
		}
		if got := radarTestProps(x, 5, 3000, source); !strings.Contains(got, "LockedTarget=") {
			t.Fatalf("lock after target birth = %q", got)
		}
	})

	t.Run("stale coordinates are not used", func(t *testing.T) {
		source := radarTestTrack(5, radarTestSample(5000, 0))
		target := radarTestTrack(7, radarTestSample(1000, 1000))
		fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(5000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		}}
		x := radarTestPrepare(t, fm, []*track{source, target})
		if got := radarTestProps(x, 5, 5000, source); got != "" {
			t.Fatalf("lock used a stale target position: %q", got)
		}
	})

	t.Run("destroyed source stops updating", func(t *testing.T) {
		source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(1500, 0), radarTestSample(2000, 0))
		source.hasEnd, source.end = true, 2000
		target7 := radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(3000, 1000))
		target7.hasEnd, target7.end = true, 3000
		target8 := radarTestTrack(8, radarTestSample(1000, 2000), radarTestSample(3000, 2000))
		fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
			radarTestFrameTargets(2000, 5, []packetfm.FMTargetData{radarTestLock(8)}),
			radarTestFrameTargets(3000, 5, []packetfm.FMTargetData{radarTestLock(8)}),
		}}
		x := radarTestPrepare(t, fm, []*track{source, target7, target8})
		if got := radarTestProps(x, 5, 1000, source); !strings.Contains(got, "LockedTarget=") {
			t.Fatalf("pre-destruction lock = %q", got)
		}
		if got := radarTestProps(x, 5, 2000, source); got != "" {
			t.Fatalf("source deletion timestamp emitted lock properties: %q", got)
		}
		if got := radarTestProps(x, 5, 3000, source); got != "" {
			t.Fatalf("destroyed source emitted a later lock: %q", got)
		}
		if len(x.extra) != 0 {
			t.Fatalf("destroyed source emitted independent clears: %+v", x.extra)
		}
		locked, changes := x.stats()
		if locked != 1 || changes != 1 {
			t.Fatalf("stats = (%d,%d), want (1,1)", locked, changes)
		}
	})
}

func TestRadarLockRangeNeverUsesFuturePositions(t *testing.T) {
	source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0))
	target := radarTestTrack(7, radarTestSample(1000, 3000), radarTestSample(3000, 0))
	target.samples[0].y, target.samples[0].z = 0, 0
	target.samples[1].y, target.samples[1].z = 0, 0
	source.samples[0].y, source.samples[0].z = 0, 0
	source.samples[1].y, source.samples[1].z = 0, 0
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(2000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
	}}
	x := radarTestPrepare(t, fm, []*track{source, target})
	rng, ok := x.primaryRangeAt(5, source, []uint64{7}, 2000)
	if !ok || rng != 3000 {
		t.Fatalf("range = %v (ok=%t), want 3000 from the at-or-before sample", rng, ok)
	}

	staleTarget := radarTestTrack(8, radarTestSample(999, 3000), radarTestSample(3000, 0))
	staleTarget.samples[0].y, staleTarget.samples[0].z = 0, 0
	staleTarget.samples[1].y, staleTarget.samples[1].z = 0, 0
	staleTracks := []*track{source, staleTarget}
	stale := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(2000, 5, []packetfm.FMTargetData{radarTestLock(8)}),
	}}, staleTracks)
	if _, ok := stale.primaryRangeAt(5, source, []uint64{8}, 2000); ok {
		t.Fatal("range accepted a target sample older than the bounded window")
	}
	if got := radarTestProps(stale, 5, 2000, source); got != "" {
		t.Fatalf("stale target produced lock properties: %q", got)
	}
}

func TestRadarLockAnglesCardinalBodyDirections(t *testing.T) {
	tests := []struct {
		name        string
		yaw         float64
		east        float64
		up          float64
		north       float64
		wantAzimuth float64
		azimuthOK   bool
		wantElev    float64
	}{
		{name: "ahead", yaw: 0, north: 1000, wantAzimuth: 0, azimuthOK: true},
		{name: "right of northbound", yaw: 0, east: 1000, wantAzimuth: 90, azimuthOK: true},
		{name: "left of northbound", yaw: 0, east: -1000, wantAzimuth: -90, azimuthOK: true},
		{name: "left of eastbound", yaw: 90, north: 1000, wantAzimuth: -90, azimuthOK: true},
		{name: "right of eastbound", yaw: 90, north: -1000, wantAzimuth: 90, azimuthOK: true},
		{name: "above", yaw: 0, up: 1000, wantElev: 90},
		{name: "below", yaw: 0, up: -1000, wantElev: -90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := radarTestTrack(5, radarTestPoseSample(1000, 0, 0, 0, tt.yaw, 0, 0))
			target := radarTestTrack(7, radarTestPoseSample(1000, tt.east, tt.up, tt.north, 0, 0, 0))
			x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
				radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
			}}, []*track{source, target})
			props := radarTestProps(x, 5, 1000, source)
			if tt.azimuthOK {
				requireRadarTestAngle(t, props, "LockedTargetAzimuth", tt.wantAzimuth)
			} else {
				requireRadarTestEmptyAngle(t, props, "LockedTargetAzimuth")
			}
			requireRadarTestAngle(t, props, "LockedTargetElevation", tt.wantElev)
			if rangeValue := parseRadarTestProps(props)["LockedTargetRange"]; rangeValue != "1000" {
				t.Fatalf("range = %q in %q, want 1000", rangeValue, props)
			}
		})
	}
}

func TestRadarLockAnglesPitchBankAndInverted(t *testing.T) {
	const cosThirty = 0.8660254037844386
	tests := []struct {
		name        string
		yaw         float64
		pitch       float64
		roll        float64
		east        float64
		up          float64
		north       float64
		wantAzimuth float64
		azimuthOK   bool
		wantElev    float64
	}{
		{name: "pitch up along flight path", pitch: 30, up: 500, north: 1000 * cosThirty, wantAzimuth: 0, azimuthOK: true},
		{name: "pitch down along flight path", pitch: -30, up: -500, north: 1000 * cosThirty, wantAzimuth: 0, azimuthOK: true},
		{name: "bank right puts east under body", roll: 90, east: 1000, wantElev: 90},
		{name: "bank left puts east behind body up", roll: -90, east: 1000, wantElev: -90},
		{name: "inverted puts east left", roll: 180, east: 1000, wantAzimuth: -90, azimuthOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := radarTestTrack(5, radarTestPoseSample(1000, 0, 0, 0, tt.yaw, tt.pitch, tt.roll))
			target := radarTestTrack(7, radarTestPoseSample(1000, tt.east, tt.up, tt.north, 0, 0, 0))
			x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
				radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
			}}, []*track{source, target})
			props := radarTestProps(x, 5, 1000, source)
			if tt.azimuthOK {
				requireRadarTestAngle(t, props, "LockedTargetAzimuth", tt.wantAzimuth)
			} else {
				requireRadarTestEmptyAngle(t, props, "LockedTargetAzimuth")
			}
			requireRadarTestAngle(t, props, "LockedTargetElevation", tt.wantElev)
		})
	}
}

func TestRadarLockAnglesClearOnTargetLifecycleChanges(t *testing.T) {
	t.Run("primary target change publishes new geometry", func(t *testing.T) {
		source := radarTestTrack(5,
			radarTestPoseSample(1000, 0, 0, 0, 0, 0, 0),
			radarTestPoseSample(2000, 0, 0, 0, 0, 0, 0),
			radarTestPoseSample(3000, 0, 0, 0, 0, 0, 0))
		east := radarTestTrack(7,
			radarTestPoseSample(1000, 1000, 0, 0, 0, 0, 0),
			radarTestPoseSample(2000, 1000, 0, 0, 0, 0, 0),
			radarTestPoseSample(3000, 1000, 0, 0, 0, 0, 0))
		north := radarTestTrack(8,
			radarTestPoseSample(1000, 0, 0, 1000, 0, 0, 0),
			radarTestPoseSample(2000, 0, 0, 1000, 0, 0, 0),
			radarTestPoseSample(3000, 0, 0, 1000, 0, 0, 0))
		coincident := radarTestTrack(9,
			radarTestPoseSample(1000, 0, 0, 0, 0, 0, 0),
			radarTestPoseSample(2000, 0, 0, 0, 0, 0, 0),
			radarTestPoseSample(3000, 0, 0, 0, 0, 0, 0))
		x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
			radarTestFrameTargets(2000, 5, []packetfm.FMTargetData{radarTestLock(8)}),
			radarTestFrameTargets(3000, 5, []packetfm.FMTargetData{radarTestLock(9)}),
		}}, []*track{source, east, north, coincident})

		requireRadarTestAngle(t, radarTestProps(x, 5, 1000, source), "LockedTargetAzimuth", 90)
		changed := radarTestProps(x, 5, 2000, source)
		requireRadarTestAngle(t, changed, "LockedTargetAzimuth", 0)
		requireRadarTestAngle(t, changed, "LockedTargetElevation", 0)
		degenerate := radarTestProps(x, 5, 3000, source)
		requireRadarTestEmptyAngle(t, degenerate, "LockedTargetAzimuth")
		requireRadarTestEmptyAngle(t, degenerate, "LockedTargetElevation")
		if rangeValue := parseRadarTestProps(degenerate)["LockedTargetRange"]; rangeValue != "0" {
			t.Fatalf("degenerate target-change range = %q in %q", rangeValue, degenerate)
		}
	})

	t.Run("sample gap", func(t *testing.T) {
		source := radarTestTrack(5,
			radarTestPoseSample(1000, 0, 0, 0, 0, 0, 0),
			radarTestPoseSample(2500, 0, 0, 0, 0, 0, 0))
		target := radarTestTrack(7, radarTestPoseSample(1000, 1000, 0, 0, 0, 0, 0))
		x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
			radarTestFrameTargets(2500, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		}}, []*track{source, target})
		props := radarTestProps(x, 5, 2500, source)
		requireRadarTestEmptyAngle(t, props, "LockedTargetAzimuth")
		requireRadarTestEmptyAngle(t, props, "LockedTargetElevation")
		if !strings.Contains(props, "LockedTargetMode=0") || !strings.Contains(props, "LockedTargetRange=0") {
			t.Fatalf("gap did not clear the full lock geometry: %q", props)
		}
	})

	t.Run("target destruction", func(t *testing.T) {
		source := radarTestTrack(5,
			radarTestPoseSample(1000, 0, 0, 0, 0, 0, 0),
			radarTestPoseSample(2000, 0, 0, 0, 0, 0, 0))
		target := radarTestTrack(7, radarTestPoseSample(1000, 1000, 0, 0, 0, 0, 0))
		target.hasEnd, target.end = true, 2000
		x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		}}, []*track{source, target})
		if len(x.extra) != 1 {
			t.Fatalf("death clears = %+v, want one", x.extra)
		}
		requireRadarTestEmptyAngle(t, x.extra[0].line, "LockedTargetAzimuth")
		requireRadarTestEmptyAngle(t, x.extra[0].line, "LockedTargetElevation")
	})

	t.Run("explicit unlock", func(t *testing.T) {
		source := radarTestTrack(5,
			radarTestPoseSample(1000, 0, 0, 0, 0, 0, 0),
			radarTestPoseSample(2000, 0, 0, 0, 0, 0, 0))
		target := radarTestTrack(7,
			radarTestPoseSample(1000, 1000, 0, 0, 0, 0, 0),
			radarTestPoseSample(2000, 1000, 0, 0, 0, 0, 0))
		x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
			radarTestEmptyFrame(2000, 5),
		}}, []*track{source, target})
		props := radarTestProps(x, 5, 2000, source)
		requireRadarTestEmptyAngle(t, props, "LockedTargetAzimuth")
		requireRadarTestEmptyAngle(t, props, "LockedTargetElevation")
	})
}

func TestRadarLockAnglesNoFutureInterpolationAndMissingGeometry(t *testing.T) {
	t.Run("earlier target sample is used", func(t *testing.T) {
		source := radarTestTrack(5,
			radarTestPoseSample(1000, 0, 0, 0, 0, 0, 0),
			radarTestPoseSample(2000, 0, 0, 0, 0, 0, 0))
		target := radarTestTrack(7,
			radarTestPoseSample(1000, 1000, 0, 0, 0, 0, 0),
			radarTestPoseSample(3000, 0, 0, 1000, 0, 0, 0))
		x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(2000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		}}, []*track{source, target})
		requireRadarTestAngle(t, radarTestProps(x, 5, 2000, source), "LockedTargetAzimuth", 90)
	})

	t.Run("future-only target is unavailable", func(t *testing.T) {
		source := radarTestTrack(5, radarTestPoseSample(2000, 0, 0, 0, 0, 0, 0))
		target := radarTestTrack(7, radarTestPoseSample(3000, 1000, 0, 0, 0, 0, 0))
		x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(2000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		}}, []*track{source, target})
		if got := radarTestProps(x, 5, 2000, source); got != "" {
			t.Fatalf("future target sample produced properties: %q", got)
		}
	})

	t.Run("coincident positions keep zero range", func(t *testing.T) {
		source := radarTestTrack(5, radarTestPoseSample(1000, 10, 20, 30, 45, 20, -15))
		target := radarTestTrack(7, radarTestPoseSample(1000, 10, 20, 30, 0, 0, 0))
		x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
			radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		}}, []*track{source, target})
		props := radarTestProps(x, 5, 1000, source)
		if rangeValue := parseRadarTestProps(props)["LockedTargetRange"]; rangeValue != "0" {
			t.Fatalf("coincident range = %q in %q", rangeValue, props)
		}
		requireRadarTestEmptyAngle(t, props, "LockedTargetAzimuth")
		requireRadarTestEmptyAngle(t, props, "LockedTargetElevation")
	})

	for _, attitude := range []struct {
		name  string
		yaw   float64
		pitch float64
		roll  float64
	}{
		{name: "yaw", yaw: math.NaN()},
		{name: "pitch", pitch: math.Inf(1)},
		{name: "roll", roll: math.Inf(-1)},
	} {
		t.Run("nonfinite "+attitude.name+" keeps range", func(t *testing.T) {
			source := radarTestTrack(5, radarTestPoseSample(1000, 0, 0, 0, attitude.yaw, attitude.pitch, attitude.roll))
			target := radarTestTrack(7, radarTestPoseSample(1000, 1000, 0, 0, 0, 0, 0))
			x := radarTestPrepare(t, &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
				radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
			}}, []*track{source, target})
			props := radarTestProps(x, 5, 1000, source)
			if rangeValue := parseRadarTestProps(props)["LockedTargetRange"]; rangeValue != "1000" {
				t.Fatalf("nonfinite attitude range = %q in %q", rangeValue, props)
			}
			requireRadarTestEmptyAngle(t, props, "LockedTargetAzimuth")
			requireRadarTestEmptyAngle(t, props, "LockedTargetElevation")
		})
	}
}

func TestWriteACMIWithRadarLockIndependentClearOnTargetDestruction(t *testing.T) {
	source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0), radarTestSample(3000, 0))
	target := radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(2000, 1000))
	target.hasEnd, target.end = true, 2000
	tracks := []*track{source, target}
	ids := acmiObjectIDs(tracks)
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
	}}
	radar := radarTestPrepare(t, fm, tracks)
	data := renderRadarTestACMI(t, tracks, radar)
	frames := parseTestACMI(data)

	lockLine := frameFindingLine(frames, 0.000, ids[trackKey{air: true, id: 5}]+",T=")
	if !strings.Contains(lockLine, "LockedTarget="+ids[trackKey{air: true, id: 7}]) {
		t.Fatalf("initial source update lacks target: %q", lockLine)
	}
	clearLine := frameFindingLine(frames, 1.000, ids[trackKey{air: true, id: 5}]+",LockedTarget=0")
	if !strings.Contains(clearLine, "LockedTargetMode=0") {
		t.Fatalf("missing independent target-destruction clear: %q", clearLine)
	}
	if got := frameFindingLine(frames, 1.000, "-"+ids[trackKey{air: true, id: 7}]); got == "" {
		t.Fatal("target was not deleted at its end time")
	}
	if got := frameFindingLine(frames, 2.000, ids[trackKey{air: true, id: 5}]+",T="); strings.Contains(got, "LockedTarget") {
		t.Fatalf("stale lock survived target destruction: %q", got)
	}
	assertRadarReferencesDeclared(t, data)
}

func TestWriteACMIWithRadarLockExplicitEmptyClear(t *testing.T) {
	source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0), radarTestSample(3000, 0))
	target := radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(2000, 1000), radarTestSample(3000, 1000))
	tracks := []*track{source, target}
	ids := acmiObjectIDs(tracks)
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		radarTestEmptyFrame(2000, 5),
	}}
	radar := radarTestPrepare(t, fm, tracks)
	data := renderRadarTestACMI(t, tracks, radar)
	frames := parseTestACMI(data)
	sourceID := ids[trackKey{air: true, id: 5}]

	clear := frameFindingLine(frames, 1.000, sourceID+",T=")
	if !strings.Contains(clear, "LockedTarget=0") || !strings.Contains(clear, "LockedTargetMode=0") {
		t.Fatalf("explicit empty list did not clear the source line: %q", clear)
	}
	if !strings.Contains(clear, "LockedTargetRange=0") {
		t.Fatalf("explicit empty list did not clear the old range: %q", clear)
	}
	if got := frameFindingLine(frames, 2.000, sourceID+",T="); strings.Contains(got, "LockedTarget") {
		t.Fatalf("cleared lock returned without a new state: %q", got)
	}
}

func TestRadarLockIgnoresGroundTrackWithCollidingNumericID(t *testing.T) {
	source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0))
	ground := &track{key: trackKey{air: false, id: 5}, samples: []acmiSample{radarTestSample(1000, 50)}}
	target := radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(2000, 1000))
	tracks := []*track{source, ground, target}
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
	}}
	radar := radarTestPrepare(t, fm, tracks)
	if got := radarTestProps(radar, 5, 1000, ground); got != "" {
		t.Fatalf("ground track received aircraft lock properties: %q", got)
	}
	data := renderRadarTestACMI(t, tracks, radar)
	frames := parseTestACMI(data)
	groundID := acmiObjectIDs(tracks)[ground.key]
	line := frameFindingLine(frames, 0.000, groundID+",T=")
	if strings.Contains(line, "LockedTarget") {
		t.Fatalf("ground update was polluted by a colliding aircraft uid: %q", line)
	}
	sourceID := acmiObjectIDs(tracks)[source.key]
	if line := frameFindingLine(frames, 0.000, sourceID+",T="); !strings.Contains(line, "LockedTarget=") {
		t.Fatalf("aircraft lock was lost: %q", line)
	}
}

func TestACMIWriterNilRadarContextMatchesLegacyWrapper(t *testing.T) {
	source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 10))
	target := radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(2000, 1010))
	tracks := []*track{source, target}
	header := wrpl.WRPLHeader{StartTime: 1700000000}
	slot := &packetslot.PacketSlotParser{}

	legacyPath := filepath.Join(t.TempDir(), "legacy.acmi")
	if err := writeACMIWithEventsMapIDAndSource(legacyPath, header, tracks, slot, nil, nil, "", acmiSourceContext{}); err != nil {
		t.Fatal(err)
	}
	legacy, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	current := renderRadarTestACMI(t, tracks, nil)
	if !bytes.Equal([]byte(radarTestStableRecording(string(legacy))), []byte(radarTestStableRecording(current))) {
		t.Fatal("nil radar context changed the legacy ACMI bytes")
	}
	if strings.Contains(current, "LockedTarget") {
		t.Fatal("disabled radar writer emitted LockedTarget properties")
	}
}

func TestRadarLockRepeatedExportDoesNotLeakSlotState(t *testing.T) {
	source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0))
	target := radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(2000, 1000))
	tracks := []*track{source, target}
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
	}}
	radar := radarTestPrepare(t, fm, tracks)
	first := renderRadarTestACMI(t, tracks, radar)
	second := renderRadarTestACMI(t, tracks, radar)
	if radarTestStableRecording(first) != radarTestStableRecording(second) {
		t.Fatal("reusing the radar export changed a later ACMI")
	}
	locked, changes := radar.stats()
	if locked != 1 || changes != 1 || radar.aircraft != 1 {
		t.Fatalf("stats leaked across exports: aircraft=%d locked=%d changes=%d", radar.aircraft, locked, changes)
	}
}

// Separate files may be created on opposite sides of a UTC second boundary.
// Their creation time is expected to differ; all replay/radar data must match.
func radarTestStableRecording(data string) string {
	lines := strings.Split(data, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "0,RecordingTime=") {
			lines[i] = "0,RecordingTime=<creation time>"
		}
	}
	return strings.Join(lines, "\n")
}

func TestRadarLockLogRecordsChangesAndDoesNotClaimSTT(t *testing.T) {
	source := radarTestTrack(5, radarTestSample(1000, 0), radarTestSample(2000, 0), radarTestSample(3000, 0))
	target := radarTestTrack(7, radarTestSample(1000, 1000), radarTestSample(2000, 1000), radarTestSample(3000, 1000))
	tracks := []*track{source, target}
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarTestFrameTargets(1000, 5, []packetfm.FMTargetData{radarTestLock(7)}),
		radarTestEmptyFrame(2000, 5),
	}}
	radar := radarTestPrepare(t, fm, tracks)
	_ = renderRadarTestACMI(t, tracks, radar)
	path := filepath.Join(t.TempDir(), "locks.csv")
	if err := writeRadarLockLog(path, radar, tracks, &packetslot.PacketSlotParser{}, nil); err != nil {
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
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want header plus lock and unlock", len(rows))
	}
	wantHeader := []string{"time_sec", "air_id", "air_uid", "air_name", "pilot", "target_count", "target_uids", "target_ids", "target_names", "primary_range_m"}
	if strings.Join(rows[0], ",") != strings.Join(wantHeader, ",") {
		t.Fatalf("header = %v", rows[0])
	}
	if rows[1][0] != "1.000" || rows[1][5] != "1" || rows[1][6] != "7" || rows[1][8] != "air:7" {
		t.Fatalf("lock row = %v", rows[1])
	}
	if rows[2][0] != "2.000" || rows[2][5] != "0" || rows[2][6] != "" {
		t.Fatalf("unlock row = %v", rows[2])
	}
	all, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(all), "STT") {
		t.Fatal("radar log claims a confirmed STT state")
	}
}

var radarLockRefPattern = regexp.MustCompile("LockedTarget(?:[2-9])?=([0-9A-F]+)")

func assertRadarReferencesDeclared(t *testing.T, data string) {
	t.Helper()
	declared := map[string]bool{}
	for _, id := range declaredACMIIDs(data) {
		declared[id] = true
	}
	for _, match := range radarLockRefPattern.FindAllStringSubmatch(data, -1) {
		if match[1] == "0" {
			continue
		}
		if !declared[match[1]] {
			t.Fatalf("LockedTarget references undeclared object %s", match[1])
		}
	}
}
