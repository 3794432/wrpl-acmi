package main

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

// The source-generated catalogue is embedded so normal builds and exports
// need no network access or Datamine checkout.
//
//go:embed data/airfields_all.json
var allAirfieldsJSON []byte

const (
	allAirfieldCatalogSchema          = 2
	allAirfieldMaxMaps                = 128
	allAirfieldMaxPrefabs             = 4096
	allAirfieldMaxMissions            = 10_000
	allAirfieldMaxPlacementsPerSource = 1024
	allAirfieldMaxSourceCount         = 1024
	allAirfieldRadarToleranceMeters   = 2.0
)

type allAirfieldRadarSet struct {
	Main   [][3]float64 `json:"main"`
	Flight [][3]float64 `json:"flight"`
}

type allAirfieldPrefabData struct {
	ID       string              `json:"id"`
	Source   string              `json:"source"`
	SHA256LF string              `json:"sha256_lf"`
	Start    [3]float64          `json:"start"`
	End      [3]float64          `json:"end"`
	Width    float64             `json:"width"`
	Radars   allAirfieldRadarSet `json:"radars"`
}

type allAirfieldPlacementData struct {
	Name               string        `json:"name"`
	Team               int32         `json:"team"`
	Prefab             string        `json:"prefab"`
	TM                 [4][3]float64 `json:"tm"`
	Source             string        `json:"source"`
	Conditional        bool          `json:"conditional"`
	GroundHeight       *float64      `json:"ground_height,omitempty"`
	GroundHeightSource string        `json:"ground_height_source,omitempty"`
}

type allAirfieldDirectRunwayData struct {
	Name               string     `json:"name"`
	Team               int32      `json:"team"`
	Start              [3]float64 `json:"start"`
	End                [3]float64 `json:"end"`
	Width              float64    `json:"width"`
	WidthKnown         *bool      `json:"width_known,omitempty"`
	GeometryKind       string     `json:"geometry_kind,omitempty"`
	Source             string     `json:"source"`
	Conditional        bool       `json:"conditional"`
	GroundHeight       *float64   `json:"ground_height,omitempty"`
	GroundHeightSource string     `json:"ground_height_source,omitempty"`
}

type allAirfieldMissionData struct {
	Mission       string                        `json:"mission"`
	Source        string                        `json:"source"`
	Placements    []allAirfieldPlacementData    `json:"placements"`
	DirectRunways []allAirfieldDirectRunwayData `json:"direct_runways"`
}

type allAirfieldMapData struct {
	MapID    string                   `json:"map_id"`
	Status   string                   `json:"status"`
	Reason   string                   `json:"reason"`
	Missions []allAirfieldMissionData `json:"missions"`
}

type allAirfieldCatalogData struct {
	Schema  int                     `json:"schema"`
	Commit  string                  `json:"commit"`
	Prefabs []allAirfieldPrefabData `json:"prefabs"`
	Maps    []allAirfieldMapData    `json:"maps"`
}

var allAirfieldEmbedded = struct {
	once sync.Once
	data *allAirfieldCatalogData
}{}

func parseAllAirfieldCatalog(raw []byte) (*allAirfieldCatalogData, error) {
	var data allAirfieldCatalogData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("decode all-map airfield catalogue: %w", err)
	}
	if err := validateAllAirfieldCatalog(&data); err != nil {
		return nil, err
	}
	return &data, nil
}

func allAirfieldEmbeddedCatalog() *allAirfieldCatalogData {
	allAirfieldEmbedded.once.Do(func() {
		data, err := parseAllAirfieldCatalog(allAirfieldsJSON)
		if err == nil {
			allAirfieldEmbedded.data = data
		}
	})
	return allAirfieldEmbedded.data
}

