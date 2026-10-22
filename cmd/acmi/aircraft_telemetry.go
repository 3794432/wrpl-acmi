package main

import (
	"fmt"
	"math"
	"strconv"
)

const (
	// Aircraft telemetry is estimated only from finite, closely spaced recorded
	// positions. The speed limit is well above the replay aircraft envelope and
	// catches coordinate seams; the acceleration limit rejects differentiated
	// position jumps while retaining extreme but possible flight loads.
	aircraftTelemetryMaxSpeedMPS       = 1200.0
	aircraftTelemetryMaxAccelerationG  = 100.0
	aircraftTelemetryMaxAltitudeMeters = 84852.0

	aircraftISASeaLevelTemperatureK = 288.15
	aircraftISASeaLevelPressurePa   = 101325.0
	aircraftISASeaLevelDensity      = 1.225
	aircraftISATropopauseMeters     = 11000.0
	aircraftISALapseRateKPerMeter   = 0.0065
	aircraftISAGasConstant          = 287.05287
	aircraftISAHeatCapacityRatio    = 1.4
)

var aircraftTelemetryFieldNames = [...]string{
	"HDG",
	"TAS",
	"Mach",
	"IAS",
	"VerticalGForce",
	"LongitudinalGForce",
	"LateralGForce",
	"AOA",
}

// aircraftTelemetryProperties returns standard ACMI telemetry assignments in
// the same order for every input sample. Missing values use NAME= so a writer
// which carries properties forward clears them at the next aircraft update.
//
// Positions use local replay axes: x east, y up, and z north. Yaw, pitch, and
// roll are already Tacview degrees (clockwise from north, nose-up, and
// right-wing-down). TAS is the recorded path speed under still-air and ISA
// assumptions. IAS is TAS*sqrt(rho/rho0), a density-ratio approximation of
// indicated airspeed; it is not exact compressible calibrated airspeed and
// includes no instrument or installation error. The G values are body-axis
// specific force (coordinate acceleration minus gravity), expressed in g.
//
// Samples must contain one continuous aircraft lifetime. The caller trims at
// destruction. The helper cannot distinguish a destruction seam from a valid
// position jump, and leaves sufficiently large jumps unestimated.
func aircraftTelemetryProperties(samples []acmiSample) [][]string {
	properties := make([][]string, len(samples))
	if len(samples) == 0 {
		return properties
	}

	var flight []missileFlightSample
	var acceleration []missileFlightSample
	if len(samples) >= 2 && aircraftTelemetryStrictlyIncreasing(samples) {
		duplicates := flightDuplicateTimes(samples)
		intervals := aircraftTelemetryIntervals(samples, duplicates)

		// Use the tighter aircraft interval guard for velocity as well as
		// acceleration, so a fast seam cannot contaminate a nearby estimate.
		flight = make([]missileFlightSample, len(samples))
		for i, sample := range samples {
			if !aircraftTelemetryFinitePosition(sample) {
				continue
			}
			velocity, ok := flightVelocityAt(samples, i, intervals, duplicates)
			if !ok || !flightFiniteVector(velocity) {
				continue
			}
			flight[i] = missileFlightSample{Valid: true, Velocity: velocity, Speed: flightVectorMagnitude(velocity)}
		}
		acceleration = make([]missileFlightSample, len(samples))
		for i := range flight {
			if !flight[i].Valid || !aircraftTelemetryFinite(flight[i].Speed) ||
				flight[i].Speed > aircraftTelemetryMaxSpeedMPS+flightEpsilon {
				continue
			}
			acceleration[i].Valid = true
			acceleration[i].Velocity = flight[i].Velocity
			acceleration[i].Speed = flight[i].Speed
		}
		flightDeriveAcceleration(samples, intervals, acceleration, duplicates)
		for i := range samples {
			velocity, accel, ok := aircraftTelemetryLocalFit(samples, intervals, i)
			if !ok {
				continue
			}
			speed := flightVectorMagnitude(velocity)
			if speed > aircraftTelemetryMaxSpeedMPS || !flightFiniteVector(velocity) || !flightFiniteVector(accel) {
				continue
			}
			flight[i] = missileFlightSample{Valid: true, Velocity: velocity, Speed: speed}
			acceleration[i] = missileFlightSample{Valid: true, HasAcceleration: true, Acceleration: accel, AccelMagnitude: flightVectorMagnitude(accel)}
		}
	}

	for i, sample := range samples {
		hdg := aircraftTelemetryAssignment("HDG", sample.yaw, aircraftTelemetryFinite(sample.yaw))
		if aircraftTelemetryFinite(sample.yaw) {
			hdg = aircraftTelemetryAssignment("HDG", aircraftTelemetryHeading(sample.yaw), true)
		}
		values := make([]string, len(aircraftTelemetryFieldNames))
		values[0] = hdg
		for j := 1; j < len(values); j++ {
			values[j] = aircraftTelemetryFieldNames[j] + "="
		}
		properties[i] = values

		if len(flight) == 0 || !flight[i].Valid || !aircraftTelemetryFinitePosition(sample) {
			continue
		}
		speed := flight[i].Speed
		if !aircraftTelemetryFinite(speed) || speed > aircraftTelemetryMaxSpeedMPS+flightEpsilon {
			continue
		}

		values[1] = aircraftTelemetryAssignment("TAS", speed, true)
		if sound, densityRatio, ok := aircraftTelemetryISA(sample.y); ok {
			values[2] = aircraftTelemetryAssignment("Mach", speed/sound, true)
			values[3] = aircraftTelemetryAssignment("IAS", speed*math.Sqrt(densityRatio), true)
		}

		forward, right, up, attitudeOK := aircraftTelemetryBodyAxes(sample)
		if !attitudeOK {
			continue
		}
		bodyVelocity := [3]float64{
			flightDot(flight[i].Velocity, forward),
			flightDot(flight[i].Velocity, right),
			flightDot(flight[i].Velocity, up),
		}
		if speed > flightEpsilon && bodyVelocity[0] > flightEpsilon {
			angle := math.Atan2(-bodyVelocity[2], bodyVelocity[0]) * 180 / math.Pi
			values[7] = aircraftTelemetryAssignment("AOA", angle, true)
		}

		if !acceleration[i].HasAcceleration ||
			!aircraftTelemetryFinite(acceleration[i].AccelMagnitude) ||
			acceleration[i].AccelMagnitude > aircraftTelemetryMaxAccelerationG*flightGravity {
			continue
		}
		specificForce := [3]float64{
			acceleration[i].Acceleration[0],
			acceleration[i].Acceleration[1] + flightGravity,
			acceleration[i].Acceleration[2],
		}
		values[4] = aircraftTelemetryAssignment("VerticalGForce", flightDot(specificForce, up)/flightGravity, true)
		values[5] = aircraftTelemetryAssignment("LongitudinalGForce", flightDot(specificForce, forward)/flightGravity, true)
		values[6] = aircraftTelemetryAssignment("LateralGForce", flightDot(specificForce, right)/flightGravity, true)
	}
	return properties
}

