package main

import "math"

// Recorded arrival times have jitter. Fit a local quadratic over 0.4-1.2 s
// rather than amplifying it with adjacent three-point differences. No recorded
// positions or timestamps are changed, and missing intervals are not crossed.
func enrichRecordedMissileFlight(samples []acmiSample) []missileFlightSample {
	result := enrichMissileFlight(samples)
	duplicates := flightDuplicateTimes(samples)
	intervals := make([]flightPositionInterval, max(0, len(samples)-1))
	for i := range intervals {
		intervals[i] = flightPositionDelta(samples[i], samples[i+1], duplicates)
	}
	for i, sample := range samples {
		if !result[i].Valid || sample.projectile == nil {
			continue
		}
		lo, hi := i, i
		for lo > 0 && intervals[lo-1].valid && sample.t-samples[lo-1].t <= 500 {
			lo--
		}
		for hi+1 < len(samples) && intervals[hi].valid && samples[hi+1].t-sample.t <= 500 {
			hi++
		}
		for hi-lo+1 < 5 {
			left := lo > 0 && intervals[lo-1].valid && samples[hi].t-samples[lo-1].t <= 1200
			right := hi+1 < len(samples) && intervals[hi].valid && samples[hi+1].t-samples[lo].t <= 1200
			if !left && !right {
				break
			}
			if left && (!right || sample.t-samples[lo-1].t <= samples[hi+1].t-sample.t) {
				lo--
			} else {
				hi++
			}
		}
		if hi-lo+1 < 4 || samples[hi].t-samples[lo].t < 400 {
			continue
		}
		var matrix, rhs [3][3]float64
		for j := lo; j <= hi; j++ {
			t := (float64(samples[j].t) - float64(sample.t)) / 1000
			basis := [3]float64{1, t, t * t}
			position := flightSubtract(samples[j], sample)
			for row := 0; row < 3; row++ {
				for col := 0; col < 3; col++ {
					matrix[row][col] += basis[row] * basis[col]
				}
				for axis := 0; axis < 3; axis++ {
					rhs[row][axis] += basis[row] * position[axis]
				}
			}
		}
		coefficients, ok := solveFlightFit(matrix, rhs)
		if !ok {
			continue
		}
		velocity, acceleration := coefficients[1], flightScale(coefficients[2], 2)
		speed := flightVectorMagnitude(velocity)
		if !flightFiniteVector(velocity) || !flightFiniteVector(acceleration) || speed > flightMaxSpeedMPS {
			continue
		}
		f := &result[i]
		f.Velocity, f.Speed, f.GroundSpeed, f.ClimbRate = velocity, speed, math.Hypot(velocity[0], velocity[2]), velocity[1]
		f.HasDirection, f.HasFlightPath = f.GroundSpeed > flightEpsilon, speed > flightEpsilon
		if f.HasDirection {
			f.Course = flightBearing(velocity[0], velocity[2])
		}
		if f.HasFlightPath {
			f.FlightPath = math.Atan2(velocity[1], f.GroundSpeed) * 180 / math.Pi
		}
		f.Acceleration, f.AccelMagnitude = acceleration, flightVectorMagnitude(acceleration)
		f.AccelG = f.AccelMagnitude / flightGravity
		f.NormalG = 0
		if speed > flightEpsilon {
			unit := flightScale(velocity, 1/speed)
			normal := flightVectorDifference(acceleration, flightScale(unit, flightDot(acceleration, unit)))
			f.NormalG = flightVectorMagnitude(normal) / flightGravity
		}
		f.HasAcceleration = true
	}
	for i := range result {
		// Beyond 200 coordinate-g the exporter rejects the numerical estimate;
		// raw positions remain available rather than presenting a false load.
		if result[i].AccelG > 200 {
			result[i].HasAcceleration = false
		}
		result[i].HasTurnRate = false
	}
	flightDeriveTurnRate(samples, intervals, result, duplicates)
	return result
}

func solveFlightFit(matrix, rhs [3][3]float64) ([3][3]float64, bool) {
	for col := 0; col < 3; col++ {
		pivot := col
		for row := col + 1; row < 3; row++ {
			if math.Abs(matrix[row][col]) > math.Abs(matrix[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(matrix[pivot][col]) < 1e-12 {
			return rhs, false
		}
		matrix[col], matrix[pivot] = matrix[pivot], matrix[col]
		rhs[col], rhs[pivot] = rhs[pivot], rhs[col]
		scale := matrix[col][col]
		for k := 0; k < 3; k++ {
			matrix[col][k] /= scale
			rhs[col][k] /= scale
		}
		for row := 0; row < 3; row++ {
			if row == col {
				continue
			}
			factor := matrix[row][col]
			for k := 0; k < 3; k++ {
				matrix[row][k] -= factor * matrix[col][k]
				rhs[row][k] -= factor * rhs[col][k]
			}
		}
	}
	return rhs, true
}
