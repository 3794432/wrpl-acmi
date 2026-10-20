package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

const (
	countermeasureWeaponTrigger        = 14
	countermeasureACMIType             = "Misc+Decoy"
	countermeasureDisplayName          = "Countermeasure"
	countermeasureEstimatedDisplayName = "Countermeasure (estimated)"
	countermeasureEventText            = "Countermeasure released"
	countermeasureEstimatedEventText   = "Countermeasure released; trajectory estimated"
	countermeasureEstimatedSource      = "EstimatedFromRelease"
	countermeasureVelocityFromTrack    = "ParentTrackFiniteDifference"
	countermeasureVelocityNone         = "None"
)

// Estimated-trajectory model. These constants are deliberately grouped so the
// assumptions are reviewable and easy to replace with real telemetry later.
const (
	// countermeasureEstimateDragK is the exponential drag rate (1/s) applied
	// to the release velocity. A flare/decoy decelerates quickly relative to
	// the aircraft, but the exact coefficient is not present in the replay.
	countermeasureEstimateDragK = 2.5
	// countermeasureEstimateMaxSeconds caps every estimated lifetime.
	countermeasureEstimateMaxSeconds = 3.0
	// countermeasureEstimateStepSeconds is the output sampling interval.
	countermeasureEstimateStepSeconds = 0.2
	// countermeasureEstimateWindowMS is the half-width of the aircraft sample
	// window used for the finite-difference release velocity.
	countermeasureEstimateWindowMS = 1500
	// countermeasureEstimateMaxSpeedMPS guards against a bad interpolation.
	countermeasureEstimateMaxSpeedMPS = 1000.0
	// countermeasureEstimateGravity is standard gravity on the ACMI Y axis
	// (altitude), applied downward.
	countermeasureEstimateGravity = 9.80665
)

// countermeasureTrackInput is the decoded construction and its real sync
// samples. Samples outside the construction lifetime have already been
// removed. A release without sync samples sets Estimated and keeps Events and
// Records; appendCountermeasureTracks then synthesizes a short trajectory.
type countermeasureTrackInput struct {
	ID        uint64
	ParentUID uint64
	HasParent bool
	Samples   []acmiSample
	EndTime   uint32
	HasEnd    bool
	Estimated bool
	Source    *projectiles.Projectile
}

type acmiEvent struct {
	t         uint32
	parentID  uint64
	hasParent bool
	text      string
}

// countermeasureRecord is retained for every trigger-14 construction, even
// when no WeaponSync position exists.
type countermeasureRecord struct {
	EID        uint64
	LifetimeID uint64
	SpawnTime  uint32
	EndTime    uint32
	HasEnd     bool
	OwnerEID   uint64
	ParentUID  uint64
	HasParent  bool
	WeaponRef  uint32
	Template   string
	SpawnX     float64
	SpawnY     float64
	SpawnZ     float64
	SyncCount  int
	Display    string
}

type countermeasureExport struct {
	Tracks        []*track
	Events        []acmiEvent
	Records       []countermeasureRecord
	TracksAdded   int
	EventsAdded   int
	Constructions int
}

// prepareCountermeasures is the single integration point used by main. When
// disabled it returns the original track slice and no new events or records,
// so the legacy writer receives exactly the same input as before.
func prepareCountermeasures(metadata *projectiles.MetadataParser, positions *projectiles.PositionParser, tracks []*track, enabled bool) countermeasureExport {
	out := countermeasureExport{Tracks: tracks}
	if !enabled || metadata == nil || positions == nil {
		return out
	}
	inputs, events, records := decodedCountermeasures(metadata, positions)
	combined, added := appendCountermeasureTracks(tracks, inputs)
	out.Tracks = combined
	out.Events = events
	out.Records = records
	out.TracksAdded = added
	out.EventsAdded = len(events)
	out.Constructions = len(records)
	return out
}

