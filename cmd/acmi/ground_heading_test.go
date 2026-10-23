package main

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
)

func TestGroundHeadingCardinalDirections(t *testing.T) {
	oldLat, oldLon, oldAlt, oldProfile := atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile
	atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile = 0, 0, 0, nil
	t.Cleanup(func() {
		atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile = oldLat, oldLon, oldAlt, oldProfile
	})

	tests := []struct {
		name   string
		second acmiSample
		want   float64
	}{
		{name: "north", second: acmiSample{t: 1100, z: 10}, want: 0},
		{name: "east", second: acmiSample{t: 1100, x: 10}, want: 90},
		{name: "south", second: acmiSample{t: 1100, z: -10}, want: 180},
		{name: "west", second: acmiSample{t: 1100, x: -10}, want: 270},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &track{key: trackKey{id: 1, groundEntity: true}, samples: []acmiSample{{t: 1000}, tt.second}}
			for i := range tr.samples {
				fields := groundPositionFields(t, groundPositionValue(tr, i))
				if fields[3] != 0 || fields[4] != 0 {
					t.Fatalf("sample %d roll/pitch = %v/%v, want level ground heading", i, fields[3], fields[4])
				}
				if math.Abs(fields[5]-tt.want) > 0.01 {
					t.Fatalf("sample %d yaw = %v, want %v", i, fields[5], tt.want)
				}
			}
			if got := groundHeadingProperties(tr); !reflect.DeepEqual(got, []string{"WT_HeadingSource=PathDerivative"}) {
				t.Fatalf("heading properties = %v", got)
			}
		})
	}
}

