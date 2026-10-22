package main

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestAircraftTelemetryLevelFlight(t *testing.T) {
	samples := aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 0, north: 0, y: 0},
		{t: 1000, north: 100, y: 0},
		{t: 2000, north: 200, y: 0},
		{t: 3000, north: 300, y: 0},
	})
	properties := aircraftTelemetryProperties(samples)
	if len(properties) != len(samples) {
		t.Fatalf("property rows = %d, want %d", len(properties), len(samples))
	}
	for i, row := range properties {
		got := aircraftTelemetryTestMap(t, row)
		aircraftTelemetryTestClose(t, "TAS", got, "TAS", 100, 1e-6)
		aircraftTelemetryTestClose(t, "IAS", got, "IAS", 100, 1e-6)
		aircraftTelemetryTestClose(t, "Mach", got, "Mach", 100/aircraftTelemetryTestSeaLevelSound(), 1e-5)
		aircraftTelemetryTestClose(t, "HDG", got, "HDG", 0, 1e-6)
		aircraftTelemetryTestClose(t, "AOA", got, "AOA", 0, 1e-6)
		aircraftTelemetryTestClose(t, "VerticalGForce", got, "VerticalGForce", 1, 1e-6)
		aircraftTelemetryTestClose(t, "LongitudinalGForce", got, "LongitudinalGForce", 0, 1e-6)
		aircraftTelemetryTestClose(t, "LateralGForce", got, "LateralGForce", 0, 1e-6)
		if i == 1 && got["HDG"] == "" {
			t.Fatal("valid attitude lost HDG")
		}
	}
}

func TestAircraftTelemetryFreefallHasZeroSpecificForce(t *testing.T) {
	samples := make([]acmiSample, 4)
	for i := range samples {
		second := float64(i)
		samples[i] = acmiSample{
			t:     uint32(i * 1000),
			y:     5000 - 0.5*flightGravity*second*second,
			pitch: -90,
		}
	}
	got := aircraftTelemetryTestMap(t, aircraftTelemetryProperties(samples)[1])
	for _, field := range []string{"VerticalGForce", "LongitudinalGForce", "LateralGForce"} {
		aircraftTelemetryTestClose(t, field, got, field, 0, 1e-5)
	}
	aircraftTelemetryTestClose(t, "AOA", got, "AOA", 0, 1e-5)
	aircraftTelemetryTestClose(t, "TAS", got, "TAS", flightGravity, 1e-5)
}

func TestAircraftTelemetryPullupIsTwoGNormal(t *testing.T) {
	samples := make([]acmiSample, 4)
	for i := range samples {
		second := float64(i)
		samples[i] = acmiSample{
			t: uint32(i * 1000),
			z: 100 * second,
			y: 0.5 * flightGravity * second * second,
		}
	}
	got := aircraftTelemetryTestMap(t, aircraftTelemetryProperties(samples)[1])
	aircraftTelemetryTestClose(t, "VerticalGForce", got, "VerticalGForce", 2, 1e-5)
	aircraftTelemetryTestClose(t, "LongitudinalGForce", got, "LongitudinalGForce", 0, 1e-5)
	aircraftTelemetryTestClose(t, "LateralGForce", got, "LateralGForce", 0, 1e-5)
}

func TestAircraftTelemetryRollProjectsSpecificForce(t *testing.T) {
	samples := aircraftTelemetryTestStationary(rollDegrees(60))
	got := aircraftTelemetryTestMap(t, aircraftTelemetryProperties(samples)[1])
	aircraftTelemetryTestClose(t, "VerticalGForce", got, "VerticalGForce", math.Cos(math.Pi/3), 1e-5)
	aircraftTelemetryTestClose(t, "LateralGForce", got, "LateralGForce", -math.Sin(math.Pi/3), 1e-5)
	aircraftTelemetryTestClose(t, "LongitudinalGForce", got, "LongitudinalGForce", 0, 1e-5)
}

func TestAircraftTelemetryInvertedFlightHasNegativeNormalForce(t *testing.T) {
	samples := aircraftTelemetryTestStationary(rollDegrees(180))
	got := aircraftTelemetryTestMap(t, aircraftTelemetryProperties(samples)[1])
	aircraftTelemetryTestClose(t, "VerticalGForce", got, "VerticalGForce", -1, 1e-5)
	aircraftTelemetryTestClose(t, "LateralGForce", got, "LateralGForce", 0, 1e-5)
}