func decodedCountermeasures(metadata *projectiles.MetadataParser, positions *projectiles.PositionParser) ([]countermeasureTrackInput, []acmiEvent, []countermeasureRecord) {
	if metadata == nil || positions == nil {
		return nil, nil, nil
	}
	byEntity := make(map[uint64][]projectiles.Sample)
	for wireID, samples := range positions.Samples {
		if id, ok := projectileEntityID(wireID); ok {
			byEntity[id] = append(byEntity[id], samples...)
		}
	}

	var inputs []countermeasureTrackInput
	var events []acmiEvent
	var records []countermeasureRecord
	for eid, instances := range metadata.History {
		countermeasureInstances := 0
		for _, source := range instances {
			if isCountermeasureProjectile(source) {
				countermeasureInstances++
			}
		}
		for instanceIndex, source := range instances {
			if !isCountermeasureProjectile(source) {
				continue
			}
			lifetimeID := eid
			if countermeasureInstances > 1 {
				// Reconstructed EIDs can be reused. Include the real spawn tick
				// so each construction remains a separate lifetime.
				lifetimeID = uint64(source.SpawnSample.Time)<<32 | eid
			}

			nextSpawnTime, hasNextConstruction := nextConstructionSpawnTime(instances, instanceIndex)
			filtered := filterCountermeasureSamples(byEntity[eid], source, nextSpawnTime, hasNextConstruction)
			syncCount := len(filtered)
			display := "event"
			input := countermeasureTrackInput{
				ID:        lifetimeID,
				ParentUID: source.ParentUID,
				HasParent: source.HasParent,
				EndTime:   source.EndTime,
				HasEnd:    source.HasEnd,
				Source:    source,
			}
			if syncCount > 0 {
				// Real WeaponSync samples always win over any estimate.
				display = "track+event"
				input.Samples = countermeasureTrackSamples(source, filtered)
			} else {
				// The construction exists but carries no position update. The
				// writer will synthesize a clearly marked short trajectory.
				input.Estimated = true
			}
			inputs = append(inputs, input)

			text := countermeasureEventText
			if syncCount == 0 {
				text = countermeasureEstimatedEventText
			}
			events = append(events, acmiEvent{
				t:         source.SpawnSample.Time,
				parentID:  source.ParentUID,
				hasParent: source.HasParent,
				text:      text,
			})
			records = append(records, countermeasureRecord{
				EID:        eid,
				LifetimeID: lifetimeID,
				SpawnTime:  source.SpawnSample.Time,
				EndTime:    source.EndTime,
				HasEnd:     source.HasEnd,
				OwnerEID:   source.OwnerEID,
				ParentUID:  source.ParentUID,
				HasParent:  source.HasParent,
				WeaponRef:  source.WeaponRef,
				Template:   source.Template,
				SpawnX:     source.SpawnSample.X,
				SpawnY:     source.SpawnSample.Y,
				SpawnZ:     source.SpawnSample.Z,
				SyncCount:  syncCount,
				Display:    display,
			})
		}
	}

	sort.Slice(inputs, func(i, j int) bool { return inputs[i].ID < inputs[j].ID })
	sort.Slice(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		if events[i].parentID != events[j].parentID {
			return events[i].parentID < events[j].parentID
		}
		return events[i].text < events[j].text
	})
	sort.Slice(records, func(i, j int) bool {
		if records[i].SpawnTime != records[j].SpawnTime {
			return records[i].SpawnTime < records[j].SpawnTime
		}
		if records[i].EID != records[j].EID {
			return records[i].EID < records[j].EID
		}
		return records[i].LifetimeID < records[j].LifetimeID
	})
	return inputs, events, records
}

func isCountermeasureProjectile(source *projectiles.Projectile) bool {
	return source != nil && source.WeaponRef>>16 == countermeasureWeaponTrigger
}

// nextConstructionSpawnTime returns the spawn tick immediately following one
// history entry. History is in packet construction order. The next lifetime of
// any type, including a missile rebuilt on the same EID, bounds the previous
// window even when the previous source has no destruction record.
func nextConstructionSpawnTime(instances []*projectiles.Projectile, index int) (uint32, bool) {
	if index < 0 || index+1 >= len(instances) || instances[index+1] == nil {
		return 0, false
	}
	return instances[index+1].SpawnSample.Time, true
}

func filterCountermeasureSamples(samples []projectiles.Sample, source *projectiles.Projectile, nextSpawnTime uint32, hasNextConstruction bool) []projectiles.Sample {
	if source == nil {
		return nil
	}
	filtered := make([]projectiles.Sample, 0, len(samples))
	for _, sample := range samples {
		if sample.Time < source.SpawnSample.Time {
			continue
		}
		if source.HasEnd && sample.Time > source.EndTime {
			continue
		}
		if hasNextConstruction && sample.Time >= nextSpawnTime {
			continue
		}
		filtered = append(filtered, sample)
	}
	return filtered
}

