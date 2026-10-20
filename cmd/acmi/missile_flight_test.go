package main

import (
	"math"
	"testing"
)

func TestEnrichMissileFlightUniformVelocity(t *testing.T) {
	samples := []acmiSample{
		{t: 1000, x: 100, y: 200, z: 300},
		{t: 2000, x: 200, y: 250, z: 400},
		{t: 3000, x: 300, y: 300, z: 500},
	}
	original := append([]acmiSample(nil), samples...)
	got := enrichMissileFlight(samples)

	if len(got) != len(samples) {
		t.Fatalf("derived samples = %d, want %d", len(got), len(samples))
	}
	if !flightTestSamplesEqual(samples, original) {
		t.Fatal("enrichMissileFlight mutated its input positions or times")
	}

	for i, sample := range got {
		if !sample.Valid || !sample.HasDirection || !sample.HasFlightPath || !sample.HasAcceleration || !sample.HasTurnRate {
			t.Fatalf("sample %d flags = %+v, want all three-point derivatives", i, sample)
		}
		flightTestVectorClose(t, "velocity", sample.Velocity, [3]float64{100, 50, 100}, 1e-9)
		flightTestClose(t, "speed", sample.Speed, 150, 1e-9)
		flightTestClose(t, "ground speed", sample.GroundSpeed, math.Sqrt(20000), 1e-9)
		flightTestClose(t, "climb rate", sample.ClimbRate, 50, 1e-9)
		flightTestClose(t, "course", sample.Course, 45, 1e-9)
		flightTestClose(t, "flight path", sample.FlightPath, math.Atan2(50, math.Sqrt(20000))*180/math.Pi, 1e-9)
		flightTestVectorClose(t, "acceleration", sample.Acceleration, [3]float64{}, 1e-9)
		flightTestClose(t, "acceleration magnitude", sample.AccelMagnitude, 0, 1e-9)
		flightTestClose(t, "acceleration g", sample.AccelG, 0, 1e-9)
		flightTestClose(t, "normal g", sample.NormalG, 0, 1e-9)
		flightTestClose(t, "turn rate", sample.TurnRate, 0, 1e-9)
	}
}

func TestEnrichMissileFlightNonUniformConstantAcceleration(t *testing.T) {
	const acceleration = 8.0
	times := []float64{0, 1, 2.5, 4, 5.5}
	samples := make([]acmiSample, len(times))
	for i, second := range times {
		samples[i] = acmiSample{
			t: uint32(math.Round(second * 1000)),
			x: 0.5 * acceleration * second * second,
		}
	}
	got := enrichMissileFlight(samples)
	for i, second := range times {
		sample := got[i]
		if !sample.Valid || !sample.HasAcceleration {
			t.Fatalf("non-uniform sample %d is missing velocity or acceleration: %+v", i, sample)
		}
		flightTestVectorClose(t, "velocity", sample.Velocity, [3]float64{acceleration * second, 0, 0}, 1e-9)
		flightTestVectorClose(t, "acceleration", sample.Acceleration, [3]float64{acceleration, 0, 0}, 1e-9)
		flightTestClose(t, "acceleration magnitude", sample.AccelMagnitude, acceleration, 1e-9)
		flightTestClose(t, "acceleration g", sample.AccelG, acceleration/9.80665, 1e-12)
		flightTestClose(t, "normal g", sample.NormalG, 0, 1e-12)
	}
}

func TestEnrichMissileFlightTwoPointsHaveVelocityButNoAcceleration(t *testing.T) {
	got := enrichMissileFlight([]acmiSample{
		{t: 0, x: 0},
		{t: 1000, x: 10},
	})
	for i, sample := range got {
		if !sample.Valid {
			t.Fatalf("two-point sample %d lost its secant velocity: %+v", i, sample)
		}
		flightTestVectorClose(t, "velocity", sample.Velocity, [3]float64{10, 0, 0}, 1e-9)
		if sample.HasAcceleration {
			t.Fatalf("two-point sample %d claimed an acceleration: %+v", i, sample)
		}
		flightTestVectorClose(t, "unavailable acceleration", sample.Acceleration, [3]float64{}, 0)
		flightTestClose(t, "unavailable acceleration magnitude", sample.AccelMagnitude, 0, 0)
	}
}

