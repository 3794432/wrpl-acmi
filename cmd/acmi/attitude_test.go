package main

import (
	"math"
	"testing"

	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
)

func TestAircraftRollToACMIInvertsWarThunderBankSign(t *testing.T) {
	tests := []struct {
		name    string
		bankDeg float64
		want    float64
	}{
		{name: "right bank in War Thunder", bankDeg: -30, want: 30},
		{name: "left bank in War Thunder", bankDeg: 30, want: -30},
		{name: "wings level", bankDeg: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := aircraftRollToACMI(tt.bankDeg); got != tt.want {
				t.Fatalf("aircraftRollToACMI(%v) = %v, want %v", tt.bankDeg, got, tt.want)
			}
		})
	}
}

func TestCollectTracksProjectsPackedBankIntoTacviewTurnDirection(t *testing.T) {
	const (
		rightTurnUID = uint64(41)
		leftTurnUID  = uint64(42)
	)

	rightPositions := [][2]float32{{0, 0}, {0, 100}, {100, 100}}
	leftPositions := [][2]float32{{0, 0}, {0, 100}, {-100, 100}}
	fm := &packetfm.PacketFlightModelParser{}
	for i := range rightPositions {
		fm.Results = append(fm.Results, packetfm.FMUpdatePacket{
			CurrentTime: uint32(1000 + i*250),
			Entries: []*packetfm.FMEntry{
				{
					UID: rightTurnUID,
					Data: &packetfm.FMData{
						PosX: rightPositions[i][0], PosZ: rightPositions[i][1],
						EulerBytes: testPackedEuler(0, 0, -30),
					},
				},
				{
					UID: leftTurnUID,
					Data: &packetfm.FMData{
						PosX: leftPositions[i][0], PosZ: leftPositions[i][1],
						EulerBytes: testPackedEuler(0, 0, 30),
					},
				},
			},
		})
	}

	tracks := collectTracks(fm, nil, nil, nil)
	if len(tracks) != 2 {
		t.Fatalf("collectTracks returned %d tracks, want 2", len(tracks))
	}
	byUID := make(map[uint64]*track, len(tracks))
	for _, tr := range tracks {
		byUID[tr.key.id] = tr
	}
	assertBankFollowsPositionalTurn(t, byUID[rightTurnUID], rightPositions, 1)
	assertBankFollowsPositionalTurn(t, byUID[leftTurnUID], leftPositions, -1)
}

func assertBankFollowsPositionalTurn(t *testing.T, tr *track, positions [][2]float32, turnSign int) {
	t.Helper()
	if tr == nil || len(tr.samples) != len(positions) {
		t.Fatalf("track samples = %+v, want one sample per position", tr)
	}
	firstCourse := testBearing(positions[0], positions[1])
	secondCourse := testBearing(positions[1], positions[2])
	turn := testWrap180(secondCourse - firstCourse)
	if math.Abs(turn-float64(turnSign)*90) > 1e-6 {
		t.Fatalf("synthetic positional turn = %v degrees, want %+d90", turn, turnSign)
	}
	for i, sample := range tr.samples {
		if sample.roll*turn <= 0 {
			t.Fatalf("sample %d roll=%v disagrees with positional turn=%v", i, sample.roll, turn)
		}
		if sample.pitch != 0 {
			t.Fatalf("sample %d pitch=%v, want unchanged zero", i, sample.pitch)
		}
	}
}

func testPackedEuler(headingDeg, pitchDeg, bankDeg float64) uint32 {
	var packed uint32
	if headingDeg < 0 {
		packed |= 1 << 31
		headingDeg = -headingDeg
	}
	if pitchDeg < 0 {
		packed |= 1 << 30
		pitchDeg = -pitchDeg
	}
	if bankDeg < 0 {
		packed |= 1 << 29
		bankDeg = -bankDeg
	}
	heading := quantizeTestAngle(headingDeg, 180, 1023)
	pitch := quantizeTestAngle(pitchDeg, 90, 511)
	bank := quantizeTestAngle(bankDeg, 180, 1023)
	return packed | heading<<19 | pitch<<10 | bank
}

func quantizeTestAngle(degrees, fullRange float64, max uint32) uint32 {
	value := uint32(math.Round(degrees / fullRange * float64(max)))
	if value > max {
		return max
	}
	return value
}

func testBearing(from, to [2]float32) float64 {
	bearing := math.Atan2(float64(to[0]-from[0]), float64(to[1]-from[1])) * 180 / math.Pi
	if bearing < 0 {
		bearing += 360
	}
	return bearing
}

func testWrap180(degrees float64) float64 {
	return math.Mod(degrees+180, 360) - 180
}