func TestAircraftTelemetryClimbAttitudeAndAirspeed(t *testing.T) {
	pitch := 10.0
	rise := math.Tan(pitch * math.Pi / 180)
	points := make([]aircraftTelemetryTestPoint, 4)
	for i := range points {
		second := float64(i)
		points[i] = aircraftTelemetryTestPoint{t: uint32(i * 1000), north: 100 * second, y: 100 * rise * second}
	}
	samples := aircraftTelemetryTestLine(0, points)
	for i := range samples {
		samples[i].pitch = pitch
	}
	got := aircraftTelemetryTestMap(t, aircraftTelemetryProperties(samples)[1])
	aircraftTelemetryTestClose(t, "TAS", got, "TAS", 100/math.Cos(pitch*math.Pi/180), 1e-5)
	aircraftTelemetryTestClose(t, "AOA", got, "AOA", 0, 1e-5)
	aircraftTelemetryTestClose(t, "VerticalGForce", got, "VerticalGForce", math.Cos(pitch*math.Pi/180), 1e-5)
	aircraftTelemetryTestClose(t, "LongitudinalGForce", got, "LongitudinalGForce", math.Sin(pitch*math.Pi/180), 1e-5)
	aircraftTelemetryTestClose(t, "LateralGForce", got, "LateralGForce", 0, 1e-5)
}

func TestAircraftTelemetrySideSlipKeepsLateralVelocityOutOfAOA(t *testing.T) {
	pitch := 10.0
	points := make([]aircraftTelemetryTestPoint, 4)
	for i := range points {
		second := float64(i)
		points[i] = aircraftTelemetryTestPoint{t: uint32(i * 1000), east: 20 * second, north: 100 * second}
	}
	samples := aircraftTelemetryTestLine(0, points)
	for i := range samples {
		samples[i].pitch = pitch
	}
	got := aircraftTelemetryTestMap(t, aircraftTelemetryProperties(samples)[1])
	aircraftTelemetryTestClose(t, "AOA", got, "AOA", pitch, 1e-5)
	aircraftTelemetryTestClose(t, "LateralGForce", got, "LateralGForce", 0, 1e-5)
	aircraftTelemetryTestClose(t, "TAS", got, "TAS", math.Hypot(20, 100), 1e-5)
}

func TestAircraftTelemetryGapClearsAccelerationAndDoesNotCarryValues(t *testing.T) {
	samples := aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 0, north: 0},
		{t: 1000, north: 100},
		{t: 4000, north: 400},
		{t: 5000, north: 500},
	})
	properties := aircraftTelemetryProperties(samples)
	for i, row := range properties {
		got := aircraftTelemetryTestMap(t, row)
		aircraftTelemetryTestClose(t, "local TAS", got, "TAS", 100, 1e-6)
		for _, field := range []string{"VerticalGForce", "LongitudinalGForce", "LateralGForce"} {
			if got[field] != "" {
				t.Fatalf("sample %d fabricated %s across a 3 s gap: %q", i, field, row)
			}
		}
	}
}

func TestAircraftTelemetryNonfinitePositionAndAttitudeClearEstimates(t *testing.T) {
	samples := aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 0, north: 0}, {t: 1000, north: 100}, {t: 2000, north: 200}, {t: 3000, north: 300},
	})
	samples[2].x = math.NaN()
	samples[1].roll = math.Inf(1)
	properties := aircraftTelemetryProperties(samples)
	got := aircraftTelemetryTestMap(t, properties[2])
	for _, field := range []string{"TAS", "Mach", "IAS", "VerticalGForce", "LongitudinalGForce", "LateralGForce", "AOA"} {
		if got[field] != "" {
			t.Fatalf("nonfinite position produced %s: %q", field, properties[2])
		}
	}
	if got["HDG"] != "0.000000" {
		t.Fatalf("finite heading was discarded with the bad position: %q", properties[2])
	}
	got = aircraftTelemetryTestMap(t, properties[1])
	if got["TAS"] == "" {
		t.Fatal("nonfinite roll unnecessarily discarded world-frame TAS")
	}
	for _, field := range []string{"VerticalGForce", "LongitudinalGForce", "LateralGForce", "AOA"} {
		if got[field] != "" {
			t.Fatalf("nonfinite roll produced body-frame %s: %q", field, properties[1])
		}
	}
}