func TestEnrichMissileFlightCourseTurnRate(t *testing.T) {
	const (
		radius     = 1000.0
		omega      = 0.1
		stepSecond = 0.5
	)
	samples := make([]acmiSample, 5)
	for i := range samples {
		tSeconds := float64(i) * stepSecond
		angle := omega * tSeconds
		samples[i] = acmiSample{
			t: uint32(math.Round(tSeconds * 1000)),
			x: radius * (1 - math.Cos(angle)),
			z: radius * math.Sin(angle),
		}
	}
	got := enrichMissileFlight(samples)
	center := got[2]
	if !center.Valid || !center.HasTurnRate || !center.HasDirection {
		t.Fatalf("center turn sample = %+v, want velocity and turn rate", center)
	}
	flightTestClose(t, "speed", center.Speed, radius*omega, 0.1)
	flightTestClose(t, "turn rate deg/s", center.TurnRate, omega*180/math.Pi, 0.01)
}

func TestEnrichMissileFlightDuplicateTimesRemainMissing(t *testing.T) {
	samples := []acmiSample{
		{t: 0, x: 0},
		{t: 1000, x: 10},
		{t: 1000, x: 20},
		{t: 2000, x: 30},
	}
	got := enrichMissileFlight(samples)
	for i, sample := range got {
		if sample.Valid {
			t.Fatalf("sample %d with a duplicate timestamp is valid: %+v", i, sample)
		}
		flightTestNoDerivedPresence(t, i, sample)
	}
}

func TestEnrichMissileFlightLongGapNeverCrossesOrClaimsZero(t *testing.T) {
	samples := []acmiSample{
		{t: 0, x: 0},
		{t: 1000, x: 100},
		{t: 4000, x: 400},
		{t: 5000, x: 500},
	}
	got := enrichMissileFlight(samples)
	for i, sample := range got {
		if !sample.Valid {
			t.Fatalf("sample %d should use only its adjacent finite run: %+v", i, sample)
		}
		flightTestVectorClose(t, "local velocity", sample.Velocity, [3]float64{100, 0, 0}, 1e-9)
		if sample.HasAcceleration {
			t.Fatalf("sample %d fabricated acceleration across a 3 second gap: %+v", i, sample)
		}
	}
}

func TestEnrichMissileFlightAllGapsDoNotClaimZeroKinematics(t *testing.T) {
	got := enrichMissileFlight([]acmiSample{
		{t: 0, x: 0},
		{t: 3001, x: 10},
	})
	for i, sample := range got {
		if sample.Valid {
			t.Fatalf("gap-only sample %d is valid with a default zero velocity: %+v", i, sample)
		}
		flightTestNoDerivedPresence(t, i, sample)
	}
}

func TestEnrichMissileFlightBackwardTimeAndJumpAreMissing(t *testing.T) {
	backward := enrichMissileFlight([]acmiSample{
		{t: 2000, x: 20},
		{t: 1000, x: 10},
	})
	for i, sample := range backward {
		if sample.Valid {
			t.Fatalf("backward-time sample %d is valid: %+v", i, sample)
		}
		flightTestNoDerivedPresence(t, i, sample)
	}

	jump := enrichMissileFlight([]acmiSample{
		{t: 0, x: 0},
		{t: 1000, x: flightMaxSpeedMPS + 1},
	})
	for i, sample := range jump {
		if sample.Valid {
			t.Fatalf("over-speed jump sample %d is valid: %+v", i, sample)
		}
		flightTestNoDerivedPresence(t, i, sample)
	}
}

