package main

import (
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func missileINSTestSample(raw float32, fromSync, complete bool) acmiSample {
	return acmiSample{projectile: &projectiles.Sample{
		FromSync: fromSync,
		RawMotion: &projectiles.RawMotion{
			Complete:        complete,
			HasTail:         true,
			HasSeekerValue:  true,
			SeekerValueBits: math.Float32bits(raw),
		},
	}}
}

func missileINSTestProperty(props []string, name string) (string, bool) {
	prefix := name + "="
	for _, prop := range props {
		if strings.HasPrefix(prop, prefix) {
			return strings.TrimPrefix(prop, prefix), true
		}
	}
	return "", false
}

func TestMissileINSSeekerTickConversion(t *testing.T) {
	for _, tt := range []struct {
		raw     float32
		seconds string
		label   string
	}{
		{raw: 0, seconds: "0.000000", label: "INS 0.00 s"},
		{raw: 0.3125, seconds: "0.312500", label: "INS 0.31 s"},
		{raw: 2.5, seconds: "2.500000", label: "INS 2.50 s"},
		{raw: 48, seconds: "48.000000", label: "INS 48.00 s"},
	} {
		props := missileFlightPropertiesWithINS(missileFlightSample{}, missileINSTestSample(tt.raw, true, true))
		joined := strings.Join(props, ",")
		seeker, _ := missileINSTestProperty(props, "WT_SeekerTimer")
		seconds, _ := missileINSTestProperty(props, "WT_InertialTime")
		valid, _ := missileINSTestProperty(props, "WT_InertialTimeValid")
		if valid != "1" || seconds != tt.seconds || !strings.Contains(joined, "Label="+tt.label) {
			t.Fatalf("raw %v: props=%s", tt.raw, joined)
		}
		if seeker != formatMissileINSTestRaw(tt.raw) {
			t.Fatalf("raw %v: WT_SeekerTimer=%q", tt.raw, seeker)
		}
		source, ok := missileINSTestProperty(props, "WT_InertialTimeSource")
		if !ok || source != "SeekerTimer48HzInferred" || strings.Count(joined, "WT_InertialTimeSource=") != 1 {
			t.Fatalf("missing single timer source: %s", joined)
		}
	}
}

func formatMissileINSTestRaw(raw float32) string {
	return strconv.FormatFloat(float64(raw), 'g', -1, 32)
}

func TestMissileINSInvalidSourcesClearValues(t *testing.T) {
	nan := missileINSTestSample(float32(math.NaN()), true, true)
	negative := missileINSTestSample(-1, true, true)
	tests := []struct {
		name   string
		sample acmiSample
	}{
		{name: "missing projectile"},
		{name: "missing raw motion", sample: acmiSample{projectile: &projectiles.Sample{FromSync: true}}},
		{name: "not from sync", sample: missileINSTestSample(48, false, true)},
		{name: "incomplete tail", sample: missileINSTestSample(48, true, false)},
		{name: "NaN", sample: nan},
		{name: "negative", sample: negative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := missileFlightPropertiesWithINS(missileFlightSample{Valid: true, Speed: 10}, tt.sample)
			joined := strings.Join(props, ",")
			for _, want := range []string{
				"WT_SeekerTimer=,", "WT_InertialTime=,", "WT_InertialTimeValid=0",
				"INS -- s",
			} {
				if !strings.Contains(joined, want) {
					t.Fatalf("missing clear %q: %s", want, joined)
				}
			}
			for _, forbidden := range []string{"WT_SeekerOn=", "WT_TrackFresh="} {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("unsupported inference %q: %s", forbidden, joined)
				}
			}
		})
	}
}

func TestMissileINSGapKeepsValidTimer(t *testing.T) {
	valid := missileINSTestSample(2.5, true, true)
	samples := []acmiSample{
		{t: 1000, x: 0},
		{t: 2000, x: 10},
		{t: 4001, x: 20, projectile: valid.projectile},
	}
	flight := enrichRecordedMissileFlight(samples)
	if flight[2].Valid {
		t.Fatal("test setup did not cross the position sampling gap")
	}
	props := missileFlightPropertiesWithINS(flight[2], samples[2])
	joined := strings.Join(props, ",")
	for _, want := range []string{"WT_FlightValid=0", "WT_InertialTimeValid=1", "WT_InertialTime=2.500000", "INS 2.50 s"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q: %s", want, joined)
		}
	}
}