func TestAircraftTelemetryDuplicateAndReversedTimesClearKinematics(t *testing.T) {
	duplicate := aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 0, north: 0}, {t: 1000, north: 100}, {t: 1000, north: 200}, {t: 2000, north: 300},
	})
	backward := aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 2000, north: 200}, {t: 1000, north: 100}, {t: 3000, north: 300}, {t: 4000, north: 400},
	})
	for name, samples := range map[string][]acmiSample{"duplicate": duplicate, "backward": backward} {
		for i, row := range aircraftTelemetryProperties(samples) {
			got := aircraftTelemetryTestMap(t, row)
			for _, field := range []string{"TAS", "Mach", "IAS", "VerticalGForce", "LongitudinalGForce", "LateralGForce", "AOA"} {
				if got[field] != "" {
					t.Fatalf("%s sample %d produced %s from invalid chronology: %q", name, i, field, row)
				}
			}
		}
	}
}

func TestAircraftTelemetryAbsurdJumpAndBackwardVelocityAreNotEstimated(t *testing.T) {
	jump := aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 0, north: 0}, {t: 1000, north: aircraftTelemetryMaxSpeedMPS + 1}, {t: 2000, north: 2 * (aircraftTelemetryMaxSpeedMPS + 1)},
	})
	jumpProperties := aircraftTelemetryProperties(jump)
	if got := aircraftTelemetryTestMap(t, jumpProperties[1]); got["TAS"] != "" {
		t.Fatalf("absurd speed jump produced TAS: %q", jumpProperties[1])
	}
	positionJump := aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 0, north: 0}, {t: 1000, north: 0}, {t: 2000, north: 1100}, {t: 3000, north: 1100},
	})
	got := aircraftTelemetryTestMap(t, aircraftTelemetryProperties(positionJump)[1])
	if got["TAS"] == "" {
		t.Fatal("finite below-limit speed was unnecessarily discarded")
	}
	for _, field := range []string{"VerticalGForce", "LongitudinalGForce", "LateralGForce"} {
		if got[field] != "" {
			t.Fatalf("isolated position jump produced %s: %v", field, got)
		}
	}

	backward := aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 0, north: 0}, {t: 1000, north: -100}, {t: 2000, north: -200},
	})
	got = aircraftTelemetryTestMap(t, aircraftTelemetryProperties(backward)[1])
	aircraftTelemetryTestClose(t, "TAS", got, "TAS", 100, 1e-6)
	if got["AOA"] != "" {
		t.Fatalf("backward body velocity produced AOA: %v", got)
	}
}

func TestAircraftTelemetryOrderAndClearAssignments(t *testing.T) {
	properties := aircraftTelemetryProperties([]acmiSample{{t: 1, x: 1, y: 2, z: 3}})
	if len(properties) != 1 || len(properties[0]) != len(aircraftTelemetryFieldNames) {
		t.Fatalf("single sample output = %#v", properties)
	}
	for i, name := range aircraftTelemetryFieldNames {
		if !strings.HasPrefix(properties[0][i], name+"=") {
			t.Fatalf("field %d = %q, want prefix %s=", i, properties[0][i], name)
		}
	}
	for i := 1; i < len(properties[0]); i++ {
		if properties[0][i] != aircraftTelemetryFieldNames[i]+"=" {
			t.Fatalf("single sample did not clear %s: %q", aircraftTelemetryFieldNames[i], properties[0][i])
		}
	}
}

