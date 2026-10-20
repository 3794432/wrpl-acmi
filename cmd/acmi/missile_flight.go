package main

import "math"

// missileFlightSample contains path-derived kinematics for exactly one input
// sample. Velocity and acceleration use local replay coordinates (m/s and
// m/s^2). HasDirection means Course is available from horizontal travel;
// HasFlightPath means FlightPath is available from vertical travel. FlightPath
// is the velocity elevation above the horizontal plane, not missile attitude.
type missileFlightSample struct {
	Valid           bool
	Velocity        [3]float64
	Speed           float64
	GroundSpeed     float64
	ClimbRate       float64
	HasDirection    bool
	HasFlightPath   bool
	Course          float64
	FlightPath      float64
	HasAcceleration bool
	Acceleration    [3]float64
	AccelMagnitude  float64
	AccelG          float64
	NormalG         float64
	HasTurnRate     bool
	TurnRate        float64
}

const (
	// Recorded position gaps longer than two seconds are left missing. Some
	// missiles are sampled less frequently in real replays; extrapolating over
	// those gaps would create a plausible-looking but unsupported trajectory.
	flightMaxSampleGapSeconds = 2.0

	// This is above the speed envelope of the replay's guided weapons and is
	// used to reject coordinate seams and implausible position jumps.
	flightMaxSpeedMPS = 6000.0

	flightGravity = 9.80665
	flightEpsilon = 1e-9
)

type flightPositionInterval struct {
	delta [3]float64
	dt    float64
	valid bool
}

// enrichMissileFlight derives velocity, path direction, acceleration, and
// course turn rate from one missile lifetime. It never sorts or mutates the
// input, and the returned slice has a one-to-one correspondence with samples.
// Callers must pass only samples from a single lifetime; a missing-data gap is
// never joined to another lifetime.
func enrichMissileFlight(samples []acmiSample) []missileFlightSample {
	result := make([]missileFlightSample, len(samples))
	if len(samples) < 2 {
		return result
	}

	duplicateTime := flightDuplicateTimes(samples)
	intervals := make([]flightPositionInterval, len(samples)-1)
	for i := range intervals {
		intervals[i] = flightPositionDelta(samples[i], samples[i+1], duplicateTime)
	}

	for i, sample := range samples {
		if !flightFinitePosition(sample) || duplicateTime[sample.t] {
			continue
		}
		velocity, computed := flightVelocityAt(samples, i, intervals, duplicateTime)
		if !computed || !flightFiniteVector(velocity) {
			continue
		}

		flight := &result[i]
		flight.Valid = true
		flight.Velocity = velocity
		flight.Speed = flightVectorMagnitude(velocity)
		flight.GroundSpeed = math.Hypot(velocity[0], velocity[2])
		flight.ClimbRate = velocity[1]
		if flight.Speed > flightEpsilon {
			flight.HasFlightPath = true
			if flight.GroundSpeed > flightEpsilon {
				flight.HasDirection = true
				flight.Course = flightBearing(velocity[0], velocity[2])
				flight.FlightPath = math.Atan2(velocity[1], flight.GroundSpeed) * 180 / math.Pi
			} else {
				flight.FlightPath = math.Copysign(90, velocity[1])
			}
		}
	}

	flightDeriveAcceleration(samples, intervals, result, duplicateTime)
	flightDeriveTurnRate(samples, intervals, result, duplicateTime)
	return result
}

func flightDuplicateTimes(samples []acmiSample) map[uint32]bool {
	counts := make(map[uint32]int, len(samples))
	for _, sample := range samples {
		counts[sample.t]++
	}
	duplicates := make(map[uint32]bool)
	for stamp, count := range counts {
		if count > 1 {
			duplicates[stamp] = true
		}
	}
	return duplicates
}

func flightPositionDelta(from, to acmiSample, duplicateTime map[uint32]bool) flightPositionInterval {
	dt, ok := flightTimeDelta(from, to, duplicateTime)
	if !ok || !flightFinitePosition(from) || !flightFinitePosition(to) {
		return flightPositionInterval{}
	}
	delta := flightSubtract(to, from)
	if !flightFiniteVector(delta) || flightVectorMagnitude(delta)/dt > flightMaxSpeedMPS {
		return flightPositionInterval{}
	}
	return flightPositionInterval{delta: delta, dt: dt, valid: true}
}

func flightTimeDelta(from, to acmiSample, duplicateTime map[uint32]bool) (float64, bool) {
	if duplicateTime[from.t] || duplicateTime[to.t] || to.t <= from.t {
		return 0, false
	}
	dt := float64(to.t-from.t) / 1000
	if dt > flightMaxSampleGapSeconds || dt <= 0 {
		return 0, false
	}
	return dt, true
}

