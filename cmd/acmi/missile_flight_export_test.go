package main

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func TestMissileNativeCoordinatesOmitUnknownBodyAttitude(t *testing.T) {
	sample := acmiSample{x: 10, y: 20, z: 30, roll: 1, pitch: 2, yaw: 3}
	fields := strings.Split(acmiMissilePositionValue(sample), "|")
	if len(fields) != 5 || fields[3] != "10.000" || fields[4] != "30.000" {
		t.Fatalf("invalid missile native transform: %v", fields)
	}
	if len(strings.Split(acmiPositionValue(sample), "|")) != 6 {
		t.Fatal("aircraft transform changed")
	}
}

func TestSpawnPointIsPreservedButNeverDifferentiated(t *testing.T) {
	samples := []acmiSample{
		{t: 1000, x: -200, projectile: &projectiles.Sample{FromSync: false}},
		{t: 1036, x: 0, projectile: &projectiles.Sample{FromSync: true}},
		{t: 1300, x: 26.4, projectile: &projectiles.Sample{FromSync: true}},
		{t: 1600, x: 56.4, projectile: &projectiles.Sample{FromSync: true}},
	}
	flight := enrichMissileFlight(samples)
	if flight[0].Valid {
		t.Fatal("constructor position invented launch velocity")
	}
	for i := 1; i < len(flight); i++ {
		if !flight[i].Valid || flight[i].Speed < 99.99 || flight[i].Speed > 100.01 {
			t.Fatalf("launch discontinuity contaminated sample %d: %+v", i, flight[i])
		}
	}
}

func TestNearCoincidentPacketsDoNotCreateAccelerationSpike(t *testing.T) {
	samples := []acmiSample{{t: 1000, x: 0}, {t: 1001, x: 0.3}, {t: 1301, x: 30.3}, {t: 1601, x: 60.3}}
	flight := enrichMissileFlight(samples)
	if flight[0].HasAcceleration {
		t.Fatal("ill-conditioned first triple used for acceleration")
	}
	if !flight[1].Valid || flight[1].Speed > 100.01 || !flight[1].HasAcceleration || flight[1].AccelMagnitude > 0.01 {
		t.Fatalf("near-coincident packet contaminated balanced window: %+v", flight[1])
	}
}

func TestRecordedLocalFitPreservesQuadraticMotion(t *testing.T) {
	var samples []acmiSample
	for _, stamp := range []uint32{1000, 1100, 1290, 1400, 1600, 1720, 1900} {
		time := float64(stamp-1000) / 1000
		samples = append(samples, acmiSample{t: stamp, x: 100*time + 4*time*time, projectile: &projectiles.Sample{FromSync: true}})
	}
	for i, f := range enrichRecordedMissileFlight(samples) {
		want := 100 + 8*float64(samples[i].t-1000)/1000
		if !f.Valid || math.Abs(f.Speed-want) > 1e-6 || !f.HasAcceleration || math.Abs(f.AccelMagnitude-8) > 1e-6 {
			t.Fatalf("fit changed exact quadratic at %d: %+v", i, f)
		}
	}
}

func TestRecordedLocalFitReducesPositionQuantizationNoise(t *testing.T) {
	var samples []acmiSample
	for i := 0; i < 21; i++ {
		noise := 1.0
		if i%2 == 0 {
			noise = -1
		}
		samples = append(samples, acmiSample{t: uint32(1000 + 100*i), x: float64(10*i) + noise, projectile: &projectiles.Sample{FromSync: true}})
	}
	raw, smooth := enrichMissileFlight(samples), enrichRecordedMissileFlight(samples)
	var before, after float64
	for i := 4; i < 17; i++ {
		before += raw[i].AccelMagnitude * raw[i].AccelMagnitude
		after += smooth[i].AccelMagnitude * smooth[i].AccelMagnitude
	}
	if after >= before*0.1 {
		t.Fatalf("fit did not reduce derivative noise: before=%v after=%v", before, after)
	}
}

func TestFlightPropertiesClearMissingDataAndUseNoFalseAirspeed(t *testing.T) {
	props := strings.Join(missileFlightProperties(missileFlightSample{}), ",")
	for _, want := range []string{"WT_FlightValid=0", "WT_Speed=,", "WT_Acceleration=,"} {
		if !strings.Contains(props, want) {
			t.Fatalf("missing clearing %q in %s", want, props)
		}
	}
	for _, forbidden := range []string{"TAS=", "Mach=", "VerticalGForce=", "RadarMode="} {
		if strings.Contains(props, forbidden) {
			t.Fatalf("invented standard telemetry: %s", props)
		}
	}
}

func TestMissileFlightCSVHasOneRowPerRecordedPoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flight.csv")
	tracks := []*track{{key: trackKey{missile: true, id: 42}, samples: []acmiSample{
		{t: 1000, x: 10}, {t: 2000, x: 20},
	}}}
	if err := writeMissileFlightLog(path, tracks); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("records=%d err=%v", len(rows), err)
	}
	if rows[1][0] != "42" || rows[1][6] != "true" || rows[1][7] != "10.000000" {
		t.Fatalf("incorrect velocity row: %v", rows[1])
	}
}
