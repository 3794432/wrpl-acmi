package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	ecs "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

func airportTestHeader() wrpl.WRPLHeader {
	h := wrpl.WRPLHeader{StartTime: 1000}
	copy(h.Raw_BattleType[:], airfieldCatalog.Mission)
	copy(h.Raw_Level[:], "levels/air_falklands.bin")
	return h
}

func airportTestMetadata(placement airfieldPlacement) *groundmeta.Parser {
	p := &groundmeta.Parser{Models: map[uint64]groundmeta.Info{}}
	for role, local := range airfieldCatalog.Radars {
		point := airfieldPrefabToWorld(placement.TM, local)
		name := "main_radar"
		model := "unit_airfield_main_radar_char"
		if role == 1 {
			name = "flight_radar_01"
			model = "unit_airfield_flight_radar_char"
		}
		if role == 2 {
			name = "flight_radar_02"
			model = "unit_airfield_flight_radar_char"
		}
		team := placement.Team
		prefix := "t1"
		if team == 2 {
			prefix = "t2"
		}
		info := groundmeta.Info{UID: uint16(role), EID: uint64(100 + role), RawModel: "structures/" + model,
			MissionName: prefix + "_airfield_" + name, Team: &team, HasConstructionTime: true, ConstructionTime: uint32(1000 + role*100),
			Position: &ecs.Point3{X: float32(point[0]), Y: 222.5, Z: float32(point[2])}}
		p.Models[uint64(info.UID)] = info
		p.All = append(p.All, info)
	}
	return p
}

func TestAirfieldGeometryRequiresMissionTeamAndAllThreeRadars(t *testing.T) {
	for _, placement := range airfieldCatalog.Placements {
		t.Run(placement.Name, func(t *testing.T) {
			p := airportTestMetadata(placement)
			markers := prepareReviewedAirfields(airportTestHeader(), airfieldCatalog.MapID, p)
			if len(markers) != 3 || countAirportMarkers(markers) != 1 {
				t.Fatalf("markers: %+v", markers)
			}
			airport := markers[0]
			if airport.MissionName != placement.Name || !airport.HasHeading || math.Abs(airport.Length-3200) > .01 || airport.Width != 120 {
				t.Fatalf("wrong reviewed geometry: %+v", airport)
			}
			if airport.Y != 222.5 || !strings.Contains(airport.ElevationSource, "Approximation") {
				t.Fatal("template altitude was incorrectly used as ground truth")
			}
			if airport.T != 1200 {
				t.Fatal("geometry exported before all three observations existed")
			}
			start := airfieldPrefabToWorld(placement.TM, airfieldCatalog.Start)
			if math.Hypot(markers[1].X-start[0], markers[1].Z-start[2]) > 1e-8 {
				t.Fatal("wrong endpoint projection")
			}
			if markers[1].ParentKey != airport.Key || markers[2].ParentKey != airport.Key {
				t.Fatal("runway points lost airport association")
			}
		})
	}
	placement := airfieldCatalog.Placements[0]
	for _, test := range []string{"mission", "map", "missing-radar", "position", "team", "ambiguous-template"} {
		t.Run(test, func(t *testing.T) {
			h := airportTestHeader()
			p := airportTestMetadata(placement)
			mapID := airfieldCatalog.MapID
			switch test {
			case "mission":
				h.Raw_BattleType = [128]byte{}
				copy(h.Raw_BattleType[:], "other_mission")
			case "map":
				mapID = "air_other"
			case "missing-radar":
				delete(p.Models, 2)
				p.All = p.All[:2]
			case "position":
				p.All[2].Position.X += 5
			case "team":
				wrong := int32(2)
				p.All[2].Team = &wrong
				p.Models[2] = p.All[2]
			case "ambiguous-template":
				old := airfieldCatalog.Placements
				airfieldCatalog.Placements = append(append([]airfieldPlacement{}, old...), placement)
				t.Cleanup(func() { airfieldCatalog.Placements = old })
			}
			markers := prepareReviewedAirfields(h, mapID, p)
			if len(markers) != 1 || markers[0].HasHeading || markers[0].Length != 0 || markers[0].Role != "Airport" || !strings.Contains(markers[0].Name, "radar anchor") {
				t.Fatalf("unsupported geometry was inferred: %+v", markers)
			}
		})
	}
}

func TestAirfieldIdentityAndPositionConflictsAreRejected(t *testing.T) {
	for _, kind := range []string{"model", "eid", "position", "name", "missing-time", "invalid", "duplicate-role"} {
		t.Run(kind, func(t *testing.T) {
			p := airportTestMetadata(airfieldCatalog.Placements[0])
			bad := p.All[0]
			switch kind {
			case "model":
				bad.Conflict = true
			case "eid":
				bad.EID++
			case "name":
				bad.MissionName += "_clone_2"
			case "position":
				v := *bad.Position
				v.X += 20
				bad.Position = &v
			case "missing-time":
				bad.HasConstructionTime = false
			case "invalid":
				bad.Position = &ecs.Point3{X: float32(math.NaN())}
			case "duplicate-role":
				bad.UID = 99
				bad.EID = 999
			}
			p.All = append(p.All, bad)
			p.Models[uint64(bad.UID)] = bad
			markers := prepareReviewedAirfields(airportTestHeader(), airfieldCatalog.MapID, p)
			if kind == "duplicate-role" {
				if len(markers) != 0 {
					t.Fatalf("ambiguous group emitted: %+v", markers)
				}
			} else {
				// Surviving flight-radar evidence may locate the facility, but
				// cannot create runway length or orientation without the main.
				for _, marker := range markers {
					if marker.HasHeading || marker.Length != 0 {
						t.Fatalf("bad identity used for geometry: %+v", markers)
					}
				}
			}
		})
	}
}

func TestAirfieldObjectsAppendWithoutChangingExistingStream(t *testing.T) {
	markers := prepareReviewedAirfields(airportTestHeader(), airfieldCatalog.MapID, airportTestMetadata(airfieldCatalog.Placements[0]))
	tr := &track{key: trackKey{air: true, id: 77}, samples: []acmiSample{{t: 1000, x: 100, y: 1000}, {t: 1300, x: 200, y: 1000}}, hasEnd: true, end: 1400}
	path := filepath.Join(t.TempDir(), "out.acmi")
	render := func(markers []airfieldMarker) string {
		if err := writeACMIWithContext(path, airportTestHeader(), []*track{tr}, nil, nil, nil, airfieldCatalog.MapID, acmiSourceContext{}, acmiWriterContext{airfields: markers}); err != nil {
			t.Fatal(err)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	off, on := render(nil), render(markers)
	for _, line := range strings.Split(off, "\n") {
		if strings.HasPrefix(line, "1,") || line == "-1" || strings.HasPrefix(line, "0,Event=") {
			if !strings.Contains(on, line+"\n") {
				t.Fatalf("original line changed: %s", line)
			}
		}
	}
	if !strings.Contains(on, "2,Name=Blue Airfield 1") || !strings.Contains(on, "Parent=2") || strings.Count(on, "Type=Ground+Static+Aerodrome") != 1 || strings.Count(on, "Type=Navaid+Static+Waypoint") != 2 {
		t.Fatalf("wrong marker ids/types: %s", on)
	}
	if !strings.Contains(on, "Next=4") {
		t.Fatal("runway endpoints are not linked")
	}
}