func TestEnrichMissileFlightStationaryAndSingleSample(t *testing.T) {
	stationary := enrichMissileFlight([]acmiSample{
		{t: 100, x: 5, y: 6, z: 7},
		{t: 1100, x: 5, y: 6, z: 7},
		{t: 2100, x: 5, y: 6, z: 7},
	})
	for i, sample := range stationary {
		if !sample.Valid || !sample.HasAcceleration {
			t.Fatalf("stationary sample %d is missing zero-motion kinematics: %+v", i, sample)
		}
		if sample.HasDirection || sample.HasFlightPath || sample.HasTurnRate {
			t.Fatalf("stationary sample %d invented a path direction: %+v", i, sample)
		}
		if sample.Speed != 0 || sample.GroundSpeed != 0 || sample.ClimbRate != 0 {
			t.Fatalf("stationary sample %d speeds = %+v", i, sample)
		}
	}

	single := enrichMissileFlight([]acmiSample{{t: 1, x: 2, y: 3, z: 4}})
	if len(single) != 1 || single[0].Valid {
		t.Fatalf("single sample should have one finite missing result: %+v", single)
	}
	flightTestNoDerivedPresence(t, 0, single[0])
}

func TestEnrichMissileFlightVerticalPathHasNoCourse(t *testing.T) {
	got := enrichMissileFlight([]acmiSample{
		{t: 0, x: 5, y: 10, z: 7},
		{t: 1000, x: 5, y: 110, z: 7},
		{t: 2000, x: 5, y: 210, z: 7},
	})
	for i, sample := range got {
		if !sample.Valid || sample.HasDirection {
			t.Fatalf("vertical sample %d course flags = %+v, want valid velocity with no course", i, sample)
		}
		if !sample.HasFlightPath {
			t.Fatalf("vertical sample %d has no flight path: %+v", i, sample)
		}
		flightTestVectorClose(t, "velocity", sample.Velocity, [3]float64{0, 100, 0}, 1e-9)
		flightTestClose(t, "ground speed", sample.GroundSpeed, 0, 1e-9)
		flightTestClose(t, "flight path", sample.FlightPath, 90, 1e-9)
		flightTestClose(t, "course", sample.Course, 0, 0)
	}
}

func TestEnrichMissileFlightNaNPositionIsMissing(t *testing.T) {
	samples := []acmiSample{
		{t: 0, x: 0},
		{t: 1000, x: math.NaN()},
		{t: 2000, x: 20},
	}
	got := enrichMissileFlight(samples)
	for i, sample := range got {
		if sample.Valid {
			t.Fatalf("NaN-adjacent sample %d is present: %+v", i, sample)
		}
		flightTestNoDerivedPresence(t, i, sample)
	}
}

func flightTestNoDerivedPresence(t *testing.T, index int, sample missileFlightSample) {
	t.Helper()
	if sample.HasDirection || sample.HasFlightPath || sample.HasAcceleration || sample.HasTurnRate {
		t.Fatalf("sample %d claimed a derived result without a valid differential: %+v", index, sample)
	}
	flightTestFinite(t, sample)
}

func flightTestSamplesEqual(a, b []acmiSample) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func flightTestFinite(t *testing.T, sample missileFlightSample) {
	t.Helper()
	values := append([]float64{}, sample.Velocity[:]...)
	values = append(values, sample.Speed, sample.GroundSpeed, sample.ClimbRate)
	values = append(values, sample.Course, sample.FlightPath)
	values = append(values, sample.Acceleration[:]...)
	values = append(values, sample.AccelMagnitude, sample.AccelG, sample.NormalG, sample.TurnRate)
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("non-finite derived value %v in %+v", value, sample)
		}
	}
}

func flightTestVectorClose(t *testing.T, name string, got, want [3]float64, tolerance float64) {
	t.Helper()
	for i := range got {
		flightTestClose(t, name, got[i], want[i], tolerance)
	}
}

func flightTestClose(t *testing.T, name string, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s = %.12g, want %.12g (tolerance %.3g)", name, got, want, tolerance)
	}
}