// countermeasureTrackSamples keeps the construction's real spawn point and
// subsequent real sync points. It never interpolates between them. Duplicate
// ticks retain the last recorded update, matching WeaponSync ordering.
func countermeasureTrackSamples(source *projectiles.Projectile, syncs []projectiles.Sample) []acmiSample {
	spawn := source.SpawnSample
	samples := []acmiSample{{
		t: spawn.Time, x: spawn.X, y: spawn.Y, z: spawn.Z, projectile: &spawn,
	}}
	for _, sample := range syncs {
		copy := sample
		samples = append(samples, acmiSample{
			t: sample.Time, x: sample.X, y: sample.Y, z: sample.Z, projectile: &copy,
		})
	}
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].t < samples[j].t })
	unique := samples[:0]
	for _, sample := range samples {
		if len(unique) > 0 && unique[len(unique)-1].t == sample.t {
			unique[len(unique)-1] = sample
			continue
		}
		unique = append(unique, sample)
	}
	return unique
}

// appendCountermeasureTracks adds decoys after every existing aircraft, ground
// and missile track. A construction with real WeaponSync samples uses those
// samples unchanged. A construction without real samples receives a short,
// explicitly estimated trajectory derived from the release point and the
// parent aircraft's velocity. Parent links use only an actual aircraft UID.
func appendCountermeasureTracks(tracks []*track, inputs []countermeasureTrackInput) ([]*track, int) {
	out := append([]*track(nil), tracks...)
	existing := make(map[trackKey]bool, len(tracks))
	aircraft := make(map[uint64]*track)
	for _, tr := range tracks {
		if tr == nil {
			continue
		}
		existing[tr.key] = true
		if tr.key.air && !tr.key.missile && !tr.key.decoy {
			if _, ok := aircraft[tr.key.id]; !ok {
				aircraft[tr.key.id] = tr
			}
		}
	}
	// The replay cutoff is fixed before any estimates are appended so every
	// estimate in one export sees the same deterministic world end.
	replayEnd, hasReplayEnd := lastRecordedTrackTime(tracks)

	added := make([]*track, 0, len(inputs))
	for _, input := range inputs {
		key := trackKey{decoy: true, id: input.ID}
		if existing[key] {
			continue
		}

		var parent *track
		if input.HasParent {
			parent = aircraft[input.ParentUID]
		}

		samples := input.Samples
		end, hasEnd := input.EndTime, input.HasEnd
		estimated := input.Estimated
		estimatedVelocity := false
		if estimated {
			samples, estimatedVelocity = estimatedCountermeasureTrack(input.Source, parent, replayEnd, hasReplayEnd)
			if len(samples) == 0 {
				continue
			}
			end, hasEnd = samples[len(samples)-1].t, true
		} else if len(samples) == 0 {
			continue
		}

		rt := &track{
			key:               key,
			name:              countermeasureDisplayName,
			samples:           append([]acmiSample(nil), samples...),
			end:               end,
			hasEnd:            hasEnd,
			projectileSource:  input.Source,
			estimated:         estimated,
			estimatedVelocity: estimatedVelocity,
		}
		rt.parent = parent
		added = append(added, rt)
		existing[key] = true
	}
	sort.Slice(added, func(i, j int) bool { return added[i].key.id < added[j].key.id })
	return append(out, added...), len(added)
}

// lastRecordedTrackTime returns the latest real sample in the existing tracks.
// Estimated decoys are appended after this call, so they cannot extend it.
func lastRecordedTrackTime(tracks []*track) (uint32, bool) {
	var last uint32
	found := false
	for _, tr := range tracks {
		if tr == nil {
			continue
		}
		for _, sample := range tr.samples {
			if !found || sample.t > last {
				last = sample.t
				found = true
			}
		}
	}
	return last, found
}

// estimatedCountermeasureTrack builds a short drag-and-gravity path. It returns
// an empty slice when no usable lifetime or release point exists. The bool is
// true only when the parent aircraft supplied a finite release velocity.
func estimatedCountermeasureTrack(source *projectiles.Projectile, parent *track, replayEnd uint32, hasReplayEnd bool) ([]acmiSample, bool) {
	duration, ok := countermeasureEstimateDuration(source, replayEnd, hasReplayEnd)
	if !ok {
		return nil, false
	}
	spawn := source.SpawnSample
	velocity, usedVelocity := countermeasureReleaseVelocity(parent, spawn.Time)
	samples := countermeasureTrajectorySamples(spawn, velocity, duration)
	if len(samples) == 0 || !finiteCountermeasureSamples(samples) {
		velocity = [3]float64{}
		usedVelocity = false
		samples = countermeasureTrajectorySamples(spawn, velocity, duration)
	}
	if len(samples) == 0 || !finiteCountermeasureSamples(samples) {
		return nil, false
	}
	return samples, usedVelocity
}

