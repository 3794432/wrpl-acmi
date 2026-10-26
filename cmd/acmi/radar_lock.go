package main

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

// Tacview accepts LockedTarget plus LockedTarget2 through LockedTarget9.
const maxTacviewLockedTargets = 9

// A designation must stay usable without unbounded extrapolation. The same
// window bounds both the target reference and the slant-range calculation.
const radarLockPositionMaxAgeMs = 1000

// FM unit ids are 11-bit. 0x7ff is the packet terminator, not a unit.
const radarLockMaxUnitUID = uint64(0x7fe)

// radarLockTargetState is one complete TargetsData list at one FM sync.
// A nil TargetsData list is never collected; an empty but non-nil list is an
// explicit unlock.
type radarLockTargetState struct {
	t    uint32
	uids []uint64
}

type radarLockEvent struct {
	t     uint32
	sync  bool
	props []string
}

type radarLockExtraLine struct {
	t    uint32
	line string
}

type radarLockLogRow struct {
	t      uint32
	source trackKey // concrete source sortie, not the raw replay uid
	slots  [maxTacviewLockedTargets]string
	uids   []trackKey // target sorties resolved at t; same order as slots
	rangeM float64
	hasRng bool
}

// radarLockAngles are expressed in degrees when formatted for ACMI.
// Azimuth is positive to the aircraft's right and elevation is positive up.
type radarLockAngles struct {
	azimuthDeg   float64
	elevationDeg float64
	hasAzimuth   bool
	hasElevation bool
}

// radarLockGeometry is computed from one pair of source and target samples so
// range and angles cannot disagree about target availability or sample time.
type radarLockGeometry struct {
	rangeM   float64
	hasRange bool
	angles   radarLockAngles
}

// radarLockExport maps kind-6 FM target designations to Tacview LockedTarget
// properties. All writer-visible state is rebuilt by prepare, so exporting the
// same replay twice cannot leak slot state from the first write.
type radarLockExport struct {
	// Raw FM syncs are grouped by the masked replay uid. prepare assigns each
	// state to exactly one sortie by its time and that sortie's explicit end.
	states map[uint64][]radarLockTargetState

	ids    map[trackKey]string
	tracks map[trackKey]*track
	byUID  map[uint64][]*track

	events  map[trackKey]map[uint32]radarLockEvent
	extra   []radarLockExtraLine
	logRows []radarLockLogRow

	aircraft    int
	lockedSyncs int
	changes     int
	prepared    bool
}

// radarLockTargetUID accepts only the typed kind-6 field shape used by the
// replay: a present uint32 payload whose high half is exactly 0xffff and whose
// low half names an 11-bit unit. UID 0 is a real object; 0x7ff terminates the
// FM entry list. No raw-byte scanning or partial record is used.
func radarLockTargetUID(target packetfm.FMTargetData) (uint64, bool) {
	if target.Unk0 != 6 || !target.Unk18 {
		return 0, false
	}
	if target.Unk19>>16 != 0xffff {
		return 0, false
	}
	uid := uint64(target.Unk19 & 0xffff)
	if uid > radarLockMaxUnitUID {
		return 0, false
	}
	return uid, true
}

