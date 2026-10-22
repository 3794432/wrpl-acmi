package main

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	ecs "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

const allTestCommit = "0123456789abcdef0123456789abcdef01234567"

func TestEmbeddedAllAirfieldCatalogIsCompleteAndValid(t *testing.T) {
	catalog, err := parseAllAirfieldCatalog(allAirfieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Commit != "f7952e4291e9fd7b6fe38e84020dddef925c3457" || len(catalog.Prefabs) == 0 {
		t.Fatal("missing pinned runway sources")
	}
	maps := map[string]bool{}
	for _, entry := range catalog.Maps {
		maps[entry.MapID] = true
	}
	for _, id := range terrainmap.KnownIDs() {
		if !maps[id] {
			t.Fatalf("map %s lacks explicit airport coverage configuration", id)
		}
	}
}

func allTestPrefab() allAirfieldPrefabData {
	return allAirfieldPrefabData{
		ID:       "test_airfield_prefab",
		Source:   "aces.vromfs.bin_u/gamedata/objectgroups/test_airfield_prefab.blkx",
		SHA256LF: strings.Repeat("a", 64),
		Start:    [3]float64{100, 0, 0},
		End:      [3]float64{-100, 0, 0},
		Width:    20,
		Radars: allAirfieldRadarSet{
			Main:   [][3]float64{{-40, 0, 0}},
			Flight: [][3]float64{{40, 0, 0}, {40, 0, 20}},
		},
	}
}

func allTestPlacement(name string, conditional bool, x float64) allAirfieldPlacementData {
	return allAirfieldPlacementData{
		Name: name, Team: 1, Prefab: "test_airfield_prefab",
		TM:     [4][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}, {x, 50, 1000}},
		Source: "mis.vromfs.bin_u/gamedata/missions/test/import_airfields.blkx", Conditional: conditional,
	}
}

func allTestCatalog(placements []allAirfieldPlacementData, direct []allAirfieldDirectRunwayData) *allAirfieldCatalogData {
	return &allAirfieldCatalogData{
		Schema: 2, Commit: allTestCommit, Prefabs: []allAirfieldPrefabData{allTestPrefab()},
		Maps: []allAirfieldMapData{{
			MapID: "air_test_generic", Status: "ok",
			Missions: []allAirfieldMissionData{{
				Mission: "Test_Generic_Mission", Source: "mis.vromfs.bin_u/gamedata/missions/test/root.blkx",
				Placements: placements, DirectRunways: direct,
			}},
		}},
	}
}

func allTestHeader(mapID, battle string) wrpl.WRPLHeader {
	var header wrpl.WRPLHeader
	copy(header.Raw_Level[:], "levels/"+mapID+".bin")
	copy(header.Raw_BattleType[:], battle)
	return header
}

func allTestRadarMetadata(t *testing.T, placement allAirfieldPlacementData, prefab allAirfieldPrefabData, ys [3]float64, swapFlight bool) *groundmeta.Parser {
	t.Helper()
	p := &groundmeta.Parser{Models: map[uint64]groundmeta.Info{}}
	setup := []struct {
		name, model string
		local       [3]float64
	}{
		{"main_radar", "unit_airfield_main_radar_char", prefab.Radars.Main[0]},
		{"flight_radar_01", "unit_airfield_flight_radar_char", prefab.Radars.Flight[0]},
		{"flight_radar_02", "unit_airfield_flight_radar_char", prefab.Radars.Flight[1]},
	}
	if swapFlight {
		setup[1].local, setup[2].local = setup[2].local, setup[1].local
	}
	for role, item := range setup {
		point := airfieldPrefabToWorld(placement.TM, item.local)
		team := placement.Team
		info := groundmeta.Info{
			UID: uint16(10 + role), EID: uint64(100 + role),
			RawModel:    "structures/" + item.model,
			MissionName: fmt.Sprintf("t%d_airfield_%s", placement.Team, item.name),
			Team:        &team, HasConstructionTime: true, ConstructionTime: uint32(1000 + role*100),
			Position: &ecs.Point3{X: float32(point[0]), Y: float32(ys[role]), Z: float32(point[2])},
		}
		p.Models[uint64(info.UID)] = info
		p.All = append(p.All, info)
	}
	return p
}