// countermeasureEstimateDuration caps the synthetic lifetime at three seconds.
// An explicit EndTime always wins; otherwise the last recorded track sample is
// the replay cutoff. If neither exists the full default duration is used.
func countermeasureEstimateDuration(source *projectiles.Projectile, replayEnd uint32, hasReplayEnd bool) (float64, bool) {
	if source == nil || !finiteCoordinate(source.SpawnSample.X) ||
		!finiteCoordinate(source.SpawnSample.Y) || !finiteCoordinate(source.SpawnSample.Z) {
		return 0, false
	}
	spawn := source.SpawnSample.Time
	duration := countermeasureEstimateMaxSeconds
	if source.HasEnd {
		if source.EndTime <= spawn {
			return 0, false
		}
		duration = math.Min(duration, float64(source.EndTime-spawn)/1000)
	} else if hasReplayEnd {
		if replayEnd <= spawn {
			return 0, false
		}
		duration = math.Min(duration, float64(replayEnd-spawn)/1000)
	}
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, false
	}
	return duration, true
}

// countermeasureReleaseVelocity estimates the aircraft velocity at release with
// a finite difference over samples within +/-1.5 s. It prefers the nearest
// sample on each side of the release and falls back to the first/last sample in
// the window. Missing or degenerate samples return zero velocity.
func countermeasureReleaseVelocity(parent *track, spawnTime uint32) ([3]float64, bool) {
	var zero [3]float64
	if parent == nil || len(parent.samples) < 2 {
		return zero, false
	}
	low := int64(spawnTime) - countermeasureEstimateWindowMS
	high := int64(spawnTime) + countermeasureEstimateWindowMS
	window := make([]acmiSample, 0, 4)
	for _, sample := range parent.samples {
		if int64(sample.t) < low || int64(sample.t) > high {
			continue
		}
		window = append(window, sample)
	}
	if len(window) < 2 {
		return zero, false
	}
	sort.Slice(window, func(i, j int) bool { return window[i].t < window[j].t })

	first, last := 0, len(window)-1
	exact := -1
	for i := range window {
		if window[i].t == spawnTime {
			exact = i
			break
		}
	}
	if exact >= 0 {
		// A release can land on an aircraft sample. Pair that exact sample
		// with the nearest sample at a different time; on a tie, prefer the
		// earlier sample so a turning aircraft is estimated locally.
		previous, next := -1, -1
		for i := exact - 1; i >= 0; i-- {
			if window[i].t != spawnTime {
				previous = i
				break
			}
		}
		for i := exact + 1; i < len(window); i++ {
			if window[i].t != spawnTime {
				next = i
				break
			}
		}
		neighbor := -1
		switch {
		case previous >= 0 && next >= 0:
			previousGap := int64(spawnTime) - int64(window[previous].t)
			nextGap := int64(window[next].t) - int64(spawnTime)
			if previousGap <= nextGap {
				neighbor = previous
			} else {
				neighbor = next
			}
		case previous >= 0:
			neighbor = previous
		case next >= 0:
			neighbor = next
		}
		if neighbor < 0 {
			return zero, false
		}
		if neighbor < exact {
			first, last = neighbor, exact
		} else {
			first, last = exact, neighbor
		}
	} else {
		before, after := -1, -1
		for i := range window {
			if window[i].t < spawnTime {
				before = i
			}
			if window[i].t > spawnTime {
				after = i
				break
			}
		}
		if before >= 0 && after >= 0 {
			first, last = before, after
		}
	}
	a, b := window[first], window[last]
	if b.t <= a.t {
		return zero, false
	}
	dt := float64(int64(b.t)-int64(a.t)) / 1000
	velocity := [3]float64{
		(b.x - a.x) / dt,
		(b.y - a.y) / dt,
		(b.z - a.z) / dt,
	}
	if !finiteCoordinate(velocity[0]) || !finiteCoordinate(velocity[1]) || !finiteCoordinate(velocity[2]) {
		return zero, false
	}
	speed := math.Sqrt(velocity[0]*velocity[0] + velocity[1]*velocity[1] + velocity[2]*velocity[2])
	if speed > countermeasureEstimateMaxSpeedMPS {
		scale := countermeasureEstimateMaxSpeedMPS / speed
		velocity[0] *= scale
		velocity[1] *= scale
		velocity[2] *= scale
	}
	return velocity, true
}