// A short bounded quadratic fit suppresses packet timing/position jitter.
// Never cross a rejected interval, and cap the work per sample at 65 points.
func aircraftTelemetryLocalFit(samples []acmiSample, intervals []flightPositionInterval, i int) (velocity, acceleration [3]float64, ok bool) {
	lo, hi := i, i
	for lo > 0 && i-lo < 32 && intervals[lo-1].valid && samples[i].t-samples[lo-1].t <= 500 {
		lo--
	}
	for hi+1 < len(samples) && hi-i < 32 && intervals[hi].valid && samples[hi+1].t-samples[i].t <= 500 {
		hi++
	}
	for hi-lo+1 < 4 {
		left := lo > 0 && intervals[lo-1].valid && samples[hi].t-samples[lo-1].t <= 1200
		right := hi+1 < len(samples) && intervals[hi].valid && samples[hi+1].t-samples[lo].t <= 1200
		if !left && !right {
			break
		}
		if left && (!right || samples[i].t-samples[lo-1].t <= samples[hi+1].t-samples[i].t) {
			lo--
		} else {
			hi++
		}
	}
	if hi-lo+1 < 4 || samples[hi].t-samples[lo].t < 400 {
		return velocity, acceleration, false
	}
	var matrix, rhs [3][3]float64
	for j := lo; j <= hi; j++ {
		t := (float64(samples[j].t) - float64(samples[i].t)) / 1000
		basis := [3]float64{1, t, t * t}
		position := flightSubtract(samples[j], samples[i])
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
		return velocity, acceleration, false
	}
	return coefficients[1], flightScale(coefficients[2], 2), true
}

func aircraftTelemetryStrictlyIncreasing(samples []acmiSample) bool {
	for i := 1; i < len(samples); i++ {
		if samples[i].t <= samples[i-1].t {
			return false
		}
	}
	return true
}

func aircraftTelemetryIntervals(samples []acmiSample, duplicates map[uint32]bool) []flightPositionInterval {
	intervals := make([]flightPositionInterval, max(0, len(samples)-1))
	for i := range intervals {
		interval := flightPositionDelta(samples[i], samples[i+1], duplicates)
		if !interval.valid {
			continue
		}
		if flightVectorMagnitude(interval.delta)/interval.dt > aircraftTelemetryMaxSpeedMPS+flightEpsilon {
			continue
		}
		intervals[i] = interval
	}
	return intervals
}

func aircraftTelemetryFinitePosition(sample acmiSample) bool {
	return flightFinitePosition(sample)
}