func TestGroundHeadingUsesReviewedGeoreferenceAxes(t *testing.T) {
	oldProfile := activeGeographicProfile
	activeGeographicProfile = &terrainmap.GeographicProfile{
		Enabled:     true,
		LocalBounds: [4]float64{-1000, -1000, 1000, 1000},
		// A 180-degree axis orientation: local east is geographic west and
		// local north is geographic south.
		GeoBounds: [4]float64{40, 11, 38, 10},
	}
	t.Cleanup(func() { activeGeographicProfile = oldProfile })

	tests := []struct {
		name   string
		second acmiSample
		want   float64
	}{
		{name: "east becomes west", second: acmiSample{t: 2000, x: 100}, want: 270},
		{name: "north becomes south", second: acmiSample{t: 2000, z: 100}, want: 180},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &track{key: trackKey{id: 2, groundEntity: true}, samples: []acmiSample{{t: 1000}, tt.second}}
			got := groundPositionFields(t, groundPositionValue(tr, 0))[5]
			if math.Abs(got-tt.want) > 0.01 {
				t.Fatalf("georeferenced yaw = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGroundHeadingRejectsStopsSingletonsGapsAndStaticTracks(t *testing.T) {
	oldProfile := activeGeographicProfile
	activeGeographicProfile = nil
	t.Cleanup(func() { activeGeographicProfile = oldProfile })

	tests := []struct {
		name string
		tr   *track
	}{
		{name: "singleton", tr: &track{key: trackKey{id: 3, groundEntity: true}, samples: []acmiSample{{t: 1000, x: 4, y: 2, z: 6}}}},
		{name: "stationary", tr: &track{key: trackKey{id: 4, groundEntity: true}, samples: []acmiSample{{t: 1000, x: 4}, {t: 2000, x: 4}}}},
		{name: "gap", tr: &track{key: trackKey{id: 5, groundEntity: true}, samples: []acmiSample{{t: 1000}, {t: 4000, x: 20}}}},
		{name: "same timestamps", tr: &track{key: trackKey{id: 6, groundEntity: true}, samples: []acmiSample{{t: 1000}, {t: 1000, x: 20}}}},
		{name: "static", tr: &track{key: trackKey{id: 7, groundEntity: true}, groundStatic: true, samples: []acmiSample{{t: 1000}, {t: 2000, x: 20}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, sample := range tt.tr.samples {
				got := groundPositionValue(tt.tr, i)
				if want := acmiBombPositionValue(sample); got != want {
					t.Fatalf("sample %d = %q, want position-only %q", i, got, want)
				}
			}
			if props := groundHeadingProperties(tt.tr); len(props) != 0 {
				t.Fatalf("position-only track has heading properties: %v", props)
			}
		})
	}
}

func TestGroundHeadingBriefStopIsBoundedAndLongStopIsDropped(t *testing.T) {
	oldProfile := activeGeographicProfile
	activeGeographicProfile = nil
	t.Cleanup(func() { activeGeographicProfile = oldProfile })

	tr := &track{key: trackKey{id: 8, groundEntity: true}, samples: []acmiSample{
		{t: 1000, x: 0},
		{t: 2000, x: 10},
		{t: 3000, x: 10},
		{t: 4000, x: 10},
		{t: 5000, x: 10},
	}}
	for _, i := range []int{1, 2, 3} {
		if got := groundPositionFields(t, groundPositionValue(tr, i))[5]; math.Abs(got-90) > 0.01 {
			t.Fatalf("bounded stop sample %d yaw = %v, want last observed 90", i, got)
		}
	}
	if got, want := groundPositionValue(tr, 4), acmiBombPositionValue(tr.samples[4]); got != want {
		t.Fatalf("four-second stop retained stale yaw: %q, want %q", got, want)
	}
}

func TestGroundHeadingDoesNotBorrowPastTrackEnd(t *testing.T) {
	tr := &track{
		key: trackKey{id: 11, groundEntity: true}, end: 1000, hasEnd: true,
		samples: []acmiSample{{t: 1000, x: 0}, {t: 2000, x: 10}},
	}
	for i, sample := range tr.samples {
		if got, want := groundPositionValue(tr, i), acmiBombPositionValue(sample); got != want {
			t.Fatalf("sample %d borrowed a post-end heading: %q, want %q", i, got, want)
		}
	}
	if props := groundHeadingProperties(tr); len(props) != 0 {
		t.Fatalf("post-end movement produced heading properties: %v", props)
	}
}

func TestGroundHeadingCarryStopsAtChronologyDiscontinuity(t *testing.T) {
	tr := &track{key: trackKey{id: 12, groundEntity: true}, samples: []acmiSample{
		{t: 1000, x: 0},
		{t: 2000, x: 10},
		{t: 2000, x: 10},
		{t: 2100, x: 10},
	}}
	for _, i := range []int{2, 3} {
		if got, want := groundPositionValue(tr, i), acmiBombPositionValue(tr.samples[i]); got != want {
			t.Fatalf("sample %d carried across repeated timestamps: %q, want %q", i, got, want)
		}
	}
}

func TestGroundHeadingCarryScanIsBounded(t *testing.T) {
	samples := []acmiSample{{t: 1000, x: 0}, {t: 1010, x: 10}}
	for i := 0; i < groundHeadingMaxCarrySegments+1; i++ {
		samples = append(samples, acmiSample{t: uint32(1011 + i), x: 10})
	}
	tr := &track{key: trackKey{id: 13, groundEntity: true}, samples: samples}
	last := len(tr.samples) - 1
	if got, want := groundPositionValue(tr, last), acmiBombPositionValue(tr.samples[last]); got != want {
		t.Fatalf("long stopped run borrowed heading beyond scan cap: %q, want %q", got, want)
	}
}

func TestGroundHeadingDoesNotMutateSourceSamples(t *testing.T) {
	tr := &track{key: trackKey{id: 9, groundEntity: true}, samples: []acmiSample{
		{t: 1000, x: 0, y: 4, z: 0, roll: 12, pitch: -6, yaw: 321},
		{t: 2000, x: 10, y: 5, z: 0, roll: -8, pitch: 3, yaw: 17},
	}}
	original := append([]acmiSample(nil), tr.samples...)
	for i := range tr.samples {
		_ = groundPositionValue(tr, i)
	}
	_ = groundHeadingProperties(tr)
	if !reflect.DeepEqual(tr.samples, original) {
		t.Fatalf("source samples changed:\n got %+v\nwant %+v", tr.samples, original)
	}
}

func TestGroundHeadingAPIIgnoresInvalidInputs(t *testing.T) {
	if got := groundPositionValue(nil, 0); got != "" {
		t.Fatalf("nil track position = %q", got)
	}
	tr := &track{key: trackKey{id: 10, groundEntity: true}, samples: []acmiSample{{t: 1000}}}
	if got := groundPositionValue(tr, -1); got != "" {
		t.Fatalf("negative index position = %q", got)
	}
	if got := groundPositionValue(tr, 1); got != "" {
		t.Fatalf("out-of-range position = %q", got)
	}
	if props := groundHeadingProperties(nil); props != nil {
		t.Fatalf("nil track properties = %v", props)
	}
}

func groundPositionFields(t *testing.T, value string) []float64 {
	t.Helper()
	parts := strings.Split(value, "|")
	if len(parts) != 6 {
		t.Fatalf("position %q has %d components, want 6", value, len(parts))
	}
	fields := make([]float64, len(parts))
	for i, part := range parts {
		v, err := strconv.ParseFloat(part, 64)
		if err != nil {
			t.Fatalf("parsing component %d of %q: %v", i, value, err)
		}
		fields[i] = v
	}
	return fields
}