// countermeasureTrajectorySamples integrates the estimate at a fixed 0.2 s step
// and includes the exact final time when the duration is not a multiple of the
// step. Y is altitude, so gravity is subtracted.
func countermeasureTrajectorySamples(spawn projectiles.Sample, velocity [3]float64, duration float64) []acmiSample {
	k := countermeasureEstimateDragK
	steps := int(duration/countermeasureEstimateStepSeconds + 1e-9)
	times := make([]float64, 0, steps+2)
	for i := 0; i <= steps; i++ {
		times = append(times, float64(i)*countermeasureEstimateStepSeconds)
	}
	if duration-float64(steps)*countermeasureEstimateStepSeconds > 1e-6 {
		times = append(times, duration)
	}

	samples := make([]acmiSample, 0, len(times))
	for _, t := range times {
		decay := (1 - math.Exp(-k*t)) / k
		x := spawn.X + velocity[0]*decay
		y := spawn.Y + velocity[1]*decay - 0.5*countermeasureEstimateGravity*t*t
		z := spawn.Z + velocity[2]*decay
		if !finiteCoordinate(x) || !finiteCoordinate(y) || !finiteCoordinate(z) {
			return nil
		}
		ts := uint32(int64(spawn.Time) + int64(math.Round(t*1000)))
		if len(samples) > 0 && samples[len(samples)-1].t == ts {
			continue
		}
		samples = append(samples, acmiSample{t: ts, x: x, y: y, z: z})
	}
	return samples
}

func finiteCountermeasureSamples(samples []acmiSample) bool {
	for _, sample := range samples {
		if !finiteCoordinate(sample.x) || !finiteCoordinate(sample.y) || !finiteCoordinate(sample.z) {
			return false
		}
	}
	return true
}

func finiteCoordinate(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// countermeasureEstimatedProperties marks the object as an estimate and records
// whether the release velocity came from the parent track or was unavailable.
func countermeasureEstimatedProperties(tr *track) []string {
	velocitySource := countermeasureVelocityNone
	if tr != nil && tr.estimatedVelocity {
		velocitySource = countermeasureVelocityFromTrack
	}
	return []string{
		"WT_TrajectorySource=" + countermeasureEstimatedSource,
		"WT_Estimated=1",
		"WT_VelocitySource=" + velocitySource,
	}
}

func decoyTrackEnd(tr *track) (uint32, bool) {
	if tr == nil || !tr.key.decoy || len(tr.samples) == 0 {
		return 0, false
	}
	if tr.hasEnd {
		return tr.end, true
	}
	// The last observation is the replay cutoff for a decoy without an ECS
	// destruction record. It does not synthesize a lifetime.
	return tr.samples[len(tr.samples)-1].t, true
}

func countermeasureType(*projectiles.Projectile) string {
	return countermeasureACMIType
}

func writeCountermeasureLog(path string, records []countermeasureRecord) error {
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
	w := csv.NewWriter(f)
	defer w.Flush()
	header := []string{
		"eid", "eid_hex", "lifetime_id", "spawn_ms", "end_ms", "has_end",
		"owner_eid", "parent_uid", "has_parent", "weapon_ref", "weapon_ref_hex",
		"template", "spawn_x", "spawn_y", "spawn_z", "sync_samples", "display",
	}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, record := range records {
		end := ""
		if record.HasEnd {
			end = strconv.FormatUint(uint64(record.EndTime), 10)
		}
		row := []string{
			strconv.FormatUint(record.EID, 10),
			fmt.Sprintf("%X", record.EID),
			strconv.FormatUint(record.LifetimeID, 10),
			strconv.FormatUint(uint64(record.SpawnTime), 10),
			end,
			strconv.FormatBool(record.HasEnd),
			strconv.FormatUint(record.OwnerEID, 10),
			strconv.FormatUint(record.ParentUID, 10),
			strconv.FormatBool(record.HasParent),
			strconv.FormatUint(uint64(record.WeaponRef), 10),
			fmt.Sprintf("%X", record.WeaponRef),
			record.Template,
			strconv.FormatFloat(record.SpawnX, 'g', -1, 64),
			strconv.FormatFloat(record.SpawnY, 'g', -1, 64),
			strconv.FormatFloat(record.SpawnZ, 'g', -1, 64),
			strconv.Itoa(record.SyncCount),
			record.Display,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func acmiEventLine(event acmiEvent, ids map[trackKey]string) string {
	text := strings.NewReplacer("\r", " ", "\n", " ").Replace(event.text)
	if event.hasParent {
		if parentID := ids[trackKey{air: true, id: event.parentID}]; parentID != "" {
			return fmt.Sprintf("0,Event=Message|%s|%s", parentID, text)
		}
	}
	return "0,Event=Message|" + text
}