// collectRadarLockStates keeps only complete, successfully parsed target lists.
// nil means unavailable (skip and retain the previous state); a non-nil empty
// slice means the aircraft explicitly sent no designations and must unlock.
// When one aircraft appears more than once at the same timestamp, the last
// complete state wins.
func collectRadarLockStates(fm *packetfm.PacketFlightModelParser) map[uint64][]radarLockTargetState {
	out := map[uint64][]radarLockTargetState{}
	if fm == nil {
		return out
	}
	byTime := map[uint64]map[uint32]radarLockTargetState{}
	for _, frame := range fm.Results {
		for _, entry := range frame.Entries {
			// Unk0&&Unk1 is a flags-only record with zero-filled telemetry, not an
			// explicit empty target list.
			if entry == nil || entry.Data == nil || (entry.Data.Unk0 && entry.Data.Unk1) || entry.Data.TargetsData == nil {
				continue
			}
			uids := make([]uint64, 0, len(entry.Data.TargetsData))
			seen := map[uint64]bool{}
			for _, target := range entry.Data.TargetsData {
				uid, ok := radarLockTargetUID(target)
				if !ok || uid == entry.UID || seen[uid] {
					continue
				}
				seen[uid] = true
				uids = append(uids, uid)
			}
			states := byTime[entry.UID]
			if states == nil {
				states = map[uint32]radarLockTargetState{}
				byTime[entry.UID] = states
			}
			states[frame.CurrentTime] = radarLockTargetState{t: frame.CurrentTime, uids: uids}
		}
	}
	for uid, statesByTime := range byTime {
		times := make([]uint32, 0, len(statesByTime))
		for t := range statesByTime {
			times = append(times, t)
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		states := make([]radarLockTargetState, 0, len(times))
		for _, t := range times {
			states = append(states, statesByTime[t])
		}
		out[uid] = states
	}
	return out
}

func newRadarLockExport(fm *packetfm.PacketFlightModelParser) *radarLockExport {
	states := collectRadarLockStates(fm)
	if len(states) == 0 {
		return nil
	}
	return &radarLockExport{states: states}
}

// prepare resolves ACMI ids and builds deterministic lock, clear and log event
// timelines. It is safe to call again with the same tracks: all derived state is
// replaced and the collected FM states are left untouched.
func (x *radarLockExport) prepare(tracks []*track, ids map[trackKey]string) {
	if x == nil {
		return
	}
	x.ids = map[trackKey]string{}
	x.tracks = map[trackKey]*track{}
	x.byUID = map[uint64][]*track{}
	for _, tr := range tracks {
		if !radarLockAirframe(tr) {
			continue
		}
		x.tracks[tr.key] = tr
		x.byUID[tr.key.id] = append(x.byUID[tr.key.id], tr)
		if id := ids[tr.key]; id != "" {
			x.ids[tr.key] = id
		}
	}
	for uid := range x.byUID {
		radarSortTracks(x.byUID[uid])
	}

	x.events = map[trackKey]map[uint32]radarLockEvent{}
	x.extra = nil
	x.logRows = nil
	x.aircraft = 0
	x.lockedSyncs = 0
	x.changes = 0
	x.prepared = true

	sources := make([]uint64, 0, len(x.states))
	for uid := range x.states {
		if len(x.byUID[uid]) > 0 {
			sources = append(sources, uid)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	for _, uid := range sources {
		assigned := map[trackKey][]radarLockTargetState{}
		for _, state := range x.states[uid] {
			tr := radarTrackAt(x.byUID[uid], uid, state.t)
			if tr == nil || x.ids[tr.key] == "" {
				continue
			}
			assigned[tr.key] = append(assigned[tr.key], state)
		}
		for _, tr := range x.byUID[uid] {
			if states := assigned[tr.key]; len(states) > 0 {
				x.prepareSource(tr, states)
			}
		}
	}
	sort.Slice(x.extra, func(i, j int) bool {
		if x.extra[i].t != x.extra[j].t {
			return x.extra[i].t < x.extra[j].t
		}
		return x.extra[i].line < x.extra[j].line
	})
	sort.Slice(x.logRows, func(i, j int) bool {
		if x.logRows[i].t != x.logRows[j].t {
			return x.logRows[i].t < x.logRows[j].t
		}
		if x.logRows[i].source.id != x.logRows[j].source.id {
			return x.logRows[i].source.id < x.logRows[j].source.id
		}
		return x.ids[x.logRows[i].source] < x.ids[x.logRows[j].source]
	})
}

func radarLockAirframe(tr *track) bool {
	return isKillAircraft(tr)
}

// radarTrackAt selects one aircraft track by its raw FM uid and its explicit
// lifetime. Sample gaps are not respawns, so absence of hasEnd does not end a
// track at its last position sample. A destroyed sortie is absent at its
// deletion timestamp. Ambiguous overlaps return nil rather than borrowing a
// different sortie's object id.
func radarTrackAt(tracks []*track, uid uint64, t uint32) *track {
	tr := aircraftTrackAt(tracks, uid, t)
	if tr == nil || !radarTrackLiveAt(tr, t) {
		return nil
	}
	return tr
}

func radarTrackLiveAt(tr *track, t uint32) bool {
	if tr == nil || len(tr.samples) == 0 || t < tr.samples[0].t {
		return false
	}
	return !tr.hasEnd || t < tr.end
}

func radarSortTracks(tracks []*track) {
	sort.SliceStable(tracks, func(i, j int) bool {
		if len(tracks[i].samples) == 0 || len(tracks[j].samples) == 0 {
			return len(tracks[i].samples) > 0
		}
		if tracks[i].samples[0].t != tracks[j].samples[0].t {
			return tracks[i].samples[0].t < tracks[j].samples[0].t
		}
		if tracks[i].hasEnd != tracks[j].hasEnd {
			return tracks[i].hasEnd
		}
		return tracks[i].end < tracks[j].end
	})
}

func (x *radarLockExport) prepareSource(tr *track, states []radarLockTargetState) {
	if tr == nil || len(states) == 0 || x.ids[tr.key] == "" {
		return
	}

	syncAt := make(map[uint32]radarLockTargetState, len(states))
	designated := map[trackKey]bool{}
	eventTimes := map[uint32]bool{}
	for _, state := range states {
		syncAt[state.t] = state
		eventTimes[state.t] = true
		for _, uid := range state.uids {
			target := radarTrackAt(x.byUID[uid], uid, state.t)
			if target != nil && target.key != tr.key {
				designated[target.key] = true
			}
		}
	}
	for key := range designated {
		if target := x.tracks[key]; target != nil && target.hasEnd {
			eventTimes[target.end] = true
		}
	}

	times := make([]uint32, 0, len(eventTimes))
	for t := range eventTimes {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })

	var (
		current    []uint64
		prev       [maxTacviewLockedTargets]string
		prevOn     bool
		prevHasRng bool
		everLock   bool
	)
	for _, t := range times {
		if t < tr.samples[0].t {
			continue
		}
		if tr.hasEnd && t >= tr.end {
			break
		}
		isSync := false
		if state, ok := syncAt[t]; ok {
			current = state.uids
			isSync = true
		}
		slots, targets := x.resolveTargetsAt(tr.key, current, t)
		on := len(targets) > 0
		geometry := x.primaryGeometryAtTracks(tr, targets, t)
		changed := slots != prev || on != prevOn
		props := radarLockPropertiesWithAngles(prev, slots, prevOn, on,
			geometry, prevHasRng)
		if len(props) > 0 {
			if isSync {
				events := x.events[tr.key]
				if events == nil {
					events = map[uint32]radarLockEvent{}
					x.events[tr.key] = events
				}
				events[t] = radarLockEvent{t: t, sync: true, props: props}
			} else if changed {
				x.extra = append(x.extra, radarLockExtraLine{
					t:    t,
					line: x.ids[tr.key] + "," + strings.Join(props, ","),
				})
			}
		}
		if isSync && on {
			x.lockedSyncs++
		}
		if on {
			everLock = true
		}
		if changed {
			x.changes++
			x.logRows = append(x.logRows, radarLockLogRow{
				t:      t,
				source: tr.key,
				slots:  slots,
				uids:   append([]trackKey(nil), targets...),
				rangeM: geometry.rangeM,
				hasRng: geometry.hasRange,
			})
		}
		prev = slots
		prevOn = on
		prevHasRng = geometry.hasRange
	}
	if everLock {
		x.aircraft++
	}
}

func (x *radarLockExport) resolveSlotsAt(source uint64, uids []uint64, t uint32) ([maxTacviewLockedTargets]string, []uint64) {
	slots, targets := x.resolveTargetsAt(trackKey{air: true, id: source}, uids, t)
	uidsOut := make([]uint64, 0, len(targets))
	for _, target := range targets {
		uidsOut = append(uidsOut, target.id)
	}
	return slots, uidsOut
}

// resolveTargetsAt applies designation order, deduplicates target instances and
// ACMI object ids, drops unknown/dead/not-yet-born/stale targets, and caps the
// list at Tacview's nine slots. The concrete track key selected at t is kept so
// one UID's later sortie can never inherit the earlier sortie's object id.
func (x *radarLockExport) resolveTargetsAt(source trackKey, uids []uint64, t uint32) ([maxTacviewLockedTargets]string, []trackKey) {
	var slots [maxTacviewLockedTargets]string
	resolved := make([]trackKey, 0, min(len(uids), maxTacviewLockedTargets))
	seenKey := map[trackKey]bool{}
	seenID := map[string]bool{}
	for _, uid := range uids {
		if len(resolved) >= maxTacviewLockedTargets {
			break
		}
		target := radarTrackAt(x.byUID[uid], uid, t)
		if target == nil || target.key == source || seenKey[target.key] || !x.targetTrackAvailableAt(target, t) {
			continue
		}
		id := x.ids[target.key]
		if id == "" || seenID[id] {
			continue
		}
		seenKey[target.key] = true
		seenID[id] = true
		slots[len(resolved)] = id
		resolved = append(resolved, target.key)
	}
	return slots, resolved
}

// targetAvailableAt requires a track that exists at t and a real position sample
// at or before t. A destroyed aircraft is unavailable at its deletion timestamp.
func (x *radarLockExport) targetAvailableAt(uid uint64, t uint32) bool {
	tr := radarTrackAt(x.byUID[uid], uid, t)
	return x.targetTrackAvailableAt(tr, t)
}

func (x *radarLockExport) targetTrackAvailableAt(tr *track, t uint32) bool {
	if tr == nil || len(tr.samples) == 0 || !radarTrackLiveAt(tr, t) {
		return false
	}
	_, ok := sampleAtOrBefore(tr, t, radarLockPositionMaxAgeMs)
	return ok
}

func sampleAtOrBefore(tr *track, t uint32, maxAge uint32) (acmiSample, bool) {
	if tr == nil || len(tr.samples) == 0 || t < tr.samples[0].t {
		return acmiSample{}, false
	}
	idx := sort.Search(len(tr.samples), func(i int) bool {
		return tr.samples[i].t > t
	}) - 1
	if idx < 0 {
		return acmiSample{}, false
	}
	s := tr.samples[idx]
	if uint64(t)-uint64(s.t) > uint64(maxAge) {
		return acmiSample{}, false
	}
	return s, true
}

// primaryGeometryAt computes the primary target's range and body-relative
// angles from one pair of fresh at-or-before samples. The source and target
// use the same availability checks as the exported range. Future samples are
// never interpolated or borrowed.
func (x *radarLockExport) primaryGeometryAt(source uint64, tr *track, targets []uint64, t uint32) radarLockGeometry {
	if len(targets) == 0 || tr == nil {
		return radarLockGeometry{}
	}
	target := radarTrackAt(x.byUID[targets[0]], targets[0], t)
	if target == nil {
		return radarLockGeometry{}
	}
	return x.primaryGeometryAtTracks(tr, []trackKey{target.key}, t)
}

func (x *radarLockExport) primaryGeometryAtTracks(tr *track, targets []trackKey, t uint32) radarLockGeometry {
	if len(targets) == 0 || tr == nil || !radarTrackLiveAt(tr, t) {
		return radarLockGeometry{}
	}
	dst := x.tracks[targets[0]]
	if !x.targetTrackAvailableAt(dst, t) {
		return radarLockGeometry{}
	}
	a, ok := sampleAtOrBefore(tr, t, radarLockPositionMaxAgeMs)
	if !ok {
		return radarLockGeometry{}
	}
	b, ok := sampleAtOrBefore(dst, t, radarLockPositionMaxAgeMs)
	if !ok {
		return radarLockGeometry{}
	}
	dx := b.x - a.x
	dy := b.y - a.y
	dz := b.z - a.z
	rng := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if math.IsNaN(rng) || math.IsInf(rng, 0) {
		return radarLockGeometry{}
	}
	return radarLockGeometry{
		rangeM:   rng,
		hasRange: true,
		angles:   radarLockAnglesAt(a, dx, dy, dz),
	}
}

// primaryRangeAt is kept as the range-only API used by existing callers and
// tests. Angle failures never affect the validity of a finite range.
func (x *radarLockExport) primaryRangeAt(source uint64, tr *track, targets []uint64, t uint32) (float64, bool) {
	geometry := x.primaryGeometryAt(source, tr, targets, t)
	return geometry.rangeM, geometry.hasRange
}

// radarLockAnglesAt converts the local east/up/north offset (dx, dy, dz) into
// Tacview body coordinates. Yaw is clockwise from north, pitch is positive up,
// and roll is positive with the right wing down. The resulting azimuth is
// positive right and elevation is positive up.
func radarLockAnglesAt(source acmiSample, dx, dy, dz float64) radarLockAngles {
	if !radarLockFinite(dx, dy, dz, source.yaw, source.pitch, source.roll) {
		return radarLockAngles{}
	}
	if dx == 0 && dy == 0 && dz == 0 {
		return radarLockAngles{}
	}

	yaw := source.yaw * math.Pi / 180
	pitch := source.pitch * math.Pi / 180
	roll := source.roll * math.Pi / 180
	sy, cy := math.Sin(yaw), math.Cos(yaw)
	sp, cp := math.Sin(pitch), math.Cos(pitch)
	sr, cr := math.Sin(roll), math.Cos(roll)

	// Aircraft basis in local east/up/north coordinates before roll.
	forwardEast, forwardUp, forwardNorth := sy*cp, sp, cy*cp
	rightEast, rightUp, rightNorth := cy, 0.0, -sy
	upEast := -sy * sp
	upUp := cp
	upNorth := -cy * sp

	// Positive roll lowers the right wing and tilts the body's up axis right.
	bodyRightEast := rightEast*cr - upEast*sr
	bodyRightUp := rightUp*cr - upUp*sr
	bodyRightNorth := rightNorth*cr - upNorth*sr
	bodyUpEast := upEast*cr + rightEast*sr
	bodyUpUp := upUp*cr + rightUp*sr
	bodyUpNorth := upNorth*cr + rightNorth*sr

	forward := dx*forwardEast + dy*forwardUp + dz*forwardNorth
	right := dx*bodyRightEast + dy*bodyRightUp + dz*bodyRightNorth
	up := dx*bodyUpEast + dy*bodyUpUp + dz*bodyUpNorth
	if !radarLockFinite(forward, right, up) {
		return radarLockAngles{}
	}

	angles := radarLockAngles{}
	horizontal := math.Hypot(forward, right)
	norm := math.Hypot(math.Hypot(dx, dy), dz)
	if horizontal > norm*1e-12 {
		angles.azimuthDeg = radarLockDegrees(math.Atan2(right, forward))
		angles.hasAzimuth = true
	}
	// A target directly above or below the nose has a defined elevation even
	// though its azimuth is undefined. Coincident and nonfinite geometry return
	// above without either angle.
	angles.elevationDeg = radarLockDegrees(math.Atan2(up, horizontal))
	angles.hasElevation = true
	return angles
}

func radarLockFinite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func radarLockDegrees(radians float64) float64 {
	degrees := radians * 180 / math.Pi
	if degrees == 0 {
		return 0
	}
	if degrees <= -180+1e-10 {
		return 180
	}
	return degrees
}

func radarLockProperties(prev, next [maxTacviewLockedTargets]string, prevOn, on bool, rng float64, hasRange, prevHasRange bool) []string {
	return radarLockPropertiesWithAngles(prev, next, prevOn, on,
		radarLockGeometry{rangeM: rng, hasRange: hasRange}, prevHasRange)
}

func radarLockPropertiesWithAngles(prev, next [maxTacviewLockedTargets]string, prevOn, on bool, geometry radarLockGeometry, prevHasRange bool) []string {
	props := make([]string, 0, maxTacviewLockedTargets+5)
	for i := range maxTacviewLockedTargets {
		if prev[i] == next[i] {
			continue
		}
		value := next[i]
		if value == "" {
			value = "0"
		}
		props = append(props, radarLockSlotName(i)+"="+value)
	}
	if on {
		if !prevOn {
			props = append(props, "LockedTargetMode=1")
		}
		if geometry.hasRange {
			props = append(props, "LockedTargetRange="+strconv.FormatFloat(geometry.rangeM, 'f', 0, 64))
		} else if prevHasRange {
			// A primary-target change without a usable fresh range must not leave
			// the previous target's distance displayed.
			props = append(props, "LockedTargetRange=0")
		}
		props = appendRadarLockAngleProperties(props, geometry.angles)
	} else if prevOn {
		props = append(props, "LockedTargetMode=0")
		if prevHasRange {
			props = append(props, "LockedTargetRange=0")
		}
		props = appendRadarLockAngleProperties(props, radarLockAngles{})
	}
	return props
}

func appendRadarLockAngleProperties(props []string, angles radarLockAngles) []string {
	azimuth := ""
	if angles.hasAzimuth {
		azimuth = strconv.FormatFloat(angles.azimuthDeg, 'f', 2, 64)
	}
	elevation := ""
	if angles.hasElevation {
		elevation = strconv.FormatFloat(angles.elevationDeg, 'f', 2, 64)
	}
	return append(props,
		"LockedTargetAzimuth="+azimuth,
		"LockedTargetElevation="+elevation,
	)
}

func radarLockSlotName(index int) string {
	if index == 0 {
		return "LockedTarget"
	}
	return "LockedTarget" + strconv.Itoa(index+1)
}

// lineProperties returns the properties for an exact source FM sync. Missing
// target-list records do not create an event and leave the previous lock alone.
func (x *radarLockExport) lineProperties(uid uint64, t uint32, tr *track) []string {
	if x == nil || !x.prepared || tr == nil || tr.key.id != uid || !tr.key.air {
		return nil
	}
	events := x.events[tr.key]
	if events == nil {
		return nil
	}
	event, ok := events[t]
	if !ok || !event.sync {
		return nil
	}
	return event.props
}

func (x *radarLockExport) extraLines() []radarLockExtraLine {
	if x == nil {
		return nil
	}
	return x.extra
}

func (x *radarLockExport) stats() (lockedSyncs, changes int) {
	if x == nil {
		return 0, 0
	}
	return x.lockedSyncs, x.changes
}

// writeRadarLockLog emits one row per lock-state change. The target list is the
// currently valid designation set; the replay does not identify these slots as
// confirmed STT tracks.
func writeRadarLockLog(path string, x *radarLockExport, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string) error {
	if x == nil {
		return nil
	}
	if !x.prepared {
		x.prepare(tracks, acmiObjectIDs(tracks))
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cw := csv.NewWriter(f)
	defer cw.Flush()
	if err := cw.Write([]string{
		"time_sec", "air_id", "air_uid", "air_name", "pilot", "target_count",
		"target_uids", "target_ids", "target_names", "primary_range_m",
	}); err != nil {
		return err
	}

	for _, row := range x.logRows {
		src := x.tracks[row.source]
		airName, pilot := "", ""
		if src != nil {
			airName = trackName(src, slot, names)
			pilot = trackPilot(src, slot)
		}
		targetNames := make([]string, 0, len(row.uids))
		for _, key := range row.uids {
			if tr := x.tracks[key]; tr != nil {
				targetNames = append(targetNames, trackName(tr, slot, names))
			} else {
				targetNames = append(targetNames, "air:"+strconv.FormatUint(key.id, 10))
			}
		}
		targetIDs := make([]string, 0, len(row.uids))
		for i := 0; i < len(row.uids) && i < maxTacviewLockedTargets; i++ {
			targetIDs = append(targetIDs, row.slots[i])
		}
		rng := ""
		if row.hasRng {
			rng = strconv.FormatFloat(row.rangeM, 'f', 0, 64)
		}
		out := []string{
			strconv.FormatFloat(float64(row.t)/1000, 'f', 3, 64),
			x.ids[row.source],
			strconv.FormatUint(row.source.id, 10),
			airName,
			pilot,
			strconv.Itoa(len(row.uids)),
			joinRadarTargetUIDs(row.uids),
			strings.Join(targetIDs, ","),
			strings.Join(targetNames, ","),
			rng,
		}
		if err := cw.Write(out); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func joinRadarUIDs(uids []uint64) string {
	parts := make([]string, 0, len(uids))
	for _, uid := range uids {
		parts = append(parts, strconv.FormatUint(uid, 10))
	}
	return strings.Join(parts, ",")
}

func joinRadarTargetUIDs(targets []trackKey) string {
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		parts = append(parts, strconv.FormatUint(target.id, 10))
	}
	return strings.Join(parts, ",")
}