func TestAllAirfieldsRequireExactMapMissionAndHeaderLevel(t *testing.T) {
	catalog := allTestCatalog([]allAirfieldPlacementData{allTestPlacement("Static Field", false, 1000)}, nil)
	for _, test := range []struct {
		name       string
		mapID      string
		header     wrpl.WRPLHeader
		recognized bool
		markers    int
	}{
		{"exact case-insensitive mission", "air_test_generic", allTestHeader("air_test_generic", "test_generic_mission"), true, 3},
		{"wrong mission suffix", "air_test_generic", allTestHeader("air_test_generic", "Test_Generic_Mission_clone"), false, 0},
		{"wrong map id", "air_other", allTestHeader("air_test_generic", "Test_Generic_Mission"), false, 0},
		{"wrong level", "air_test_generic", allTestHeader("air_test_other", "Test_Generic_Mission"), false, 0},
		{"empty battle type", "air_test_generic", allTestHeader("air_test_generic", ""), false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			markers, recognized := prepareAllAirfieldsFromCatalog(test.header, test.mapID, nil, catalog)
			if recognized != test.recognized || len(markers) != test.markers {
				t.Fatalf("recognized=%v markers=%d, want %v/%d: %+v", recognized, len(markers), test.recognized, test.markers, markers)
			}
		})
	}

	if markers, recognized := prepareAllAirfields(allTestHeader("air_test_generic", "Test_Generic_Mission"), "air_test_generic", nil); recognized || len(markers) != 0 {
		t.Fatalf("empty embedded placeholder was treated as a catalogue: %v %+v", recognized, markers)
	}
}

func TestAllAirfieldsApplyRotatedTransformAndWidthScale(t *testing.T) {
	placement := allTestPlacement("Rotated Field", false, 0)
	placement.TM = [4][3]float64{
		{0, 0, 2},
		{0, 1, 0},
		{-3, 0, 0},
		{1000, 10, 2000},
	}
	catalog := allTestCatalog([]allAirfieldPlacementData{placement}, nil)
	markers, recognized := prepareAllAirfieldsFromCatalog(allTestHeader("air_test_generic", "Test_Generic_Mission"), "air_test_generic", nil, catalog)
	if !recognized || len(markers) != 3 {
		t.Fatalf("recognized=%v markers=%+v", recognized, markers)
	}
	airport := markers[0]
	if airport.Role != "Airport" || markers[1].Role != "RunwayEndpoint" || markers[2].Role != "RunwayEndpoint" {
		t.Fatalf("wrong native object roles: %+v", markers)
	}
	if math.Abs(airport.Length-400) > 1e-6 || math.Abs(airport.Width-60) > 1e-6 {
		t.Fatalf("length/width = %v/%v, want 400/60", airport.Length, airport.Width)
	}
	if !airfieldFinite(airport.X, airport.Z, airport.Y) || math.Abs(airport.X-1000) > 1e-6 || math.Abs(airport.Z-2000) > 1e-6 || math.Abs(airport.Y-10) > 1e-6 {
		t.Fatalf("wrong airport midpoint: %+v", airport)
	}
	if math.Abs(markers[1].X-1000) > 1e-6 || math.Abs(markers[1].Z-2200) > 1e-6 ||
		math.Abs(markers[2].X-1000) > 1e-6 || math.Abs(markers[2].Z-1800) > 1e-6 {
		t.Fatalf("wrong transformed endpoints: %+v", markers)
	}
	if math.Abs(airport.Heading-180) > 1e-6 || !airport.HasHeading {
		t.Fatalf("heading = %v, want geometric 180 degrees", airport.Heading)
	}
	if markers[1].ParentKey != airport.Key || markers[2].ParentKey != airport.Key || markers[1].NextKey != markers[2].Key {
		t.Fatalf("parent/next links are incomplete: %+v", markers)
	}
	keys := map[string]bool{}
	for _, marker := range markers {
		if marker.Key == "" || keys[marker.Key] {
			t.Fatalf("duplicate or empty marker key: %+v", markers)
		}
		keys[marker.Key] = true
		if marker.Team != placement.Team {
			t.Fatalf("team was not retained: %+v", marker)
		}
	}
	if lines := airfieldLines(markers, 0); len(lines[0]) != 3 {
		t.Fatalf("static markers should render at T=0: %#v", lines)
	}
}