func TestMissileINSResetUsesCurrentRawZero(t *testing.T) {
	props := []string{}
	for _, raw := range []float32{2.5, 0} {
		props = missileFlightPropertiesWithINS(missileFlightSample{}, missileINSTestSample(raw, true, true))
	}
	joined := strings.Join(props, ",")
	if !strings.Contains(joined, "WT_SeekerTimer=0,") ||
		!strings.Contains(joined, "WT_InertialTime=0.000000") ||
		!strings.Contains(joined, "INS 0.00 s") {
		t.Fatalf("reset retained the previous timer: %s", joined)
	}
}

func TestMissileINSLabelIsSingleAndPreservesKinematics(t *testing.T) {
	flight := missileFlightSample{Valid: true, Speed: 321.5, ClimbRate: -7.25, HasAcceleration: true, AccelMagnitude: 12.5}
	props := missileFlightPropertiesWithINS(flight, acmiSample{})
	joined := strings.Join(props, ",")
	if strings.Count(joined, "Label=") != 1 {
		t.Fatalf("Label count is not one: %s", joined)
	}
	for _, want := range []string{"WT_FlightValid=1", "WT_Speed=321.500000", "Label=INS -- s"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing property %q: %s", want, joined)
		}
	}
	for _, forbidden := range []string{"Speed:", "climb:", "accel:", "INS:"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("unexpected display text %q: %s", forbidden, joined)
		}
	}
}

func TestMissileINSIntegratedACMIUpdate(t *testing.T) {
	first := acmiSample{t: 1000, x: 0, y: 0, z: 0}
	second := missileINSTestSample(48, true, true)
	second.t, second.x, second.y, second.z = 1100, 10, 0, 0
	third := acmiSample{t: 1200, x: 20, y: 0, z: 0, projectile: &projectiles.Sample{FromSync: true}}
	tr := &track{key: trackKey{missile: true, id: 42}, samples: []acmiSample{first, second, third}}
	data := renderTestACMI(t, []*track{tr}, nil, nil)
	if !strings.Contains(data, "WT_SeekerTimer=48,") ||
		!strings.Contains(data, "WT_InertialTime=48.000000") ||
		!strings.Contains(data, "INS 48.00 s") {
		t.Fatalf("valid timer missing from ACMI update:\n%s", data)
	}
	lastFrame := data[strings.LastIndex(data, "#0.200"):]
	if !strings.Contains(lastFrame, "WT_SeekerTimer=,") ||
		!strings.Contains(lastFrame, "WT_InertialTime=,") ||
		!strings.Contains(lastFrame, "WT_InertialTimeValid=0") {
		t.Fatalf("missing timer did not clear in ACMI update:\n%s", lastFrame)
	}
}

func TestMissileINSCSVAppendsColumnsAndKeepsRawEvidence(t *testing.T) {
	valid := missileINSTestSample(0.3125, true, true)
	first := acmiSample{t: 1000, x: 1, projectile: valid.projectile}
	second := acmiSample{t: 1100, x: 2, projectile: &projectiles.Sample{FromSync: true}}
	path := filepath.Join(t.TempDir(), "missile.csv")
	if err := writeMissileFlightLog(path, []*track{{key: trackKey{missile: true, id: 42}, samples: []acmiSample{first, second}}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	header := rows[0]
	if got := strings.Join(header[len(header)-3:], ","); got != "WT_SeekerTimer,WT_InertialTime,WT_InertialTimeValid" {
		t.Fatalf("new CSV suffix = %q", got)
	}
	rawIndex := 7 + len(missileFlightFields)
	if header[rawIndex] != "recorded_sample_json" || header[rawIndex+1] != "creation_evidence_json" {
		t.Fatalf("existing CSV evidence columns moved: %v", header)
	}
	wantRaw, err := json.Marshal(first.projectile)
	if err != nil {
		t.Fatal(err)
	}
	if rows[1][rawIndex] != string(wantRaw) {
		t.Fatalf("raw sample JSON changed: got %s want %s", rows[1][rawIndex], wantRaw)
	}
	if got := strings.Join(rows[1][len(header)-3:], ","); got != "0.3125,0.312500,1" {
		t.Fatalf("valid INS CSV values = %q", got)
	}
	if got := strings.Join(rows[2][len(header)-3:], ","); got != ",,0" {
		t.Fatalf("missing INS CSV values = %q", got)
	}
}