func validateAllAirfieldCatalog(data *allAirfieldCatalogData) error {
	if data == nil {
		return fmt.Errorf("all-map airfield catalogue is nil")
	}
	if data.Schema != allAirfieldCatalogSchema {
		return fmt.Errorf("unsupported all-map airfield schema %d", data.Schema)
	}
	if len(data.Maps) > allAirfieldMaxMaps {
		return fmt.Errorf("all-map airfield catalogue has %d maps, limit %d", len(data.Maps), allAirfieldMaxMaps)
	}
	if len(data.Prefabs) > allAirfieldMaxPrefabs {
		return fmt.Errorf("all-map airfield catalogue has %d prefabs, limit %d", len(data.Prefabs), allAirfieldMaxPrefabs)
	}
	if data.Commit == "" {
		if len(data.Prefabs) != 0 || len(data.Maps) != 0 {
			return fmt.Errorf("non-empty all-map airfield catalogue has no commit")
		}
	} else if !allAirfieldHex(data.Commit, 40) {
		return fmt.Errorf("invalid all-map airfield commit")
	}

	prefabs := make(map[string]struct{}, len(data.Prefabs))
	for i := range data.Prefabs {
		prefab := &data.Prefabs[i]
		if strings.TrimSpace(prefab.ID) == "" || strings.TrimSpace(prefab.Source) == "" {
			return fmt.Errorf("prefab %d has no id or source", i)
		}
		if _, exists := prefabs[prefab.ID]; exists {
			return fmt.Errorf("duplicate all-map prefab %q", prefab.ID)
		}
		prefabs[prefab.ID] = struct{}{}
		if prefab.SHA256LF != "" && !allAirfieldHex(prefab.SHA256LF, 64) {
			return fmt.Errorf("invalid prefab source hash for %q", prefab.ID)
		}
		if !allAirfieldVectorBounded(prefab.Start) || !allAirfieldVectorBounded(prefab.End) ||
			!allAirfieldBounded(prefab.Width) || prefab.Width < 0 {
			return fmt.Errorf("invalid prefab geometry for %q", prefab.ID)
		}
		for _, point := range append(append([][3]float64(nil), prefab.Radars.Main...), prefab.Radars.Flight...) {
			if !allAirfieldVectorBounded(point) {
				return fmt.Errorf("invalid prefab radar position for %q", prefab.ID)
			}
		}
	}

	mapIDs := make(map[string]struct{}, len(data.Maps))
	missions := 0
	for i := range data.Maps {
		mapData := &data.Maps[i]
		if strings.TrimSpace(mapData.MapID) == "" {
			return fmt.Errorf("map %d has no id", i)
		}
		key := strings.ToLower(mapData.MapID)
		if _, exists := mapIDs[key]; exists {
			return fmt.Errorf("duplicate all-map id %q", mapData.MapID)
		}
		mapIDs[key] = struct{}{}
		missions += len(mapData.Missions)
		if missions > allAirfieldMaxMissions {
			return fmt.Errorf("all-map airfield catalogue has more than %d missions", allAirfieldMaxMissions)
		}
		missionNames := make(map[string]struct{}, len(mapData.Missions))
		for j := range mapData.Missions {
			mission := &mapData.Missions[j]
			if strings.TrimSpace(mission.Mission) == "" || strings.TrimSpace(mission.Source) == "" {
				return fmt.Errorf("map %q mission %d has no name or source", mapData.MapID, j)
			}
			nameKey := strings.ToLower(mission.Mission)
			if _, exists := missionNames[nameKey]; exists {
				return fmt.Errorf("duplicate mission %q on map %q", mission.Mission, mapData.MapID)
			}
			missionNames[nameKey] = struct{}{}
			if len(mission.Placements) > allAirfieldMaxPlacementsPerSource ||
				len(mission.DirectRunways) > allAirfieldMaxPlacementsPerSource {
				return fmt.Errorf("mission %q exceeds the %d-entry source limit", mission.Mission, allAirfieldMaxPlacementsPerSource)
			}
			sourceCounts := make(map[string]int, len(mission.Placements)+len(mission.DirectRunways))
			for k := range mission.Placements {
				placement := &mission.Placements[k]
				if err := validateAllAirfieldPlacement(placement); err != nil {
					return fmt.Errorf("mission %q placement %d: %w", mission.Mission, k, err)
				}
				sourceCounts[placement.Source]++
			}
			for k := range mission.DirectRunways {
				runway := &mission.DirectRunways[k]
				if err := validateAllAirfieldDirectRunway(runway); err != nil {
					return fmt.Errorf("mission %q direct runway %d: %w", mission.Mission, k, err)
				}
				sourceCounts[runway.Source]++
			}
			for source, count := range sourceCounts {
				if count > allAirfieldMaxSourceCount {
					return fmt.Errorf("mission %q source %q exceeds %d entries", mission.Mission, source, allAirfieldMaxSourceCount)
				}
			}
		}
	}
	return nil
}

