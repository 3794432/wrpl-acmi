package main

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func bombTrajectoryTestTracks() (*track, *track) {
	parent := &track{key: trackKey{air: true, id: 11}, samples: []acmiSample{
		{t: 1000, x: 0}, {t: 2000, x: 100}, {t: 6000, x: 500}}}
	bomb := bombModelTestTrack(1, 100, 11, 5000)
	bomb.parent = parent
	bomb.projectileSource.SpawnSample.Time = 2000
	bomb.projectileSource.SpawnSample.X = 10
	bomb.projectileSource.SpawnSample.Y = 1000
	bomb.projectileSource.SpawnSample.Z = 30
	bomb.samples = []acmiSample{{t: 2000, x: 10, y: 1000, z: 30}}
	bomb.bombPositionOnly = true
	return parent, bomb
}

func TestBombEstimateUsesParentVelocityAndGravityWithHonestProperties(t *testing.T) {
	parent, bomb := bombTrajectoryTestTracks()
	before := *bomb.projectileSource
	if got := applyBombTrajectoryEstimates([]*track{parent, bomb}); got != 1 {
		t.Fatalf("estimated = %d", got)
	}
	if bomb.bombPositionOnly || bomb.bombEstimate == nil || bomb.bombEstimate.velocity != [3]float64{100, 0, 0} {
		t.Fatal("initial velocity/source flag missing")
	}
	last := bomb.samples[len(bomb.samples)-1]
	if last.t != 5000 || last.x != 310 || math.Abs(last.y-(1000-0.5*bombEstimateGravity*9)) > 1e-9 || last.z != 30 {
		t.Fatalf("ballistic endpoint = %+v", last)
	}
	if !reflect.DeepEqual(before, *bomb.projectileSource) {
		t.Fatal("estimated motion modified recorded evidence")
	}
	data := renderTestACMI(t, []*track{parent, bomb}, nil, nil)
	for _, want := range []string{"WT_BombTrajectoryEstimated=1", "WT_BombTrajectoryAvailable=0", "WT_BombPositionSource=EstimatedBallistic", "Label=Estimated ballistic trajectory", "WT_BombRecordedEndTimeMS=5000"} {
		if !strings.Contains(data, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(data, "Label=Release position only") {
		t.Fatal("moving estimate still labelled a stationary release marker")
	}
}

func TestBombEstimateLeavesRealTrajectoriesAndUnsupportedMarkersUntouched(t *testing.T) {
	parent, real := bombTrajectoryTestTracks()
	real.bombPositionOnly = false
	before := append([]acmiSample(nil), real.samples...)
	_, missing := bombTrajectoryTestTracks()
	missing.parent = nil
	_, below := bombTrajectoryTestTracks()
	below.projectileSource.SpawnSample.Y = -10
	if got := applyBombTrajectoryEstimates([]*track{parent, real, missing, below}); got != 0 {
		t.Fatalf("unjustified estimates = %d", got)
	}
	if !reflect.DeepEqual(before, real.samples) || !missing.bombPositionOnly || !below.bombPositionOnly {
		t.Fatal("real/missing/negative-altitude evidence changed")
	}
}

func TestBombEstimateStopsAtSeaLevelWithoutInventingImpactTime(t *testing.T) {
	parent, bomb := bombTrajectoryTestTracks()
	for i := range parent.samples {
		parent.samples[i].x = 0
	}
	bomb.projectileSource.SpawnSample.Y = 10
	bomb.samples[0].y = 10
	applyBombTrajectoryEstimates([]*track{parent, bomb})
	if bomb.bombEstimate == nil || bomb.bombEstimate.endReason != "SeaLevelVisualCutoff" || bomb.end >= bomb.projectileSource.EndTime {
		t.Fatal("sea-level cutoff/source destruction distinction missing")
	}
	for _, s := range bomb.samples {
		if s.y < 0 || s.t > bomb.end {
			t.Fatalf("estimate continued below cutoff: %+v", s)
		}
	}
}

func TestBombEstimateSeaLevelUsesExportAltitudeOffset(t *testing.T) {
	previous := *alt0
	*alt0 = 100
	t.Cleanup(func() { *alt0 = previous })
	parent, bomb := bombTrajectoryTestTracks()
	for i := range parent.samples {
		parent.samples[i].x = 0
	}
	bomb.projectileSource.SpawnSample.Y = -90 // Exported altitude is +10 m.
	bomb.samples[0].y = -90
	applyBombTrajectoryEstimates([]*track{parent, bomb})
	if bomb.bombEstimate == nil || bomb.bombEstimate.endReason != "SeaLevelVisualCutoff" {
		t.Fatal("positive exported altitude was rejected")
	}
	last := bomb.samples[len(bomb.samples)-1]
	if last.y+*alt0 < 0 || last.y+*alt0 > 0.02 {
		t.Fatalf("cutoff ignored altitude offset: %+v", last)
	}
}

func TestBombReleaseVelocityRejectsMissingStaleRecycledAndJumpingParents(t *testing.T) {
	for _, name := range []string{"one sample", "dead sortie", "stale", "jump", "nan", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			parent, _ := bombTrajectoryTestTracks()
			switch name {
			case "one sample":
				parent.samples = parent.samples[:1]
			case "dead sortie":
				parent.hasEnd, parent.end = true, 1500
			case "stale":
				parent.samples = []acmiSample{{t: 0}, {t: 100}}
			case "jump":
				parent.samples[1].x = 50000
			case "nan":
				parent.samples[1].x = math.NaN()
			case "duplicate":
				parent.samples = []acmiSample{{t: 2000}, {t: 2000, x: 1}}
			}
			if _, ok := bombReleaseVelocity(parent, 2000); ok {
				t.Fatal("unsafe release velocity accepted")
			}
		})
	}
}

func TestBombEstimateHasBoundedSampleCountAndRespectsReplayEnd(t *testing.T) {
	parent, bomb := bombTrajectoryTestTracks()
	bomb.projectileSource.SpawnSample.Y = 1000000
	bomb.samples[0].y = 1000000
	bomb.end, bomb.projectileSource.EndTime = 1000000, 1000000
	applyBombTrajectoryEstimates([]*track{parent, bomb})
	if bomb.end != 182000 || len(bomb.samples) != 901 || bomb.bombEstimate.endReason != "DurationLimit" {
		t.Fatalf("unbounded path: end=%d samples=%d", bomb.end, len(bomb.samples))
	}
	parent, bomb = bombTrajectoryTestTracks()
	parent.samples = append(parent.samples[:2], acmiSample{t: 3500})
	bomb.hasEnd, bomb.projectileSource.HasEnd = false, false
	applyBombTrajectoryEstimates([]*track{parent, bomb})
	if bomb.end != 3500 || bomb.bombEstimate.endReason != "ReplayEnd" {
		t.Fatal("estimate extended past the available recording")
	}
}