func TestAllAirfieldsConditionalNeedsCompleteExplicitRadarGroup(t *testing.T) {
	placement := allTestPlacement("Conditional Field", true, 1000)
	direct := allAirfieldDirectRunwayData{
		Name: "Conditional Direct", Team: 1, Start: [3]float64{0, 0, 0}, End: [3]float64{0, 0, 100},
		Width: 20, Source: "root.blkx#conditional", Conditional: true,
	}
	catalog := allTestCatalog([]allAirfieldPlacementData{placement}, []allAirfieldDirectRunwayData{direct})
	header := allTestHeader("air_test_generic", "Test_Generic_Mission")

	if markers, recognized := prepareAllAirfieldsFromCatalog(header, "air_test_generic", nil, catalog); !recognized || len(markers) != 0 {
		t.Fatalf("conditional source materialized without radars: %v %+v", recognized, markers)
	}
	partial := allTestRadarMetadata(t, placement, allTestPrefab(), [3]float64{200, 260, 240}, false)
	partial.All = partial.All[:2]
	delete(partial.Models, 12)
	if markers, _ := prepareAllAirfieldsFromCatalog(header, "air_test_generic", partial, catalog); len(markers) != 0 {
		t.Fatalf("conditional field materialized with only two radars: %+v", markers)
	}
	badTeam := allTestRadarMetadata(t, placement, allTestPrefab(), [3]float64{200, 260, 240}, false)
	wrong := int32(2)
	badTeam.All[2].Team = &wrong
	badTeam.Models[12] = badTeam.All[2]
	if markers, _ := prepareAllAirfieldsFromCatalog(header, "air_test_generic", badTeam, catalog); len(markers) != 0 {
		t.Fatalf("conditional field materialized from a mismatched radar team: %+v", markers)
	}

	metadata := allTestRadarMetadata(t, placement, allTestPrefab(), [3]float64{200, 260, 240}, true)
	markers, recognized := prepareAllAirfieldsFromCatalog(header, "air_test_generic", metadata, catalog)
	if !recognized || len(markers) != 3 {
		t.Fatalf("complete unordered radar group was rejected: %v %+v", recognized, markers)
	}
	airport := markers[0]
	if airport.T != 1200 || math.Abs(airport.Y-700.0/3) > 1e-3 {
		t.Fatalf("validated time/elevation = %d/%v", airport.T, airport.Y)
	}
	if !strings.Contains(airport.ElevationSource, "ReplayRadar") || len(airport.UnitUIDs) != 3 {
		t.Fatalf("radar evidence was not retained: %+v", airport)
	}
}

func TestAllAirfieldsDuplicateVariantsDedupAndAmbiguityRejects(t *testing.T) {
	first := allTestPlacement("Rank Variant A", true, 1000)
	second := first
	second.Name = "Rank Variant B"
	second.Source = "root.blkx#second-import"
	catalog := allTestCatalog([]allAirfieldPlacementData{first, second}, nil)
	header := allTestHeader("air_test_generic", "Test_Generic_Mission")
	metadata := allTestRadarMetadata(t, first, allTestPrefab(), [3]float64{100, 100, 100}, false)
	markers, _ := prepareAllAirfieldsFromCatalog(header, "air_test_generic", metadata, catalog)
	if len(markers) != 3 || countAirportMarkers(markers) != 1 {
		t.Fatalf("identical variants were not deduplicated: %+v", markers)
	}

	third := first
	third.Name = "Shifted Rank Variant"
	third.TM[3][0] += 1
	catalog.Maps[0].Missions[0].Placements = append(catalog.Maps[0].Missions[0].Placements, third)
	markers, _ = prepareAllAirfieldsFromCatalog(header, "air_test_generic", metadata, catalog)
	if len(markers) != 0 {
		t.Fatalf("multiple matching geometries were not rejected as ambiguous: %+v", markers)
	}
}

