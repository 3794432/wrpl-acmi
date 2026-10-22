package main

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestAirfieldMarkersRenderOfficialTypesGeometryAndHeading(t *testing.T) {
	oldLat, oldLon, oldAlt, oldProfile := atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile
	atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile = 35, 135, 500, nil
	t.Cleanup(func() {
		atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile = oldLat, oldLon, oldAlt, oldProfile
	})

	name := "东京机场, Alpha\nOne"
	markers := []airfieldMarker{
		{
			Key: "airport", Name: name, Role: "Airport", Source: "mission,source", Confidence: "high",
			MissionName: "Training, 東京", Prefab: "airport,alpha", GeometrySource: "mission airfield",
			T: 10, X: 1250, Y: 100, Z: 600, Heading: 275.5, HasHeading: true,
			Length: 2500, Width: 60, Team: 1, UnitUIDs: []uint64{9001, 9002},
		},
		{
			Key: "approach", Name: "Runway 27 approach", Role: "Approach", ParentKey: "airport",
			T: 20, X: 500, Y: 25, Z: -250, Team: 2,
		},
	}
	lines := airfieldLines(markers, 4)
	if len(lines) != 2 || len(lines[10]) != 1 || len(lines[20]) != 1 {
		t.Fatalf("unexpected line groups: %#v", lines)
	}

	airport := airfieldLineWithID(t, lines, "5")
	for _, want := range []string{
		`Name=东京机场\, Alpha\nOne`, `CallSign=东京机场\, Alpha\nOne`,
		"Type=Ground+Static+Aerodrome", "Coalition=Blue,Color=Blue,Group=War Thunder Blue",
		`WT_AirfieldSource=mission\,source`, "WT_AirfieldConfidence=high",
		`WT_AirfieldMissionName=Training\, 東京`, `WT_AirfieldPrefab=airport\,alpha`,
		"WT_AirfieldGeometrySource=mission airfield", "WT_AirfieldLength=2500",
		"WT_AirfieldWidth=60", "WT_AirfieldUnitUIDs=9001;9002", "WT_AirfieldPositionSource=AirfieldMarker",
	} {
		if !strings.Contains(airport, want) {
			t.Errorf("airport line is missing %q: %s", want, airport)
		}
	}
	position := airfieldPositionFields(t, airport)
	wantLat, wantLon := terrainPosition(1250, 600)
	for i, want := range []float64{wantLon, wantLat, 600, 0, 0, 275.5} {
		if math.Abs(position[i]-want) > 1e-6 {
			t.Errorf("position field %d = %v, want %v: %v", i, position[i], want, position)
		}
	}

	approach := airfieldLineWithID(t, lines, "6")
	for _, want := range []string{
		"Parent=5", "Type=Navaid+Static+Waypoint", "Coalition=Red,Color=Red,Group=War Thunder Red",
	} {
		if !strings.Contains(approach, want) {
			t.Errorf("approach line is missing %q: %s", want, approach)
		}
	}
	if got := len(airfieldPositionFields(t, approach)); got != 3 {
		t.Fatalf("approach without heading has %d position fields, want 3", got)
	}
	for _, line := range []string{airport, approach} {
		if strings.Contains(line, "Event=") || strings.Contains(line, "Timeout=") {
			t.Fatalf("static marker has a synthetic lifecycle event: %s", line)
		}
	}
}

func TestAirfieldMarkerWithoutHeadingOmitsSyntheticAttitude(t *testing.T) {
	markers := []airfieldMarker{{Key: "threshold", Name: "Threshold", Role: "Threshold", T: 7, X: 10, Y: 20, Z: 30, Heading: math.NaN()}}
	lines := airfieldLines(markers, 0)
	line := airfieldLineWithID(t, lines, "1")
	fields := airfieldPositionFields(t, line)
	if len(fields) != 3 {
		t.Fatalf("position has %d fields, want 3: %v", len(fields), fields)
	}
	if !strings.HasPrefix(line, "1,Name=Threshold,CallSign=Threshold,") {
		t.Fatalf("unexpected object line: %s", line)
	}
}

func TestAirfieldEvidenceEscapesACMIValuesAndPreservesUnicode(t *testing.T) {
	friendly := "東京, Field\\One\r\nTwo"
	marker := airfieldMarker{
		Key: "unicode", Name: friendly, Role: "Navaid", Source: "evidence,東京\\path",
		MissionName: "mission\n東京", Prefab: "prefab,東京", GeometrySource: "geometry\r東京",
		T: 3, X: 1, Y: 2, Z: 3,
	}
	line := airfieldLineWithID(t, airfieldLines([]airfieldMarker{marker}, 0), "1")
	for _, want := range []string{
		`Name=東京\, Field\\One\r\nTwo`,
		`WT_AirfieldSource=evidence\,東京\\path`,
		`WT_AirfieldMissionName=mission\n東京`,
		`WT_AirfieldPrefab=prefab\,東京`,
		`WT_AirfieldGeometrySource=geometry\r東京`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("escaped evidence %q is missing: %s", want, line)
		}
	}
	if strings.Contains(line, "\r") || strings.Contains(line, "\n") {
		t.Fatalf("raw line break remained in object line: %q", line)
	}
}