func flightDifferentiationTime(samples []acmiSample, first, last int, duplicateTime map[uint32]bool) (float64, bool) {
	if first < 0 || last <= first || last >= len(samples) {
		return 0, false
	}
	for i := first; i < last; i++ {
		if _, ok := flightTimeDelta(samples[i], samples[i+1], duplicateTime); !ok {
			return 0, false
		}
	}
	return float64(samples[last].t-samples[first].t) / 1000, true
}

// flightThreePointWindow selects a local triple containing index i. The
// central window is preferred, then a forward or backward one-sided window.
// Every interval must independently pass the finite, dt, and speed guards.
func flightThreePointWindow(i int, intervals []flightPositionInterval) ([3]int, bool) {
	candidates := [][3]int{
		{i - 1, i, i + 1},
		{i, i + 1, i + 2},
		{i - 2, i - 1, i},
	}
	for _, candidate := range candidates {
		if candidate[0] < 0 || candidate[2] >= len(intervals)+1 {
			continue
		}
		if intervals[candidate[0]].valid && intervals[candidate[0]+1].valid {
			// Near-coincident packet timestamps make quadratic derivatives ill
			// conditioned. Prefer a nearby balanced triple or a longer secant.
			a, b := intervals[candidate[0]].dt, intervals[candidate[0]+1].dt
			if math.Max(a, b)/math.Min(a, b) > 8 {
				continue
			}
			return candidate, true
		}
	}
	return [3]int{}, false
}

func flightVelocityAt(samples []acmiSample, i int, intervals []flightPositionInterval, duplicateTime map[uint32]bool) ([3]float64, bool) {
	if window, ok := flightThreePointWindow(i, intervals); ok {
		if velocity, computed := flightLagrangeVelocity(samples, i, window, duplicateTime); computed {
			return velocity, true
		}
	}
	if i > 0 && intervals[i-1].valid {
		interval := intervals[i-1]
		if i < len(intervals) && intervals[i].valid && intervals[i].dt > interval.dt {
			interval = intervals[i]
		}
		return flightScale(interval.delta, 1/interval.dt), true
	}
	if i < len(intervals) && intervals[i].valid {
		interval := intervals[i]
		return flightScale(interval.delta, 1/interval.dt), true
	}
	return [3]float64{}, false
}

func flightLagrangeVelocity(samples []acmiSample, target int, window [3]int, duplicateTime map[uint32]bool) ([3]float64, bool) {
	if _, ok := flightDifferentiationTime(samples, window[0], window[2], duplicateTime); !ok {
		return [3]float64{}, false
	}
	coefficients, ok := flightLagrangeDerivativeCoefficients(samples, target, window)
	if !ok {
		return [3]float64{}, false
	}
	velocity := [3]float64{}
	for i, index := range window {
		point := [3]float64{samples[index].x, samples[index].y, samples[index].z}
		velocity = flightAdd(velocity, flightScale(point, coefficients[i]))
	}
	if !flightFiniteVector(velocity) {
		return [3]float64{}, false
	}
	return velocity, true
}

func flightLagrangeDerivativeCoefficients(samples []acmiSample, target int, window [3]int) ([3]float64, bool) {
	targetTime := int64(samples[target].t)
	var times [3]float64
	for i, index := range window {
		times[i] = float64(int64(samples[index].t)-targetTime) / 1000
	}
	if times[0] >= times[1] || times[1] >= times[2] {
		return [3]float64{}, false
	}
	denominator0 := (times[0] - times[1]) * (times[0] - times[2])
	denominator1 := (times[1] - times[0]) * (times[1] - times[2])
	denominator2 := (times[2] - times[0]) * (times[2] - times[1])
	if denominator0 == 0 || denominator1 == 0 || denominator2 == 0 {
		return [3]float64{}, false
	}
	// The target is at relative time zero.
	coefficients := [3]float64{
		(-times[1] - times[2]) / denominator0,
		(-times[0] - times[2]) / denominator1,
		(-times[0] - times[1]) / denominator2,
	}
	for _, coefficient := range coefficients {
		if math.IsNaN(coefficient) || math.IsInf(coefficient, 0) {
			return [3]float64{}, false
		}
	}
	return coefficients, true
}