func TestAllAirfieldsStaticPlacementAndDirectRunwayNeedNoMetadata(t *testing.T) {
	placement := allTestPlacement("Static Field", false, 500)
	direct := allAirfieldDirectRunwayData{
		Name: "Source Runway", Team: 2, Start: [3]float64{-200, 40, 300}, End: [3]float64{200, 40, 300},
		Width: 45, Source: "root.blkx#triggers/addAirfield/0", Conditional: false,
	}
	conditional := direct
	conditional.Name = "Inactive Conditional Direct"
	conditional.Conditional = true
	conditional.Source = "root.blkx#conditional-addAirfield"
	catalog := allTestCatalog([]allAirfieldPlacementData{placement}, []allAirfieldDirectRunwayData{direct, conditional})
	markers, recognized := prepareAllAirfieldsFromCatalog(allTestHeader("air_test_generic", "Test_Generic_Mission"), "air_test_generic", nil, catalog)
	if !recognized || len(markers) != 6 || countAirportMarkers(markers) != 2 {
		t.Fatalf("static source geometry was not emitted: %v %+v", recognized, markers)
	}
	for _, marker := range markers {
		if marker.T != 0 {
			t.Fatalf("static catalogue runway did not start at zero: %+v", marker)
		}
	}
	if math.Abs(markers[0].Length-200) > 1e-6 || math.Abs(markers[0].Width-20) > 1e-6 {
		t.Fatalf("wrong static prefab dimensions: %+v", markers[0])
	}
	if math.Abs(markers[3].Length-400) > 1e-6 || math.Abs(markers[3].Width-45) > 1e-6 || markers[3].Team != 2 {
		t.Fatalf("wrong direct runway: %+v", markers[3])
	}
	if !strings.Contains(markers[3].PositionSource, "AddAirfield") || strings.Contains(strings.Join(allTestMarkerNames(markers), " "), conditional.Name) {
		t.Fatalf("direct runway provenance/inactive filtering is wrong: %+v", markers)
	}
}

func TestAllAirfieldsTerrainHeightAndRadarHeightAreDistinct(t *testing.T) {
	static := allTestPlacement("Terrain Field", false, 1000)
	ground := 123.5
	static.GroundHeight = &ground
	static.GroundHeightSource = "terrain:test-map"
	catalog := allTestCatalog([]allAirfieldPlacementData{static}, nil)
	markers, _ := prepareAllAirfieldsFromCatalog(allTestHeader("air_test_generic", "Test_Generic_Mission"), "air_test_generic", nil, catalog)
	if len(markers) != 3 || markers[0].Y != ground || markers[0].T != 0 || !strings.Contains(markers[0].ElevationSource, "CatalogGroundHeight") ||
		!strings.Contains(markers[0].ElevationSource, "terrain:test-map") {
		t.Fatalf("catalog terrain height was not used: %+v", markers)
	}
	staticRadars := allTestRadarMetadata(t, static, allTestPrefab(), [3]float64{250, 260, 240}, false)
	markers, _ = prepareAllAirfieldsFromCatalog(allTestHeader("air_test_generic", "Test_Generic_Mission"), "air_test_generic", staticRadars, catalog)
	if len(markers) != 3 || markers[0].Y != 250 || markers[0].T != 1200 || !strings.Contains(markers[0].ElevationSource, "ReplayRadar") {
		t.Fatalf("matching replay radars did not refine an unconditional placement: %+v", markers)
	}

	conditional := static
	conditional.Name = "Radar Height Field"
	conditional.Conditional = true
	catalog = allTestCatalog([]allAirfieldPlacementData{conditional}, nil)
	metadata := allTestRadarMetadata(t, conditional, allTestPrefab(), [3]float64{250, 260, 240}, false)
	markers, _ = prepareAllAirfieldsFromCatalog(allTestHeader("air_test_generic", "Test_Generic_Mission"), "air_test_generic", metadata, catalog)
	if len(markers) != 3 || markers[0].Y != 250 || markers[0].T != 1200 ||
		!strings.Contains(markers[0].ElevationSource, "ReplayRadar") {
		t.Fatalf("radar mean height did not override catalog terrain: %+v", markers)
	}
	if markers[1].Y != markers[0].Y || markers[2].Y != markers[0].Y {
		t.Fatalf("runway endpoints did not share the chosen field elevation: %+v", markers)
	}
}

