package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
)

func setTestTerrainLayout(t *testing.T, layout terrainLayout) {
	t.Helper()
	old := *terrainLayoutName
	*terrainLayoutName = string(layout)
	t.Cleanup(func() { *terrainLayoutName = old })
}

func TestAtlasWriterKeepsAbsoluteTWithoutDoubleReferencing(t *testing.T) {
	setTestTerrainLayout(t, terrainLayoutAtlas)
	oldLat, oldLon, oldAlt := *lat0, *lon0, *alt0
	oldCenterX, oldCenterZ := *centerX, *centerZ
	*lat0, *lon0, *alt0 = -60, 100, 1000
	*centerX, *centerZ = 0, 0
	t.Cleanup(func() {
		*lat0, *lon0, *alt0 = oldLat, oldLon, oldAlt
		*centerX, *centerZ = oldCenterX, oldCenterZ
	})

	air := &track{
		key: trackKey{air: true, id: 1},
		samples: []acmiSample{{
			t: 1000, x: 1250, y: 23.5, z: 600,
			roll: 12.5, pitch: -7.25, yaw: 350,
		}},
	}
	missile := &track{
		key:  trackKey{missile: true, id: 2},
		name: "R-73",
		samples: []acmiSample{
			{t: 1000, x: 0, y: 10, z: 30},
			{t: 2000, x: 100, y: 20, z: 30},
			{t: 3000, x: 200, y: 30, z: 30},
		},
	}
	path := filepath.Join(t.TempDir(), "atlas.acmi")
	if err := writeACMIWithEventsMapID(path, wrpl.WRPLHeader{}, []*track{air, missile}, nil, nil, nil, "air_grand_canyon"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, line := range []string{
		"0,ReferenceLongitude=0.0000000",
		"0,ReferenceLatitude=0.0000000",
		"0,ReferenceAltitude=0.0",
		"0,MapId=WarThunder.Atlas1.air_grand_canyon",
		"0,WT_TerrainLayout=AtlasV1",
	} {
		if !strings.Contains(text, line+"\n") {
			t.Fatalf("missing %q in:\n%s", line, text)
		}
	}

	airFields := acmiFields(t, text, "1")
	lat := -60 + 600/111320.0
	lon := 100 + 1250/(111320.0*math.Cos(lat*math.Pi/180))
	want := []float64{lon, lat, 1023.5, 12.5, -7.25, 350}
	if len(airFields) != len(want) {
		t.Fatalf("aircraft T has %d fields, want %d: %v", len(airFields), len(want), airFields)
	}
	for i := range want {
		if math.Abs(airFields[i]-want[i]) > 1e-6 {
			t.Fatalf("aircraft T field %d = %v, want %v; anchor must be applied once", i, airFields[i], want[i])
		}
	}

	missileFields := acmiFields(t, text, "2")
	if len(missileFields) != 5 || math.Abs(missileFields[3]) > 1e-9 || math.Abs(missileFields[4]-30) > 1e-9 {
		t.Fatalf("missile native U/V fields changed: %v", missileFields)
	}
	if !strings.Contains(text, "WT_Speed=100.498756") ||
		!strings.Contains(text, "WT_GroundSpeed=100.000000") ||
		!strings.Contains(text, "WT_ClimbRate=10.000000") {
		t.Fatalf("missile speed was not preserved:\n%s", text)
	}
}

func TestLegacyWriterKeepsManualAbsoluteTWithZeroReferences(t *testing.T) {
	setTestTerrainLayout(t, terrainLayoutLegacy)
	oldLat, oldLon, oldAlt := *lat0, *lon0, *alt0
	oldCenterX, oldCenterZ := *centerX, *centerZ
	*lat0, *lon0, *alt0 = 10, 20, 30
	*centerX, *centerZ = 5, 6
	t.Cleanup(func() {
		*lat0, *lon0, *alt0 = oldLat, oldLon, oldAlt
		*centerX, *centerZ = oldCenterX, oldCenterZ
	})

	air := &track{
		key:     trackKey{air: true, id: 1},
		samples: []acmiSample{{t: 1000, x: 5, y: 7, z: 6, roll: 1, pitch: 2, yaw: 3}},
	}
	path := filepath.Join(t.TempDir(), "legacy.acmi")
	if err := writeACMIWithEventsMapID(path, wrpl.WRPLHeader{}, []*track{air}, nil, nil, nil, "spain"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	fields := acmiFields(t, text, "1")
	want := []float64{20, 10, 37, 1, 2, 3}
	for i := range want {
		if math.Abs(fields[i]-want[i]) > 1e-6 {
			t.Fatalf("legacy T field %d = %v, want %v", i, fields[i], want[i])
		}
	}
	for _, line := range []string{
		"0,ReferenceLongitude=0.0000000",
		"0,ReferenceLatitude=0.0000000",
		"0,ReferenceAltitude=0.0",
		"0,MapId=WarThunder.spain",
	} {
		if !strings.Contains(text, line+"\n") {
			t.Fatalf("missing %q in:\n%s", line, text)
		}
	}
}

func acmiFields(t *testing.T, data, id string) []float64 {
	t.Helper()
	for _, line := range strings.Split(data, "\n") {
		if !strings.HasPrefix(line, id+",") {
			continue
		}
		index := strings.Index(line, "T=")
		if index < 0 {
			continue
		}
		value := line[index+len("T="):]
		if comma := strings.IndexByte(value, ','); comma >= 0 {
			value = value[:comma]
		}
		parts := strings.Split(value, "|")
		fields := make([]float64, len(parts))
		for i, part := range parts {
			number, err := strconv.ParseFloat(part, 64)
			if err != nil {
				t.Fatalf("parsing track %s field %q: %v", id, part, err)
			}
			fields[i] = number
		}
		return fields
	}
	t.Fatalf("no T line for track %s in:\n%s", id, data)
	return nil
}