func TestAirfieldIDsAndParentsUseUppercaseHexAndOriginalPositions(t *testing.T) {
	markers := []airfieldMarker{
		{Key: "parent", Role: "Airport", T: 100, X: 1, Y: 2, Z: 3},
		{Key: "child", Role: "Approach", ParentKey: "parent", T: 101, X: 4, Y: 5, Z: 6},
		{Key: "missing-parent", Role: "Approach", ParentKey: "not-retained", T: 102, X: 7, Y: 8, Z: 9},
	}
	lines := airfieldLines(markers, 14)
	parent := airfieldLineWithID(t, lines, "F")
	child := airfieldLineWithID(t, lines, "10")
	orphan := airfieldLineWithID(t, lines, "11")
	if !strings.HasPrefix(parent, "F,") || !strings.Contains(child, "Parent=F,") {
		t.Fatalf("parent reference is incorrect: parent=%s child=%s", parent, child)
	}
	if strings.Contains(orphan, "Parent=") {
		t.Fatalf("marker referenced a non-retained key: %s", orphan)
	}
	if len(lines[100]) != 1 || len(lines[101]) != 1 || len(lines[102]) != 1 {
		t.Fatalf("marker was not emitted at its own T: %#v", lines)
	}
}

func TestAirfieldInvalidAndDuplicateMarkersAreRejectedSafely(t *testing.T) {
	markers := []airfieldMarker{
		{Key: "nan", Role: "Airport", T: 1, X: math.NaN(), Z: 3},
		{Key: "", Role: "Airport", T: 2, X: 1, Z: 3},
		{Key: "valid", Role: "Airport", T: 3, X: 1, Z: 3},
		{Key: "valid", Role: "Approach", T: 4, X: 2, Z: 4},
		{Key: "bad-heading", Role: "Approach", HasHeading: true, Heading: math.Inf(1), T: 5, X: 1, Z: 3},
		{Key: "max-geometry", Role: "Airport", Length: 50_000, Width: 1_000, T: 6, X: 1, Z: 3},
		{Key: "bad-geometry", Role: "Airport", Length: 50_001, Width: 1_001, T: 7, X: 1, Z: 3},
		{Key: "infinite-width", Role: "Airport", Length: 100, Width: math.Inf(1), T: 8, X: 1, Z: 3},
	}
	lines := airfieldLines(markers, 2)
	valid := airfieldLineWithID(t, lines, "5")
	maxGeometry := airfieldLineWithID(t, lines, "8")
	badGeometry := airfieldLineWithID(t, lines, "9")
	infiniteWidth := airfieldLineWithID(t, lines, "A")
	if !strings.Contains(maxGeometry, "WT_AirfieldLength=50000") || !strings.Contains(maxGeometry, "WT_AirfieldWidth=1000") {
		t.Fatalf("conservative geometry bounds were not retained: %s", maxGeometry)
	}
	for _, bad := range []string{"WT_AirfieldLength=", "WT_AirfieldWidth="} {
		if strings.Contains(badGeometry, bad) {
			t.Fatalf("invalid geometry %q was rendered: %s", bad, badGeometry)
		}
	}
	if strings.Contains(infiniteWidth, "WT_AirfieldWidth=") || !strings.Contains(infiniteWidth, "WT_AirfieldLength=100") {
		t.Fatalf("non-finite width was rendered or valid length was dropped: %s", infiniteWidth)
	}
	if len(lines[3]) != 1 || len(lines[4]) != 0 || len(lines[5]) != 0 || len(lines[6]) != 1 || len(lines[7]) != 1 || len(lines[8]) != 1 || len(lines[9]) != 0 {
		t.Fatalf("filtering changed retained groups or exposed rejected markers: %#v", lines)
	}
	if !strings.HasPrefix(valid, "5,") || strings.Contains(valid, "Parent=") {
		t.Fatalf("duplicate key changed first-valid assignment: %s", valid)
	}
	for _, id := range []string{"3", "4", "6", "7", "B", "C"} {
		for _, group := range lines {
			for _, line := range group {
				if strings.HasPrefix(line, id+",") {
					t.Fatalf("rejected marker reserved visible ID %s: %s", id, line)
				}
			}
		}
	}
}

func TestAirfieldPositionsUseSharedTerrainProjectionAndAltitudeOffset(t *testing.T) {
	oldLat, oldLon, oldAlt, oldProfile := atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile
	atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile = 50, 10, 1000, nil
	t.Cleanup(func() {
		atlasLatitude, atlasLongitude, *alt0, activeGeographicProfile = oldLat, oldLon, oldAlt, oldProfile
	})

	marker := airfieldMarker{Key: "projected", Role: "Airport", HasHeading: true, Heading: 90, T: 42, X: 1250, Y: 23.5, Z: 600}
	line := airfieldLineWithID(t, airfieldLines([]airfieldMarker{marker}, 0), "1")
	fields := airfieldPositionFields(t, line)
	wantLat := 50 + 600/111320.0
	wantLon := 10 + 1250/(111320.0*math.Cos(wantLat*math.Pi/180))
	for i, want := range []float64{wantLon, wantLat, 1023.5, 0, 0, 90} {
		if math.Abs(fields[i]-want) > 1e-6 {
			t.Errorf("position field %d = %v, want %v", i, fields[i], want)
		}
	}
}

func airfieldLineWithID(t *testing.T, lines map[uint32][]string, id string) string {
	t.Helper()
	for _, group := range lines {
		for _, line := range group {
			if strings.HasPrefix(line, id+",") {
				return line
			}
		}
	}
	t.Fatalf("object %s not found in %#v", id, lines)
	return ""
}

func airfieldPositionFields(t *testing.T, line string) []float64 {
	t.Helper()
	marker := ",T="
	i := strings.LastIndex(line, marker)
	if i < 0 {
		t.Fatalf("object has no T property: %s", line)
	}
	parts := strings.Split(line[i+len(marker):], "|")
	out := make([]float64, len(parts))
	for j, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil {
			t.Fatalf("parsing T field %q: %v", part, err)
		}
		out[j] = value
	}
	return out
}