func validateAllAirfieldPlacement(placement *allAirfieldPlacementData) error {
	if strings.TrimSpace(placement.Name) == "" || strings.TrimSpace(placement.Prefab) == "" ||
		strings.TrimSpace(placement.Source) == "" {
		return fmt.Errorf("name, prefab and source are required")
	}
	if !allAirfieldMatrixBounded(placement.TM) {
		return fmt.Errorf("transform is not finite and bounded")
	}
	if err := validateAllAirfieldGroundHeight(placement.GroundHeight); err != nil {
		return err
	}
	return nil
}

func validateAllAirfieldDirectRunway(runway *allAirfieldDirectRunwayData) error {
	if strings.TrimSpace(runway.Name) == "" || strings.TrimSpace(runway.Source) == "" {
		return fmt.Errorf("name and source are required")
	}
	if !allAirfieldVectorBounded(runway.Start) || !allAirfieldVectorBounded(runway.End) ||
		!allAirfieldBounded(runway.Width) || runway.Width < 0 {
		return fmt.Errorf("runway geometry is not finite and bounded")
	}
	if runway.WidthKnown != nil && !*runway.WidthKnown && (runway.Width != 0 || runway.GeometryKind != "NamedRunwayEndpointAreas") {
		return fmt.Errorf("unknown width is allowed only for explicitly named runway endpoint areas")
	}
	return validateAllAirfieldGroundHeight(runway.GroundHeight)
}

func validateAllAirfieldGroundHeight(value *float64) error {
	if value != nil && !allAirfieldBounded(*value) {
		return fmt.Errorf("ground height is not finite and bounded")
	}
	return nil
}

func allAirfieldHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func allAirfieldBounded(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= 10_000_000
}

func allAirfieldVectorBounded(value [3]float64) bool {
	return allAirfieldBounded(value[0]) && allAirfieldBounded(value[1]) && allAirfieldBounded(value[2])
}

func allAirfieldMatrixBounded(value [4][3]float64) bool {
	for _, row := range value {
		if !allAirfieldVectorBounded(row) {
			return false
		}
	}
	return true
}

// prepareAllAirfields matches the embedded catalogue against the exact replay
// map and mission. The boolean reports an exact catalogue mission entry, even
// when the mission currently contributes no renderable runway.
func prepareAllAirfields(header wrpl.WRPLHeader, mapID string, metadata *groundmeta.Parser) ([]airfieldMarker, bool) {
	return prepareAllAirfieldsFromCatalog(header, mapID, metadata, allAirfieldEmbeddedCatalog())
}