func TestAllAirfieldsSourceIdentityAndUnknownJSONFields(t *testing.T) {
	commit := strings.Repeat("b", 40)
	hash := strings.Repeat("c", 64)
	raw := fmt.Sprintf(`{
		"schema":2,"commit":%q,"future_top":true,
		"prefabs":[{"id":"source_prefab","source":"objectgroups/source_prefab.blkx","sha256_lf":%q,
		  "start":[-10,0,0],"end":[10,0,0],"width":4,
		  "radars":{"main":[],"flight":[]},"future_prefab":1}],
		"maps":[{"map_id":"air_source","status":"ok","reason":"","future_map":2,"missions":[{
		  "mission":"SourceMission","source":"missions/source_root.blkx","future_mission":3,
		  "placements":[{"name":"Source Field","team":1,"prefab":"source_prefab",
		    "tm":[[1,0,0],[0,1,0],[0,0,1],[0,20,0]],"source":"missions/source_import.blkx",
		    "conditional":false,"ground_height":20.0,"ground_height_source":"terrain:source","future_placement":4}],
		  "direct_runways":[]}]}]
	}`, commit, hash)
	catalog, err := parseAllAirfieldCatalog([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	markers, recognized := prepareAllAirfieldsFromCatalog(allTestHeader("air_source", "SOURCEMISSION"), "air_source", nil, catalog)
	if !recognized || len(markers) != 3 {
		t.Fatalf("parsed injected catalogue did not render: %v %+v", recognized, markers)
	}
	airport := markers[0]
	if airport.Source != "Datamine@"+commit+":missions/source_import.blkx" || airport.Prefab != "source_prefab" ||
		!strings.Contains(airport.GeometrySource, "objectgroups/source_prefab.blkx#/airfield/start,end,width") ||
		!strings.Contains(airport.ElevationSource, "terrain:source") {
		t.Fatalf("source provenance was not retained: %+v", airport)
	}
	if airport.Length != 20 || airport.Width != 4 {
		t.Fatalf("wrong source dimensions: %+v", airport)
	}

	if _, err := parseAllAirfieldCatalog([]byte(`{"schema":2,"commit":"","prefabs":[],"maps":[],"future":1}`)); err != nil {
		t.Fatalf("empty schema 2 placeholder with extras was rejected: %v", err)
	}
	if _, err := parseAllAirfieldCatalog([]byte(`{"schema":1,"commit":"","prefabs":[],"maps":[]}`)); err == nil {
		t.Fatal("schema 1 catalogue was accepted")
	}
}

func TestAllAirfieldCatalogValidationCaps(t *testing.T) {
	base := allTestCatalog(nil, nil)
	if err := validateAllAirfieldCatalog(base); err != nil {
		t.Fatalf("fixture rejected: %v", err)
	}

	maps := make([]allAirfieldMapData, 0, allAirfieldMaxMaps+1)
	for i := 0; i <= allAirfieldMaxMaps; i++ {
		maps = append(maps, allAirfieldMapData{MapID: fmt.Sprintf("map_%02d", i)})
	}
	tooMany := &allAirfieldCatalogData{Schema: 2, Commit: allTestCommit, Maps: maps}
	if err := validateAllAirfieldCatalog(tooMany); err == nil {
		t.Fatal("catalogue above the map cap was accepted")
	}

	placements := make([]allAirfieldPlacementData, allAirfieldMaxPlacementsPerSource+1)
	for i := range placements {
		placements[i] = allTestPlacement(fmt.Sprintf("Field %d", i), false, float64(i))
	}
	overSource := allTestCatalog(placements, nil)
	if err := validateAllAirfieldCatalog(overSource); err == nil {
		t.Fatal("catalogue above the placement/source cap was accepted")
	}
}

func TestNamedRunwayAreasDoNotInventWidth(t *testing.T) {
	known := false
	runway := allAirfieldDirectRunwayData{Name: "Runway 01", Start: [3]float64{0, 10, 0}, End: [3]float64{1200, 10, 0},
		Source: "missions/test.blkx#/areas/airfield_start_01,airfield_end_01", WidthKnown: &known, GeometryKind: "NamedRunwayEndpointAreas"}
	catalog := allTestCatalog(nil, []allAirfieldDirectRunwayData{runway})
	if err := validateAllAirfieldCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	markers, ok := prepareAllAirfieldsFromCatalog(allTestHeader("air_test_generic", "Test_Generic_Mission"), "air_test_generic", nil, catalog)
	if !ok || len(markers) != 3 || markers[0].Length != 1200 || markers[0].Width != 0 {
		t.Fatalf("explicit runway axis lost: %+v", markers)
	}
	lines := airfieldLines(markers, 0)
	for _, group := range lines {
		for _, line := range group {
			if strings.Contains(line, ",Width=") || strings.Contains(line, "WT_AirfieldWidth=") {
				t.Fatal("unknown source width was fabricated")
			}
		}
	}
	runway.GeometryKind = ""
	if validateAllAirfieldDirectRunway(&runway) == nil {
		t.Fatal("unsupported zero width was accepted as a named source")
	}
}

func allTestMarkerNames(markers []airfieldMarker) []string {
	names := make([]string, len(markers))
	for i := range markers {
		names[i] = markers[i].MissionName
	}
	return names
}