func TestAircraftTelemetryISAAtAltitudeApproximatesIAS(t *testing.T) {
	sound, densityRatio, ok := aircraftTelemetryISA(aircraftISATropopauseMeters)
	if !ok {
		t.Fatal("ISA unavailable at the tropopause")
	}
	aircraftTelemetryTestClose(t, "speed of sound", map[string]string{"v": strconv.FormatFloat(sound, 'g', -1, 64)}, "v", math.Sqrt(1.4*287.05287*216.65), 1e-6)
	aircraftTelemetryTestClose(t, "density ratio", map[string]string{"v": strconv.FormatFloat(densityRatio, 'g', -1, 64)}, "v", 0.297075, 5e-5)

	properties := aircraftTelemetryProperties(aircraftTelemetryTestLine(0, []aircraftTelemetryTestPoint{
		{t: 0, y: aircraftISATropopauseMeters}, {t: 1000, north: 200, y: aircraftISATropopauseMeters},
	}))
	got := aircraftTelemetryTestMap(t, properties[0])
	if got["IAS"] == "" || got["TAS"] == "" {
		t.Fatalf("missing altitude airspeed estimates: %q", properties[0])
	}
	ias, _ := strconv.ParseFloat(got["IAS"], 64)
	tas, _ := strconv.ParseFloat(got["TAS"], 64)
	if !(ias < tas) {
		t.Fatalf("density-ratio IAS %.6f was not below TAS %.6f", ias, tas)
	}
}

func TestAircraftTelemetryISAUpperLayersAndJitterFit(t *testing.T) {
	sound, _, ok := aircraftTelemetryISA(32000)
	if !ok || math.Abs(sound-math.Sqrt(1.4*287.05287*228.65)) > 1e-6 {
		t.Fatalf("bad stratosphere sound speed %v", sound)
	}
	if _, _, ok := aircraftTelemetryISA(90000); ok {
		t.Fatal("unsupported atmosphere height estimated")
	}
	samples := make([]acmiSample, 41)
	for i := range samples {
		samples[i] = acmiSample{t: uint32(i * 50), z: float64(i) * 10, y: 1000 + 0.02*math.Cos(float64(i)*math.Pi)}
	}
	props := aircraftTelemetryProperties(samples)
	for i := 10; i < 30; i++ {
		g := aircraftTelemetryTestMap(t, props[i])
		value, err := strconv.ParseFloat(g["VerticalGForce"], 64)
		if err != nil || math.Abs(value-1) > .05 {
			t.Fatalf("packet jitter amplified at %d: %v", i, g)
		}
	}
}

type aircraftTelemetryTestPoint struct {
	t     uint32
	east  float64
	north float64
	y     float64
	roll  float64
	pitch float64
	yaw   float64
}

func aircraftTelemetryTestLine(yaw float64, points []aircraftTelemetryTestPoint) []acmiSample {
	samples := make([]acmiSample, len(points))
	for i, point := range points {
		samples[i] = acmiSample{
			t: point.t, x: point.east, y: point.y, z: point.north,
			roll: point.roll, pitch: point.pitch, yaw: yaw + point.yaw,
		}
	}
	return samples
}

func aircraftTelemetryTestStationary(roll, pitch, yaw float64) []acmiSample {
	samples := make([]acmiSample, 4)
	for i := range samples {
		samples[i] = acmiSample{t: uint32(i * 1000), y: 1000, roll: roll, pitch: pitch, yaw: yaw}
	}
	return samples
}

func aircraftTelemetryTestMap(t *testing.T, properties []string) map[string]string {
	t.Helper()
	if len(properties) != len(aircraftTelemetryFieldNames) {
		t.Fatalf("properties = %v, want %d standard fields", properties, len(aircraftTelemetryFieldNames))
	}
	result := make(map[string]string, len(properties))
	for i, property := range properties {
		parts := strings.SplitN(property, "=", 2)
		if len(parts) != 2 || parts[0] != aircraftTelemetryFieldNames[i] {
			t.Fatalf("property %d = %q, want assignment for %s", i, property, aircraftTelemetryFieldNames[i])
		}
		result[parts[0]] = parts[1]
	}
	return result
}

func aircraftTelemetryTestClose(t *testing.T, name string, got map[string]string, field string, want, tolerance float64) {
	t.Helper()
	text, ok := got[field]
	if !ok || text == "" {
		t.Fatalf("%s is missing from %v", name, got)
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatalf("%s = %q: %v", name, text, err)
	}
	if math.Abs(value-want) > tolerance {
		t.Fatalf("%s = %.9g, want %.9g (tolerance %.3g)", name, value, want, tolerance)
	}
}

func aircraftTelemetryTestSeaLevelSound() float64 {
	return math.Sqrt(aircraftISAHeatCapacityRatio * aircraftISAGasConstant * aircraftISASeaLevelTemperatureK)
}

func rollDegrees(value float64) (roll, pitch, yaw float64) {
	return value, 0, 0
}
