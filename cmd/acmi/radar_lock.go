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
	source uint64
	slots  [maxTacviewLockedTargets]string
	uids   []uint64
	rangeM float64
	hasRng bool
}

// radarLockExport maps kind-6 FM target designations to Tacview LockedTarget
// properties. All writer-visible state is rebuilt by prepare, so exporting the
// same replay twice cannot leak slot state from the first write.
type radarLockExport struct {
	states map[uint64][]radarLockTargetState // aircraft uid -> sorted complete syncs

	ids    map[uint64]string
	tracks map[uint64]*track

	events  map[uint64]map[uint32]radarLockEvent
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
			if entry == nil || entry.Data == nil || entry.Data.TargetsData == nil {
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
	x.ids = map[uint64]string{}
	x.tracks = map[uint64]*track{}
	for _, tr := range tracks {
		if tr == nil || !tr.key.air {
			continue
		}
		id := ids[tr.key]
		if id == "" {
			continue
		}
		x.ids[tr.key.id] = id
		x.tracks[tr.key.id] = tr
	}

	x.events = map[uint64]map[uint32]radarLockEvent{}
	x.extra = nil
	x.logRows = nil
	x.aircraft = 0
	x.lockedSyncs = 0
	x.changes = 0
	x.prepared = true

	endTimes := map[uint64][]uint32{}
	for uid, tr := range x.tracks {
		if tr.hasEnd {
			endTimes[uid] = append(endTimes[uid], tr.end)
		}
	}
	sources := make([]uint64, 0, len(x.states))
	for uid := range x.states {
		if _, ok := x.ids[uid]; ok {
			sources = append(sources, uid)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	for _, uid := range sources {
		x.prepareSource(uid, endTimes)
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
		return x.logRows[i].source < x.logRows[j].source
	})
}

func (x *radarLockExport) prepareSource(source uint64, endTimes map[uint64][]uint32) {
	tr := x.tracks[source]
	if tr == nil {
		return
	}
	states := x.states[source]
	if len(states) == 0 {
		return
	}

	syncAt := make(map[uint32]radarLockTargetState, len(states))
	designated := map[uint64]bool{}
	eventTimes := map[uint32]bool{}
	for _, state := range states {
		syncAt[state.t] = state
		eventTimes[state.t] = true
		for _, target := range state.uids {
			designated[target] = true
		}
	}
	for target := range designated {
		for _, t := range endTimes[target] {
			eventTimes[t] = true
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
		if tr.hasEnd && t >= tr.end {
			break
		}
		isSync := false
		if state, ok := syncAt[t]; ok {
			current = state.uids
			isSync = true
		}
		slots, targets := x.resolveSlotsAt(source, current, t)
		on := len(targets) > 0
		rng, hasRange := x.primaryRangeAt(source, tr, targets, t)
		changed := slots != prev || on != prevOn
		props := radarLockProperties(prev, slots, prevOn, on, rng, hasRange, prevHasRng)
		if len(props) > 0 {
			if isSync {
				events := x.events[source]
				if events == nil {
					events = map[uint32]radarLockEvent{}
					x.events[source] = events
				}
				events[t] = radarLockEvent{t: t, sync: true, props: props}
			} else if changed {
				x.extra = append(x.extra, radarLockExtraLine{
					t:    t,
					line: x.ids[source] + "," + strings.Join(props, ","),
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
				source: source,
				slots:  slots,
				uids:   append([]uint64(nil), targets...),
				rangeM: rng,
				hasRng: hasRange,
			})
		}
		prev = slots
		prevOn = on
		prevHasRng = hasRange
	}
	if everLock {
		x.aircraft++
	}
}

// resolveSlotsAt applies designation order, deduplicates both unit ids and ACMI
// object ids, drops unknown/dead/not-yet-born/stale targets, and caps the list at
// Tacview's nine slots. The returned uids use the same order as slots.
func (x *radarLockExport) resolveSlotsAt(source uint64, uids []uint64, t uint32) ([maxTacviewLockedTargets]string, []uint64) {
	var slots [maxTacviewLockedTargets]string
	resolved := make([]uint64, 0, min(len(uids), maxTacviewLockedTargets))
	seenUID := map[uint64]bool{}
	seenID := map[string]bool{}
	for _, uid := range uids {
		if len(resolved) >= maxTacviewLockedTargets {
			break
		}
		if uid == source || seenUID[uid] || !x.targetAvailableAt(uid, t) {
			continue
		}
		id := x.ids[uid]
		if id == "" || seenID[id] {
			continue
		}
		seenUID[uid] = true
		seenID[id] = true
		slots[len(resolved)] = id
		resolved = append(resolved, uid)
	}
	return slots, resolved
}

// targetAvailableAt requires a track that exists at t and a real position sample
// at or before t. A destroyed aircraft is unavailable at its deletion timestamp.
func (x *radarLockExport) targetAvailableAt(uid uint64, t uint32) bool {
	tr := x.tracks[uid]
	if tr == nil || len(tr.samples) == 0 {
		return false
	}
	if tr.hasEnd && t >= tr.end {
		return false
	}
	if t < tr.samples[0].t {
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

// primaryRangeAt computes slant range only when both the source and its primary
// target have usable positions at or before t. It never interpolates from a
// future sample.
func (x *radarLockExport) primaryRangeAt(source uint64, tr *track, targets []uint64, t uint32) (float64, bool) {
	if len(targets) == 0 || tr == nil {
		return 0, false
	}
	if tr.hasEnd && t >= tr.end {
		return 0, false
	}
	target := targets[0]
	if !x.targetAvailableAt(target, t) {
		return 0, false
	}
	dst := x.tracks[target]
	if dst == nil {
		return 0, false
	}
	a, ok := sampleAtOrBefore(tr, t, radarLockPositionMaxAgeMs)
	if !ok {
		return 0, false
	}
	b, ok := sampleAtOrBefore(dst, t, radarLockPositionMaxAgeMs)
	if !ok {
		return 0, false
	}
	dx := b.x - a.x
	dy := b.y - a.y
	dz := b.z - a.z
	rng := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if math.IsNaN(rng) || math.IsInf(rng, 0) {
		return 0, false
	}
	return rng, true
}

func radarLockProperties(prev, next [maxTacviewLockedTargets]string, prevOn, on bool, rng float64, hasRange, prevHasRange bool) []string {
	props := make([]string, 0, maxTacviewLockedTargets+3)
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
		if hasRange {
			props = append(props, "LockedTargetRange="+strconv.FormatFloat(rng, 'f', 0, 64))
		} else if prevHasRange {
			// A primary-target change without a usable fresh range must not leave
			// the previous target's distance displayed.
			props = append(props, "LockedTargetRange=0")
		}
	} else if prevOn {
		props = append(props, "LockedTargetMode=0")
		if prevHasRange {
			props = append(props, "LockedTargetRange=0")
		}
	}
	return props
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
	if x == nil || !x.prepared || tr == nil || !tr.key.air {
		return nil
	}
	events := x.events[uid]
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

	byUID := map[uint64]*track{}
	for _, tr := range tracks {
		if tr != nil && tr.key.air {
			byUID[tr.key.id] = tr
		}
	}
	for _, row := range x.logRows {
		src := byUID[row.source]
		airName, pilot := "", ""
		if src != nil {
			airName = trackName(src, slot, names)
			pilot = trackPilot(src, slot)
		}
		targetNames := make([]string, 0, len(row.uids))
		for _, uid := range row.uids {
			if tr := byUID[uid]; tr != nil {
				targetNames = append(targetNames, trackName(tr, slot, names))
			} else {
				targetNames = append(targetNames, "air:"+strconv.FormatUint(uid, 10))
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
			strconv.FormatUint(row.source, 10),
			airName,
			pilot,
			strconv.Itoa(len(row.uids)),
			joinRadarUIDs(row.uids),
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
