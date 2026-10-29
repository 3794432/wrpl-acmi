package main

import (
	"math"
	"sort"
	"strconv"
)

const (
	bombEstimateGravity     = 9.80665
	bombEstimateStepMS      = uint32(200)
	bombEstimateMaxMS       = uint64(180000)
	bombEstimateVelocityGap = uint32(1500)
)

type bombTrajectoryEstimate struct {
	velocity  [3]float64
	endReason string
}

// Only release-only markers are eligible. A missing parent or release velocity
// leaves the marker unchanged; zero velocity is never invented as a fallback.
func applyBombTrajectoryEstimates(tracks []*track) int {
	var replayEnd uint32
	for _, tr := range tracks {
		if tr == nil || tr.estimated || tr.bombEstimate != nil {
			continue
		}
		for _, s := range tr.samples {
			if s.t > replayEnd {
				replayEnd = s.t
			}
		}
		if tr.projectileSource != nil && tr.projectileSource.HasEnd && tr.projectileSource.EndTime > replayEnd {
			replayEnd = tr.projectileSource.EndTime
		}
	}
	count := 0
	for _, tr := range tracks {
		if tr == nil || !tr.key.bomb || !tr.bombPositionOnly || tr.projectileSource == nil || !isBombProjectile(tr.projectileSource) {
			continue
		}
		spawn := tr.projectileSource.SpawnSample
		releaseAltitude := spawn.Y + *alt0
		if !finiteGroundPosition(spawn.X, spawn.Y, spawn.Z) || math.IsNaN(releaseAltitude) || math.IsInf(releaseAltitude, 0) || releaseAltitude <= 0 {
			continue
		}
		velocity, ok := bombReleaseVelocity(tr.parent, spawn.Time)
		if !ok {
			continue
		}
		limit := uint64(spawn.Time) + bombEstimateMaxMS
		reason := "DurationLimit"
		if limit > math.MaxUint32 {
			limit = math.MaxUint32
		}
		if tr.hasEnd && uint64(tr.end) <= limit {
			limit, reason = uint64(tr.end), "RecordedLifetimeCutoff"
		}
		if uint64(replayEnd) < limit {
			limit, reason = uint64(replayEnd), "ReplayEnd"
		}
		// With no required terrain installation, sea level is only a visual
		// cutoff to prevent an estimate falling through the globe. It is not
		// an inferred terrain impact and does not alter the source end record.
		root := math.Sqrt(velocity[1]*velocity[1] + 2*bombEstimateGravity*releaseAltitude)
		seaSeconds := (velocity[1] + root) / bombEstimateGravity
		if velocity[1] < 0 {
			seaSeconds = 2 * releaseAltitude / (root - velocity[1])
		}
		if seaSeconds*1000 <= float64(limit)-float64(spawn.Time) {
			limit = uint64(spawn.Time) + uint64(math.Floor(seaSeconds*1000))
			reason = "SeaLevelVisualCutoff"
		}
		if limit <= uint64(spawn.Time) {
			continue
		}
		end := uint32(limit)
		samples := make([]acmiSample, 0, int((end-spawn.Time)/bombEstimateStepMS)+2)
		for stamp := uint64(spawn.Time); ; {
			t := float64(stamp-uint64(spawn.Time)) / 1000
			s := acmiSample{t: uint32(stamp), x: spawn.X + velocity[0]*t,
				y: spawn.Y + velocity[1]*t - 0.5*bombEstimateGravity*t*t,
				z: spawn.Z + velocity[2]*t}
			if !finiteGroundPosition(s.x, s.y, s.z) {
				samples = nil
				break
			}
			samples = append(samples, s)
			if stamp == limit {
				break
			}
			stamp += uint64(bombEstimateStepMS)
			if stamp > limit {
				stamp = limit
			}
		}
		if len(samples) < 2 {
			continue
		}
		tr.samples, tr.end, tr.hasEnd = samples, end, true
		tr.bombPositionOnly = false
		tr.bombEstimate = &bombTrajectoryEstimate{velocity: velocity, endReason: reason}
		count++
	}
	return count
}

func bombReleaseVelocity(parent *track, stamp uint32) ([3]float64, bool) {
	var zero [3]float64
	if parent == nil || !isKillAircraft(parent) || len(parent.samples) < 2 || !killTrackContains(parent, stamp) {
		return zero, false
	}
	samples := parent.samples
	i := sort.Search(len(samples), func(i int) bool { return samples[i].t >= stamp })
	a, b := i-1, i
	if i < len(samples) && samples[i].t == stamp {
		if i+1 < len(samples) && samples[i+1].t == stamp {
			return zero, false
		}
		if i == 0 {
			a, b = 0, 1
		}
	} else if i == len(samples) {
		a, b = len(samples)-2, len(samples)-1
	}
	if a < 0 || b >= len(samples) || samples[b].t <= samples[a].t || samples[b].t-samples[a].t > bombEstimateVelocityGap {
		return zero, false
	}
	for _, s := range []acmiSample{samples[a], samples[b]} {
		if !finiteGroundPosition(s.x, s.y, s.z) || missileNameTimeDelta(s.t, stamp) > bombEstimateVelocityGap || (parent.hasEnd && s.t > parent.end) {
			return zero, false
		}
	}
	dt := float64(samples[b].t-samples[a].t) / 1000
	v := [3]float64{(samples[b].x - samples[a].x) / dt, (samples[b].y - samples[a].y) / dt, (samples[b].z - samples[a].z) / dt}
	if !flightFiniteVector(v) || flightVectorMagnitude(v) > aircraftTelemetryMaxSpeedMPS {
		return zero, false
	}
	return v, true
}

func bombEstimatedProperties(tr *track) []string {
	if tr.bombEstimate == nil {
		return nil
	}
	props := []string{"WT_BombPositionSource=EstimatedBallistic", "WT_BombTrajectoryAvailable=0",
		"WT_BombTrajectoryEstimated=1", "WT_BombEstimateModel=InitialVelocityPlusGravity",
		"WT_BombVelocitySource=ParentTrackFiniteDifference", "WT_BombEstimateGravity=9.80665",
		"WT_BombEstimateEndReason=" + tr.bombEstimate.endReason, "Label=Estimated ballistic trajectory"}
	for i, axis := range []string{"X", "Y", "Z"} {
		props = append(props, "WT_BombInitialVelocity"+axis+"="+strconv.FormatFloat(tr.bombEstimate.velocity[i], 'f', 6, 64))
	}
	if tr.projectileSource.HasEnd {
		props = append(props, "WT_BombRecordedEndTimeMS="+strconv.FormatUint(uint64(tr.projectileSource.EndTime), 10))
	}
	return props
}