// aircraftTelemetryBodyAxes returns forward, right, and up body axes in the
// local x-east, y-up, z-north frame. Positive roll lowers the right wing.
func aircraftTelemetryBodyAxes(sample acmiSample) (forward, right, up [3]float64, ok bool) {
	if !aircraftTelemetryFinite(sample.yaw) || !aircraftTelemetryFinite(sample.pitch) ||
		!aircraftTelemetryFinite(sample.roll) {
		return forward, right, up, false
	}
	yaw := sample.yaw * math.Pi / 180
	pitch := sample.pitch * math.Pi / 180
	roll := sample.roll * math.Pi / 180
	sy, cy := math.Sin(yaw), math.Cos(yaw)
	sp, cp := math.Sin(pitch), math.Cos(pitch)
	sr, cr := math.Sin(roll), math.Cos(roll)

	forward = [3]float64{sy * cp, sp, cy * cp}
	levelRight := [3]float64{cy, 0, -sy}
	levelUp := [3]float64{-sy * sp, cp, -cy * sp}
	right = [3]float64{
		levelRight[0]*cr - levelUp[0]*sr,
		levelRight[1]*cr - levelUp[1]*sr,
		levelRight[2]*cr - levelUp[2]*sr,
	}
	up = [3]float64{
		levelUp[0]*cr + levelRight[0]*sr,
		levelUp[1]*cr + levelRight[1]*sr,
		levelUp[2]*cr + levelRight[2]*sr,
	}
	return forward, right, up, flightFiniteVector(forward) && flightFiniteVector(right) && flightFiniteVector(up)
}

// aircraftTelemetryISA returns local speed of sound and density relative to
// sea level for the layered standard atmosphere through 84.852 km. Game height
// is used as an approximation of geopotential height; no weather is recorded.
func aircraftTelemetryISA(altitude float64) (speedOfSound, densityRatio float64, ok bool) {
	if !aircraftTelemetryFinite(altitude) || altitude > aircraftTelemetryMaxAltitudeMeters {
		return 0, 0, false
	}
	var temperature, pressure float64
	if altitude <= aircraftISATropopauseMeters {
		temperature = aircraftISASeaLevelTemperatureK - aircraftISALapseRateKPerMeter*altitude
		if temperature <= 0 {
			return 0, 0, false
		}
		exponent := flightGravity / (aircraftISALapseRateKPerMeter * aircraftISAGasConstant)
		pressure = aircraftISASeaLevelPressurePa * math.Pow(temperature/aircraftISASeaLevelTemperatureK, exponent)
	} else {
		temperature = aircraftISASeaLevelTemperatureK - aircraftISALapseRateKPerMeter*aircraftISATropopauseMeters
		tropopauseExponent := flightGravity / (aircraftISALapseRateKPerMeter * aircraftISAGasConstant)
		tropopausePressure := aircraftISASeaLevelPressurePa *
			math.Pow(temperature/aircraftISASeaLevelTemperatureK, tropopauseExponent)
		pressure = tropopausePressure
		base := aircraftISATropopauseMeters
		layers := [][2]float64{{20000, 0}, {32000, .001}, {47000, .0028}, {51000, 0}, {71000, -.0028}, {84852, -.002}}
		for _, layer := range layers {
			top := math.Min(altitude, layer[0])
			lapse := layer[1]
			if lapse == 0 {
				pressure *= math.Exp(-flightGravity * (top - base) / (aircraftISAGasConstant * temperature))
			} else {
				next := temperature + lapse*(top-base)
				pressure *= math.Pow(next/temperature, -flightGravity/(lapse*aircraftISAGasConstant))
				temperature = next
			}
			base = top
			if top >= altitude {
				break
			}
		}
	}
	density := pressure / (aircraftISAGasConstant * temperature)
	speedOfSound = math.Sqrt(aircraftISAHeatCapacityRatio * aircraftISAGasConstant * temperature)
	densityRatio = density / aircraftISASeaLevelDensity
	if !aircraftTelemetryFinite(speedOfSound) || !aircraftTelemetryFinite(densityRatio) || speedOfSound <= 0 || densityRatio < 0 {
		return 0, 0, false
	}
	return speedOfSound, densityRatio, true
}

func aircraftTelemetryHeading(yaw float64) float64 {
	heading := math.Mod(yaw, 360)
	if heading < 0 {
		heading += 360
	}
	if heading >= 360 {
		heading -= 360
	}
	if heading == 0 {
		return 0
	}
	return heading
}

func aircraftTelemetryAssignment(name string, value float64, available bool) string {
	if !available || !aircraftTelemetryFinite(value) {
		return name + "="
	}
	// Avoid a negative-zero spelling for mathematically zero components.
	if math.Abs(value) < 0.0000005 {
		value = 0
	}
	return fmt.Sprintf("%s=%s", name, strconv.FormatFloat(value, 'f', 6, 64))
}

func aircraftTelemetryFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