// prepareAllAirfieldsFromCatalog is the injected-catalogue path used by tests
// and by callers that assemble the sidecar themselves.
func prepareAllAirfieldsFromCatalog(header wrpl.WRPLHeader, mapID string, metadata *groundmeta.Parser, catalog *allAirfieldCatalogData) ([]airfieldMarker, bool) {
	if catalog == nil || strings.TrimSpace(mapID) == "" {
		return nil, false
	}
	if headerText(header.Raw_Level[:]) != "levels/"+mapID+".bin" {
		return nil, false
	}
	battleType := headerText(header.Raw_BattleType[:])
	if battleType == "" {
		return nil, false
	}

	var mapData *allAirfieldMapData
	for i := range catalog.Maps {
		if catalog.Maps[i].MapID == mapID {
			mapData = &catalog.Maps[i]
			break
		}
	}
	if mapData == nil {
		return nil, false
	}
	var mission *allAirfieldMissionData
	for i := range mapData.Missions {
		if strings.EqualFold(strings.TrimSpace(mapData.Missions[i].Mission), battleType) {
			mission = &mapData.Missions[i]
			break
		}
	}
	if mission == nil {
		return nil, false
	}

	prefabs := make(map[string]*allAirfieldPrefabData, len(catalog.Prefabs))
	for i := range catalog.Prefabs {
		prefabs[catalog.Prefabs[i].ID] = &catalog.Prefabs[i]
	}

	selected := make(map[int]*allAirfieldRadarGroup)
	for _, group := range allAirfieldRadarGroups(metadata) {
		matches := make([]allAirfieldMatch, 0, 1)
		seen := make(map[string]struct{})
		for i := range mission.Placements {
			placement := &mission.Placements[i]
			if placement.Team != group.team {
				continue
			}
			prefab := prefabs[placement.Prefab]
			if prefab == nil || !allAirfieldPrefabMatches(group, placement, prefab) {
				continue
			}
			candidate, ok := allAirfieldPlacementCandidate(placement, prefab, mission.Source, group)
			if !ok {
				continue
			}
			key := allAirfieldGeometryKey(candidate)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			matches = append(matches, allAirfieldMatch{index: i, candidate: candidate})
		}
		// Two distinct geometries under one explicit radar group are ambiguous.
		// Identical rank/import variants were already collapsed above.
		if len(matches) != 1 {
			continue
		}
		if _, exists := selected[matches[0].index]; !exists {
			selected[matches[0].index] = group
		}
	}

	candidates := make([]allAirfieldRunwayCandidate, 0, len(mission.Placements)+len(mission.DirectRunways))
	for i := range mission.Placements {
		placement := &mission.Placements[i]
		group := selected[i]
		if placement.Conditional && group == nil {
			continue
		}
		prefab := prefabs[placement.Prefab]
		if prefab == nil {
			continue
		}
		candidate, ok := allAirfieldPlacementCandidate(placement, prefab, mission.Source, group)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	for i := range mission.DirectRunways {
		runway := &mission.DirectRunways[i]
		// A conditional addAirfield action cannot be proven active without an
		// explicit prefab/radar identity relationship. Leave it out.
		if runway.Conditional {
			continue
		}
		candidate, ok := allAirfieldDirectCandidate(runway, mission.Source)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	candidates = allAirfieldDedupeCandidates(candidates)
	return allAirfieldCandidateMarkers(catalog.Commit, mapID, mission.Mission, candidates), true
}

type allAirfieldMatch struct {
	index     int
	candidate allAirfieldRunwayCandidate
}

type allAirfieldRunwayCandidate struct {
	name               string
	team               int32
	prefab             string
	prefabSource       string
	missionSource      string
	source             string
	start, end         [3]float64
	width              float64
	groundHeight       *float64
	groundHeightSource string
	validated          bool
	radarY             float64
	radarT             uint32
	radarUIDs          []uint64
	direct             bool
	geometryKind       string
}

func allAirfieldPlacementCandidate(placement *allAirfieldPlacementData, prefab *allAirfieldPrefabData, missionSource string, group *allAirfieldRadarGroup) (allAirfieldRunwayCandidate, bool) {
	start := airfieldPrefabToWorld(placement.TM, prefab.Start)
	end := airfieldPrefabToWorld(placement.TM, prefab.End)
	width := allAirfieldPrefabWidth(placement.TM, prefab)
	if !allAirfieldGeometryOK(start, end, width) {
		return allAirfieldRunwayCandidate{}, false
	}
	candidate := allAirfieldRunwayCandidate{
		name: placement.Name, team: placement.Team, prefab: prefab.ID,
		prefabSource: prefab.Source, missionSource: missionSource, source: placement.Source,
		start: start, end: end, width: width,
		groundHeight: placement.GroundHeight, groundHeightSource: placement.GroundHeightSource,
	}
	if group != nil {
		y, stamp, uids, ok := allAirfieldRadarEvidence(group)
		if !ok {
			return allAirfieldRunwayCandidate{}, false
		}
		candidate.validated = true
		candidate.radarY, candidate.radarT, candidate.radarUIDs = y, stamp, uids
	}
	return candidate, true
}

func allAirfieldDirectCandidate(runway *allAirfieldDirectRunwayData, missionSource string) (allAirfieldRunwayCandidate, bool) {
	widthForValidation := runway.Width
	if runway.WidthKnown != nil && !*runway.WidthKnown && runway.GeometryKind == "NamedRunwayEndpointAreas" && runway.Width == 0 {
		widthForValidation = 1 // Validate the source axis, without inventing an exported width.
	}
	if !allAirfieldGeometryOK(runway.Start, runway.End, widthForValidation) {
		return allAirfieldRunwayCandidate{}, false
	}
	return allAirfieldRunwayCandidate{
		name: runway.Name, team: runway.Team, missionSource: missionSource, source: runway.Source,
		start: runway.Start, end: runway.End, width: runway.Width,
		groundHeight: runway.GroundHeight, groundHeightSource: runway.GroundHeightSource,
		direct: true, geometryKind: runway.GeometryKind,
	}, true
}

func allAirfieldGeometryOK(start, end [3]float64, width float64) bool {
	if !allAirfieldVectorBounded(start) || !allAirfieldVectorBounded(end) {
		return false
	}
	length := math.Hypot(end[0]-start[0], end[2]-start[2])
	return airfieldDimension(length, airfieldMaxLengthMeters) && airfieldDimension(width, airfieldMaxWidthMeters)
}

func allAirfieldPrefabWidth(tm [4][3]float64, prefab *allAirfieldPrefabData) float64 {
	dx, dz := prefab.End[0]-prefab.Start[0], prefab.End[2]-prefab.Start[2]
	horizontal := math.Hypot(dx, dz)
	if horizontal < 1e-9 {
		return math.Hypot(tm[2][0], tm[2][2]) * prefab.Width
	}
	perpendicular := [3]float64{dz / horizontal, 0, -dx / horizontal}
	worldWidth := allAirfieldTransformVector(tm, [3]float64{perpendicular[0] * prefab.Width, 0, perpendicular[2] * prefab.Width})
	return math.Hypot(worldWidth[0], worldWidth[2])
}

func allAirfieldTransformVector(tm [4][3]float64, local [3]float64) [3]float64 {
	var result [3]float64
	for axis := range result {
		for row := 0; row < 3; row++ {
			result[axis] += tm[row][axis] * local[row]
		}
	}
	return result
}

func allAirfieldDedupeCandidates(candidates []allAirfieldRunwayCandidate) []allAirfieldRunwayCandidate {
	out := candidates[:0]
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		key := allAirfieldGeometryKey(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func allAirfieldGeometryKey(candidate allAirfieldRunwayCandidate) string {
	values := []float64{
		candidate.start[0], candidate.start[2],
		candidate.end[0], candidate.end[2], candidate.width,
	}
	parts := make([]string, 0, len(values)+1)
	for _, value := range values {
		parts = append(parts, strconv.FormatFloat(value, 'f', 6, 64))
	}
	parts = append(parts, strconv.FormatInt(int64(candidate.team), 10))
	return strings.Join(parts, ":")
}

func allAirfieldCandidateMarkers(commit, mapID, mission string, candidates []allAirfieldRunwayCandidate) []airfieldMarker {
	markers := make([]airfieldMarker, 0, len(candidates)*3)
	counts := make(map[int32]int)
	for i, candidate := range candidates {
		y, stamp, elevationSource, positionSource, confidence := allAirfieldElevation(candidate)
		if !allAirfieldBounded(y) {
			continue
		}
		length := math.Hypot(candidate.end[0]-candidate.start[0], candidate.end[2]-candidate.start[2])
		if !airfieldDimension(length, airfieldMaxLengthMeters) || (candidate.width != 0 && !airfieldDimension(candidate.width, airfieldMaxWidthMeters)) {
			continue
		}
		source := "Datamine@" + commit + ":" + candidate.source
		geometrySource := "MissionAddAirfield:" + candidate.missionSource
		if candidate.geometryKind == "NamedRunwayEndpointAreas" {
			geometrySource = "MissionNamedRunwayEndpointAreas:" + candidate.source
			if candidate.width == 0 {
				confidence += ";WidthUnknown"
			}
		}
		if !candidate.direct {
			geometrySource = candidate.prefabSource + "#/airfield/start,end,width;mission=" + candidate.missionSource
		}
		base := fmt.Sprintf("all-airfield:%s:%s:%d", mapID, mission, i)
		counts[candidate.team]++
		coalition, _ := airfieldCoalition(candidate.team)
		displayName := fmt.Sprintf("%s Airfield %d", coalition, counts[candidate.team])
		airport := airfieldMarker{
			Key: base, Name: displayName, Role: "Airport", Source: source,
			Confidence: confidence, MissionName: candidate.name, Prefab: candidate.prefab,
			GeometrySource: geometrySource, PositionSource: positionSource, ElevationSource: elevationSource,
			HeadingSource: "RunwayStartToEndGeometry;LandingDirectionUnknown",
			T:             stamp, X: (candidate.start[0] + candidate.end[0]) / 2, Y: y,
			Z:       (candidate.start[2] + candidate.end[2]) / 2,
			Heading: airfieldBearing(candidate.start, candidate.end), HasHeading: true,
			Length: length, Width: candidate.width, Team: candidate.team,
		}
		if candidate.validated {
			airport.UnitUIDs = append([]uint64(nil), candidate.radarUIDs...)
		}
		markers = append(markers, airport)
		for pointIndex, point := range [][3]float64{candidate.start, candidate.end} {
			endpoint := airport
			endpoint.Key = fmt.Sprintf("%s:endpoint%d", base, pointIndex+1)
			endpoint.Name = fmt.Sprintf("%s RWY endpoint %d", displayName, pointIndex+1)
			endpoint.ParentKey = base
			endpoint.Role = "RunwayEndpoint"
			endpoint.Length, endpoint.Width = 0, 0
			endpoint.X, endpoint.Z = point[0], point[2]
			if pointIndex == 0 {
				endpoint.NextKey = fmt.Sprintf("%s:endpoint2", base)
			} else {
				endpoint.Heading = airfieldBearing(candidate.end, candidate.start)
			}
			markers = append(markers, endpoint)
		}
	}
	return markers
}

func allAirfieldElevation(candidate allAirfieldRunwayCandidate) (float64, uint32, string, string, string) {
	if candidate.validated {
		return candidate.radarY, candidate.radarT, "MeanReplayRadarGroundHeightApproximation",
			"MissionPrefabTransformValidatedByThreeReplayRadars", "SourceGeometryValidatedByThreeReplayRadars;EstimatedElevation"
	}
	if candidate.groundHeight != nil {
		source := "CatalogGroundHeightApproximation"
		if candidate.groundHeightSource != "" {
			source += ":" + candidate.groundHeightSource
		}
		return *candidate.groundHeight, 0, source, allAirfieldStaticPositionSource(candidate),
			"SourceMissionGeometry;CatalogTerrainApproximation"
	}
	return (candidate.start[1] + candidate.end[1]) / 2, 0, "NominalTransformedEndpointY;NotGameGround",
		allAirfieldStaticPositionSource(candidate), "SourceMissionGeometry;NominalElevation"
}

func allAirfieldStaticPositionSource(candidate allAirfieldRunwayCandidate) string {
	if candidate.geometryKind == "NamedRunwayEndpointAreas" {
		return "MissionNamedRunwayEndpointAreas"
	}
	if candidate.direct {
		return "MissionAddAirfieldSource"
	}
	return "MissionPrefabTransform"
}

type allAirfieldRadarGroup struct {
	key    string
	team   int32
	radars [3]*airfieldRadar
}

func allAirfieldRadarGroups(metadata *groundmeta.Parser) []*allAirfieldRadarGroup {
	radars := airfieldReplayRadars(metadata)
	groups := make(map[string]*allAirfieldRadarGroup)
	bad := make(map[string]bool)
	for i := range radars {
		radar := &radars[i]
		match := airfieldMissionPattern.FindStringSubmatch(radar.info.MissionName)
		if match == nil || radar.info.Team == nil {
			continue
		}
		expectedTeam := int32(match[1][1] - '0')
		role, known := airfieldRadarRole(radar.info)
		if !known || role < 0 || *radar.info.Team != expectedTeam ||
			(role == 0) != (match[2] == "main_radar") {
			continue
		}
		key := match[1] + match[3]
		group := groups[key]
		if group == nil {
			group = &allAirfieldRadarGroup{key: key, team: expectedTeam}
			groups[key] = group
		}
		if group.radars[role] != nil {
			bad[key] = true
			continue
		}
		group.radars[role] = radar
	}
	keys := make([]string, 0, len(groups))
	for key, group := range groups {
		if !bad[key] && group.radars[0] != nil && group.radars[1] != nil && group.radars[2] != nil {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]*allAirfieldRadarGroup, 0, len(keys))
	for _, key := range keys {
		out = append(out, groups[key])
	}
	return out
}

func allAirfieldPrefabMatches(group *allAirfieldRadarGroup, placement *allAirfieldPlacementData, prefab *allAirfieldPrefabData) bool {
	if len(prefab.Radars.Main) != 1 || len(prefab.Radars.Flight) != 2 {
		return false
	}
	main := airfieldPrefabToWorld(placement.TM, prefab.Radars.Main[0])
	flight0 := airfieldPrefabToWorld(placement.TM, prefab.Radars.Flight[0])
	flight1 := airfieldPrefabToWorld(placement.TM, prefab.Radars.Flight[1])
	if !allAirfieldRadarNear(group.radars[0], main) {
		return false
	}
	ordered := allAirfieldRadarNear(group.radars[1], flight0) && allAirfieldRadarNear(group.radars[2], flight1)
	swapped := allAirfieldRadarNear(group.radars[1], flight1) && allAirfieldRadarNear(group.radars[2], flight0)
	return ordered || swapped
}

func allAirfieldRadarNear(radar *airfieldRadar, point [3]float64) bool {
	if radar == nil || radar.info.Position == nil || !allAirfieldVectorBounded(point) {
		return false
	}
	dx := float64(radar.info.Position.X) - point[0]
	dz := float64(radar.info.Position.Z) - point[2]
	return allAirfieldBounded(dx) && allAirfieldBounded(dz) && math.Hypot(dx, dz) <= allAirfieldRadarToleranceMeters
}

func allAirfieldRadarEvidence(group *allAirfieldRadarGroup) (float64, uint32, []uint64, bool) {
	if group == nil || group.radars[0] == nil || group.radars[1] == nil || group.radars[2] == nil {
		return 0, 0, nil, false
	}
	var y float64
	var stamp uint32
	uids := make([]uint64, 0, 3)
	for _, radar := range group.radars {
		if radar == nil || radar.info.Position == nil {
			return 0, 0, nil, false
		}
		y += float64(radar.info.Position.Y)
		if radar.info.ConstructionTime > stamp {
			stamp = radar.info.ConstructionTime
		}
		uids = append(uids, uint64(radar.info.UID))
	}
	y /= 3
	if !allAirfieldBounded(y) {
		return 0, 0, nil, false
	}
	return y, stamp, uids, true
}
