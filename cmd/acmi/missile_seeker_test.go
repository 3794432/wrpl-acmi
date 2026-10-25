package main

import (
	"math"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func seekerACMITestBits(length int) []byte {
	return make([]byte, (length+7)/8)
}

func seekerACMITestPutLE(raw []byte, offset, width int, value uint64) {
	for byteIndex := 0; byteIndex*8 < width; byteIndex++ {
		wireByte := byte(value >> uint(byteIndex*8))
		for bit := 0; bit < 8 && byteIndex*8+bit < width; bit++ {
			position := offset + byteIndex*8 + bit
			mask := byte(1 << uint(7-position%8))
			if wireByte&(1<<uint(7-bit)) != 0 {
				raw[position/8] |= mask
			} else {
				raw[position/8] &^= mask
			}
		}
	}
}

func seekerACMITestPutMSB(raw []byte, offset, width int, value uint64) {
	for bit := 0; bit < width; bit++ {
		position := offset + bit
		mask := byte(1 << uint(7-position%8))
		if value&(1<<uint(width-1-bit)) != 0 {
			raw[position/8] |= mask
		} else {
			raw[position/8] &^= mask
		}
	}
}

func seekerACMITestRadar(length int, mode uint64, shift int, estimate [3]float32, los [3]int16, distance uint16) *projectiles.RawMotion {
	raw := seekerACMITestBits(length)
	seekerACMITestPutMSB(raw, 8, 3, mode)
	for i, value := range estimate {
		seekerACMITestPutLE(raw, 11+shift+i*32, 32, uint64(math.Float32bits(value)))
	}
	for i, value := range los {
		seekerACMITestPutLE(raw, 340+shift+i*16, 16, uint64(uint16(value)))
	}
	seekerACMITestPutLE(raw, 454+shift, 16, uint64(distance))
	return &projectiles.RawMotion{
		Complete:        true,
		HasTail:         true,
		HasSeekerBlock:  true,
		SeekerBitLength: uint16(length),
		SeekerRawBits:   raw,
	}
}

func seekerACMITestSample(motion *projectiles.RawMotion) acmiSample {
	return acmiSample{t: 1000, projectile: &projectiles.Sample{FromSync: true, RawMotion: motion}}
}

func seekerACMITestARH() *track {
	return &track{
		key:             trackKey{missile: true, id: 42},
		missileGuidance: &missileGuidanceInfo{Type: "ARH", Source: "MissileModelTable", Confidence: "high"},
	}
}

func seekerACMITestProps(t *testing.T, props []string) map[string]string {
	t.Helper()
	values := make(map[string]string, len(props))
	for _, prop := range props {
		key, value, ok := strings.Cut(prop, "=")
		if !ok {
			t.Fatalf("property has no equals sign: %q", prop)
		}
		if _, duplicate := values[key]; duplicate {
			t.Fatalf("duplicate property %q", key)
		}
		values[key] = value
	}
	return values
}

func TestMissileSeekerRadarSearchAndTrack(t *testing.T) {
	los := [3]int16{32767, 0, 0}
	estimate := [3]float32{1200, -250, 3500}
	t.Run("search does not assert native lock", func(t *testing.T) {
		props := seekerACMITestProps(t, missileSeekerProperties(seekerACMITestARH(), seekerACMITestSample(seekerACMITestRadar(607, 0b001, 0, estimate, los, 4321))))
		for key, want := range map[string]string{
			"WT_SeekerKind":     "Radar",
			"WT_SeekerState":    "Search",
			"WT_SeekerSource":   "WeaponSync",
			"WT_SeekerBits":     "607",
			"WT_SeekerRange":    "4321",
			"LockedTargetMode":  "0",
			"LockedTargetRange": "",
			"RadarMode":         "1",
		} {
			if props[key] != want {
				t.Fatalf("%s = %q, want %q in %v", key, props[key], want, props)
			}
		}
		if _, ok := props["LockedTarget"]; ok {
			t.Fatalf("search invented a target object: %v", props)
		}
	})

	t.Run("track maps ARH range and mode", func(t *testing.T) {
		props := seekerACMITestProps(t, missileSeekerProperties(seekerACMITestARH(), seekerACMITestSample(seekerACMITestRadar(607, 0b111, 0, estimate, los, 4321))))
		for key, want := range map[string]string{
			"WT_SeekerState":           "Track",
			"WT_SeekerLOSX":            "1.000000",
			"WT_SeekerLOSY":            "0.000000",
			"WT_SeekerLOSZ":            "0.000000",
			"WT_SeekerTargetEstimateX": "1200",
			"LockedTargetMode":         "1",
			"LockedTargetRange":        "4321",
			"RadarMode":                "1",
		} {
			if props[key] != want {
				t.Fatalf("%s = %q, want %q in %v", key, props[key], want, props)
			}
		}
	})
}

func TestMissileSeekerRadarTransitionUsesShiftedFields(t *testing.T) {
	props := seekerACMITestProps(t, missileSeekerProperties(seekerACMITestARH(), seekerACMITestSample(seekerACMITestRadar(639, 0b100, 32, [3]float32{10, 20, 30}, [3]int16{0, 32767, 0}, 987))))
	for key, want := range map[string]string{
		"WT_SeekerState":           "Track",
		"WT_SeekerBits":            "639",
		"WT_SeekerLOSY":            "1.000000",
		"WT_SeekerTargetEstimateZ": "30",
		"LockedTargetMode":         "1",
		"LockedTargetRange":        "987",
		"RadarMode":                "1",
	} {
		if props[key] != want {
			t.Fatalf("%s = %q, want %q in %v", key, props[key], want, props)
		}
	}
}

func TestMissileSeekerIRDoesNotInferLockFromTimer(t *testing.T) {
	raw := seekerACMITestBits(283)
	for i, value := range []int16{0, 32767, 0} {
		seekerACMITestPutLE(raw, 58+i*16, 16, uint64(uint16(value)))
	}
	motion := &projectiles.RawMotion{
		Complete:        true,
		HasTail:         true,
		HasSeekerBlock:  true,
		SeekerBitLength: 283,
		SeekerRawBits:   raw,
		HasSeekerValue:  true,
		SeekerValueBits: math.Float32bits(0),
	}
	props := seekerACMITestProps(t, missileSeekerProperties(seekerACMITestARH(), seekerACMITestSample(motion)))
	for key, want := range map[string]string{
		"WT_SeekerKind":     "IR",
		"WT_SeekerState":    "Unknown",
		"WT_SeekerLOSY":     "1.000000",
		"LockedTargetMode":  "0",
		"LockedTargetRange": "",
		"RadarMode":         "0",
	} {
		if props[key] != want {
			t.Fatalf("%s = %q, want %q in %v", key, props[key], want, props)
		}
	}
}

func TestMissileSeekerUnknownRetainsEvidenceAndClearsNative(t *testing.T) {
	motion := &projectiles.RawMotion{
		Complete:        true,
		HasTail:         true,
		HasSeekerBlock:  true,
		SeekerBitLength: 12,
		SeekerRawBits:   []byte{0xa0, 0x0c},
	}
	props := seekerACMITestProps(t, missileSeekerProperties(seekerACMITestARH(), seekerACMITestSample(motion)))
	for key, want := range map[string]string{
		"WT_SeekerKind":            "Unknown",
		"WT_SeekerState":           "Unknown",
		"WT_SeekerSource":          "WeaponSync",
		"WT_SeekerBits":            "12",
		"WT_SeekerLOSX":            "",
		"WT_SeekerTargetEstimateX": "",
		"LockedTargetMode":         "0",
		"LockedTargetRange":        "",
		"RadarMode":                "0",
	} {
		if props[key] != want {
			t.Fatalf("%s = %q, want %q in %v", key, props[key], want, props)
		}
	}
}

func TestMissileSeekerRejectsInvalidValues(t *testing.T) {
	motion := seekerACMITestRadar(607, 0b111, 0, [3]float32{float32(math.NaN()), 2, 3}, [3]int16{0, 12000, 0}, 0)
	props := seekerACMITestProps(t, missileSeekerProperties(seekerACMITestARH(), seekerACMITestSample(motion)))
	if props["WT_SeekerState"] != "Track" || props["WT_SeekerKind"] != "Radar" {
		t.Fatalf("valid mode was lost: %v", props)
	}
	for _, key := range []string{"WT_SeekerTargetEstimateX", "WT_SeekerTargetEstimateY", "WT_SeekerTargetEstimateZ", "WT_SeekerLOSX", "WT_SeekerLOSY", "WT_SeekerLOSZ"} {
		if props[key] != "" {
			t.Fatalf("invalid numeric %s = %q", key, props[key])
		}
	}
	if props["LockedTargetRange"] != "" {
		t.Fatalf("zero range was asserted: %v", props)
	}
}

func TestMissileSeekerStateClearsPreviousNativeValues(t *testing.T) {
	tr := seekerACMITestARH()
	first := seekerACMITestProps(t, missileSeekerProperties(tr, seekerACMITestSample(seekerACMITestRadar(607, 0b111, 0, [3]float32{1, 2, 3}, [3]int16{32767, 0, 0}, 500))))
	if first["LockedTargetMode"] != "1" || first["LockedTargetRange"] != "500" || first["RadarMode"] != "1" {
		t.Fatalf("setup did not establish native values: %v", first)
	}
	cleared := seekerACMITestProps(t, missileSeekerProperties(tr, acmiSample{projectile: &projectiles.Sample{FromSync: true}}))
	if cleared["LockedTargetMode"] != "0" || cleared["LockedTargetRange"] != "" || cleared["RadarMode"] != "0" {
		t.Fatalf("missing data did not clear native values: %v", cleared)
	}
	for _, key := range []string{"WT_SeekerKind", "WT_SeekerState", "WT_SeekerSource", "WT_SeekerLOSX", "WT_SeekerTargetEstimateX"} {
		if cleared[key] != "" {
			t.Fatalf("stale %s = %q", key, cleared[key])
		}
	}
}

func TestMissileSeekerRadarModeRequiresExplicitARH(t *testing.T) {
	motion := seekerACMITestRadar(607, 0b111, 0, [3]float32{1, 2, 3}, [3]int16{32767, 0, 0}, 500)
	tests := []struct {
		name  string
		track *track
		want  string
	}{
		{name: "SARH", track: &track{key: trackKey{missile: true}, missileGuidance: &missileGuidanceInfo{Type: "SARH", Confidence: "confirmed"}}, want: "0"},
		{name: "inferred ARH", track: &track{key: trackKey{missile: true}, missileGuidance: &missileGuidanceInfo{Type: "ARH", Confidence: "inferred"}}, want: "0"},
		{name: "explicit template ARH", track: &track{key: trackKey{missile: true}, projectileSource: &projectiles.Projectile{Template: "arh_shell"}}, want: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := seekerACMITestProps(t, missileSeekerProperties(tt.track, seekerACMITestSample(motion)))
			if props["RadarMode"] != tt.want {
				t.Fatalf("RadarMode = %q, want %q in %v", props["RadarMode"], tt.want, props)
			}
			if props["LockedTargetMode"] != "1" {
				t.Fatalf("radar Track did not map LockedTargetMode: %v", props)
			}
		})
	}
}