func flightDeriveAcceleration(samples []acmiSample, intervals []flightPositionInterval, result []missileFlightSample, duplicateTime map[uint32]bool) {
	for i := range result {
		if !result[i].Valid {
			continue
		}
		window, ok := flightThreePointWindow(i, intervals)
		if !ok {
			continue
		}
		first := intervals[window[0]]
		second := intervals[window[1]]
		h0 := first.dt
		h1 := second.dt
		slope0 := flightScale(first.delta, 1/h0)
		slope1 := flightScale(second.delta, 1/h1)
		acceleration := flightScale(flightVectorDifference(slope1, slope0), 2/(h0+h1))
		if !flightFiniteVector(acceleration) {
			continue
		}

		flight := &result[i]
		flight.Acceleration = acceleration
		flight.AccelMagnitude = flightVectorMagnitude(acceleration)
		flight.AccelG = flight.AccelMagnitude / flightGravity
		flight.HasAcceleration = true
		if flight.Speed > flightEpsilon {
			unit := flightScale(flight.Velocity, 1/flight.Speed)
			axial := flightDot(acceleration, unit)
			normal := [3]float64{
				acceleration[0] - axial*unit[0],
				acceleration[1] - axial*unit[1],
				acceleration[2] - axial*unit[2],
			}
			flight.NormalG = flightVectorMagnitude(normal) / flightGravity
		}
	}
}

func flightDeriveTurnRate(samples []acmiSample, intervals []flightPositionInterval, result []missileFlightSample, duplicateTime map[uint32]bool) {
	for i := range result {
		if !result[i].HasDirection {
			continue
		}
		if window, ok := flightThreePointWindow(i, intervals); ok &&
			result[window[0]].HasDirection && result[window[1]].HasDirection && result[window[2]].HasDirection {
			coefficients, ok := flightLagrangeDerivativeCoefficients(samples, i, window)
			if !ok {
				continue
			}
			courses := [3]float64{}
			for j, index := range window {
				courses[j] = result[i].Course + flightSignedAngle(result[index].Course-result[i].Course)
			}
			rate := coefficients[0]*courses[0] + coefficients[1]*courses[1] + coefficients[2]*courses[2]
			if !math.IsNaN(rate) && !math.IsInf(rate, 0) {
				result[i].TurnRate = rate
				result[i].HasTurnRate = true
			}
			continue
		}

		var first, last int
		switch {
		case i == 0:
			first, last = 0, 1
		case i == len(result)-1:
			first, last = i-1, i
		default:
			first, last = i-1, i+1
		}
		if first < 0 || last >= len(result) || !result[first].HasDirection || !result[last].HasDirection {
			continue
		}
		dt, ok := flightDifferentiationTime(samples, first, last, duplicateTime)
		if !ok || dt <= flightEpsilon {
			continue
		}
		rate := flightSignedAngle(result[last].Course-result[first].Course) / dt
		if math.IsNaN(rate) || math.IsInf(rate, 0) {
			continue
		}
		result[i].TurnRate = rate
		result[i].HasTurnRate = true
	}
}

func flightFinitePosition(sample acmiSample) bool {
	// Constructor time and the first synchronization time can refer to
	// different simulation ticks. Keep the spawn point but do not differentiate it.
	if sample.projectile != nil && !sample.projectile.FromSync {
		return false
	}
	return !math.IsNaN(sample.x) && !math.IsInf(sample.x, 0) &&
		!math.IsNaN(sample.y) && !math.IsInf(sample.y, 0) &&
		!math.IsNaN(sample.z) && !math.IsInf(sample.z, 0)
}

func flightFiniteVector(value [3]float64) bool {
	for _, component := range value {
		if math.IsNaN(component) || math.IsInf(component, 0) {
			return false
		}
	}
	return true
}

func flightSubtract(to, from acmiSample) [3]float64 {
	return [3]float64{to.x - from.x, to.y - from.y, to.z - from.z}
}

func flightVectorDifference(to, from [3]float64) [3]float64 {
	return [3]float64{to[0] - from[0], to[1] - from[1], to[2] - from[2]}
}

func flightAdd(a, b [3]float64) [3]float64 {
	return [3]float64{a[0] + b[0], a[1] + b[1], a[2] + b[2]}
}

func flightScale(value [3]float64, scale float64) [3]float64 {
	return [3]float64{value[0] * scale, value[1] * scale, value[2] * scale}
}

func flightDot(a, b [3]float64) float64 {
	return a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
}

func flightVectorMagnitude(value [3]float64) float64 {
	return math.Hypot(math.Hypot(value[0], value[1]), value[2])
}

func flightBearing(east, north float64) float64 {
	bearing := math.Atan2(east, north) * 180 / math.Pi
	if bearing < 0 {
		bearing += 360
	}
	if bearing >= 360 {
		bearing -= 360
	}
	if bearing == 0 {
		return 0
	}
	return bearing
}

func flightSignedAngle(degrees float64) float64 {
	for degrees > 180 {
		degrees -= 360
	}
	for degrees <= -180 {
		degrees += 360
	}
	return degrees
}
