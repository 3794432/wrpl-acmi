package main

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestAircraftHeadingToACMIAppliesClockwiseQuarterTurnAndWraps(t *testing.T) {
	tests := []struct {
		name   string
		source float64
		want   float64
	}{
		{name: "north", source: 0, want: 90},
		{name: "east", source: 90, want: 180},
		{name: "south", source: 180, want: 270},
		{name: "west wraps", source: 270, want: 0},
		{name: "full turn wraps", source: 360, want: 90},
		{name: "negative wraps", source: -90, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aircraftHeadingToACMI(tt.source)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("aircraftHeadingToACMI(%v) = %v, want %v", tt.source, got, tt.want)
			}
		})
	}
}

func TestACMIPositionFormatterLeavesPositionAttitudeAndYawAxesUnchanged(t *testing.T) {
	oldLat, oldLon, oldAlt := atlasLatitude, atlasLongitude, *alt0
	atlasLatitude, atlasLongitude, *alt0 = 50, 10, 1000
	defer func() {
		atlasLatitude, atlasLongitude, *alt0 = oldLat, oldLon, oldAlt
	}()

	sample := acmiSample{x: 1250, y: 23.5, z: 600, roll: 12.5, pitch: -7.25, yaw: 350}
	fields := strings.Split(acmiPositionValue(sample), "|")
	if len(fields) != 6 {
		t.Fatalf("position has %d fields, want 6: %v", len(fields), fields)
	}
	got := make([]float64, len(fields))
	for i, field := range fields {
		value, err := strconv.ParseFloat(field, 64)
		if err != nil {
			t.Fatalf("parsing position field %d %q: %v", i, field, err)
		}
		got[i] = value
	}

	lat := 50 + 600/111320.0
	lon := 10 + 1250/(111320.0*math.Cos(lat*math.Pi/180))
	want := []float64{lon, lat, 1023.5, 12.5, -7.25, 350}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-6 {
			t.Fatalf("field %d = %v, want %v; position, attitude, and yaw axes must be unchanged", i, got[i], want[i])
		}
	}
}
