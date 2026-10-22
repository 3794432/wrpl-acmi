package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

//go:embed data/airfields.json
var airfieldCatalogJSON []byte

type airfieldPlacement struct {
	Name   string        `json:"name"`
	Team   int32         `json:"team"`
	TM     [4][3]float64 `json:"tm"`
	Source string        `json:"source"`
}

type airfieldCatalogData struct {
	Schema         int                 `json:"schema"`
	Commit         string              `json:"commit"`
	MapID          string              `json:"map_id"`
	Mission        string              `json:"mission"`
	Prefab         string              `json:"prefab"`
	GeometrySource string              `json:"geometry_source"`
	Radars         [3][3]float64       `json:"radars"`
	Start          [3]float64          `json:"start"`
	End            [3]float64          `json:"end"`
	Width          float64             `json:"width"`
	Placements     []airfieldPlacement `json:"placements"`
}

var airfieldCatalog = func() airfieldCatalogData {
	var data airfieldCatalogData
	if err := json.Unmarshal(airfieldCatalogJSON, &data); err != nil {
		panic(err)
	}
	if data.Schema != 1 || len(data.Commit) != 40 || len(data.Placements) > 64 {
		panic("invalid embedded airport catalogue")
	}
	return data
}()

var airfieldMissionPattern = regexp.MustCompile(`^(t[12])_airfield_(main_radar|flight_radar_01|flight_radar_02)(_clone_[0-9]+)?$`)

func countAirportMarkers(markers []airfieldMarker) int {
	count := 0
	for _, m := range markers {
		if m.Role == "Airport" {
			count++
		}
	}
	return count
}

type airfieldRadar struct {
	info groundmeta.Info
	role int
}

// The name is explicit replay identity evidence. Geometry alone is never used
// to join two radar objects or choose a runway prefab.
func airfieldRadarRole(info groundmeta.Info) (int, bool) {
	switch groundModelKey(info.RawModel) {
	case "unit_airfield_main_radar_char", "unit_airfield_main_radar_snow_char":
		return 0, true
	case "unit_airfield_flight_radar_char", "unit_airfield_flight_radar_snow_char":
		m := airfieldMissionPattern.FindStringSubmatch(info.MissionName)
		if m == nil {
			return -1, true
		}
		if m[2] == "flight_radar_01" {
			return 1, true
		}
		if m[2] == "flight_radar_02" {
			return 2, true
		}
		return -1, true
	}
	return -1, false
}

func airfieldReplayRadars(metadata *groundmeta.Parser) []airfieldRadar {
	if metadata == nil {
		return nil
	}
	bad := map[uint64]bool{}
	first := map[uint64]groundmeta.Info{}
	for _, info := range metadata.All {
		_, radar := airfieldRadarRole(info)
		if !radar {
			continue
		}
		uid := uint64(info.UID)
		if old, found := first[uid]; found && (old.EID != info.EID || old.MissionName != info.MissionName || old.RawModel != info.RawModel) {
			bad[uid] = true
		}
		if info.Conflict || info.ClassConflict || !info.HasConstructionTime || info.Position == nil {
			bad[uid] = true
			continue
		}
		p := info.Position
		if !finiteGroundPosition(float64(p.X), float64(p.Y), float64(p.Z)) {
			bad[uid] = true
			continue
		}
		if old, found := first[uid]; found {
			o := old.Position
			if math.Abs(float64(p.X)-float64(o.X)) > .1 || math.Abs(float64(p.Y)-float64(o.Y)) > .1 || math.Abs(float64(p.Z)-float64(o.Z)) > .1 {
				bad[uid] = true
			}
			if info.ConstructionTime < old.ConstructionTime {
				first[uid] = info
			}
		} else {
			first[uid] = info
		}
	}
	uids := make([]uint64, 0, len(first))
	for uid := range first {
		uids = append(uids, uid)
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	var out []airfieldRadar
	for _, uid := range uids {
		info := first[uid]
		latest, found := metadata.Models[uid]
		if bad[uid] || !found || latest.Conflict || latest.ClassConflict || latest.EID != info.EID || latest.MissionName != info.MissionName {
			continue
		}
		role, _ := airfieldRadarRole(info)
		if role < 0 {
			continue
		}
		out = append(out, airfieldRadar{info: info, role: role})
	}
	return out
}

type airfieldRadarGroup struct {
	key       string
	radars    [3]*airfieldRadar
	ambiguous bool
}

func prepareAirfields(header wrpl.WRPLHeader, mapID string, metadata *groundmeta.Parser) []airfieldMarker {
	if markers, configured := prepareAllAirfields(header, mapID, metadata); configured && len(markers) > 0 {
		return markers
	}
	return prepareReviewedAirfields(header, mapID, metadata)
}

// Preserve source-validated legacy geometry and facility-only anchors when a
// replay mission has no active runway in the broader source catalogue.
func prepareReviewedAirfields(header wrpl.WRPLHeader, mapID string, metadata *groundmeta.Parser) []airfieldMarker {
	radars := airfieldReplayRadars(metadata)
	groups := map[string]*airfieldRadarGroup{}
	for i := range radars {
		r := &radars[i]
		m := airfieldMissionPattern.FindStringSubmatch(r.info.MissionName)
		key := fmt.Sprintf("radar:%d", r.info.UID)
		if m != nil {
			expected := int32(m[1][1] - '0')
			if r.info.Team == nil || *r.info.Team != expected {
				continue
			}
			if (r.role == 0) != (m[2] == "main_radar") {
				continue
			}
			key = m[1] + m[3]
		} else if r.role != 0 {
			continue
		}
		g := groups[key]
		if g == nil {
			g = &airfieldRadarGroup{key: key}
			groups[key] = g
		}
		if g.radars[r.role] != nil {
			g.ambiguous = true
		} else {
			g.radars[r.role] = r
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var markers []airfieldMarker
	counts := map[int32]int{}
	for _, key := range keys {
		g := groups[key]
		if g.ambiguous {
			continue
		}
		anchor := g.radars[0]
		if anchor == nil {
			if g.radars[1] != nil {
				anchor = g.radars[1]
			} else {
				anchor = g.radars[2]
			}
		}
		if anchor == nil {
			continue
		}
		team := int32(0)
		if anchor.info.Team != nil {
			team = *anchor.info.Team
		}
		coalition, _ := airfieldCoalition(team)
		counts[team]++
		name := fmt.Sprintf("%s Airfield %d", coalition, counts[team])
		p := anchor.info.Position
		marker := airfieldMarker{Key: key, Name: name + " (radar anchor)", Role: "Airport", Team: team,
			T: anchor.info.ConstructionTime, X: float64(p.X), Y: float64(p.Y), Z: float64(p.Z),
			Source: "ReplayECS:unit__missionName+unit__className+transform", Confidence: "FacilityPositionOnly",
			PositionSource: "ReplayRadarTransform", ElevationSource: "ReplayRadarGroundHeight", MissionName: anchor.info.MissionName}
		for _, r := range g.radars {
			if r != nil {
				marker.UnitUIDs = append(marker.UnitUIDs, uint64(r.info.UID))
				if r.info.ConstructionTime > marker.T {
					marker.T = r.info.ConstructionTime
				}
			}
		}
		placement, ok := matchAirfieldPlacement(header, mapID, g, &airfieldCatalog)
		if !ok {
			markers = append(markers, marker)
			continue
		}
		start := airfieldPrefabToWorld(placement.TM, airfieldCatalog.Start)
		end := airfieldPrefabToWorld(placement.TM, airfieldCatalog.End)
		// Horizontal geometry is source-backed and independently checked by all
		// three replay radars. Grounding changes Y; use their mean as a labelled
		// flat-airfield height approximation, not the nominal template-origin Y.
		marker.Y = 0
		for _, r := range g.radars {
			marker.Y += float64(r.info.Position.Y) / 3
		}
		marker.X, marker.Z = (start[0]+end[0])/2, (start[2]+end[2])/2
		marker.Name = name
		marker.MissionName = placement.Name
		marker.Prefab = airfieldCatalog.Prefab
		marker.Source = "Datamine@" + airfieldCatalog.Commit + ":" + placement.Source
		marker.GeometrySource = airfieldCatalog.GeometrySource + "#/airfield/start,end,width"
		marker.PositionSource = "MissionPrefabTransformValidatedByThreeReplayRadars"
		marker.ElevationSource = "MeanReplayRadarGroundHeightApproximation"
		marker.Confidence = "ReviewedHorizontalGeometry;EstimatedElevation"
		marker.HeadingSource = "RunwayStartToEndGeometry;LandingDirectionUnknown"
		marker.HasHeading = true
		marker.Heading = airfieldBearing(start, end)
		marker.Length = math.Hypot(end[0]-start[0], end[2]-start[2])
		marker.Width = airfieldCatalog.Width
		markers = append(markers, marker)
		for i, point := range [][3]float64{start, end} {
			endpoint := marker
			endpoint.Key = fmt.Sprintf("%s:endpoint%d", key, i+1)
			endpoint.ParentKey = key
			endpoint.Role = "RunwayEndpoint"
			endpoint.Length, endpoint.Width = 0, 0
			endpoint.X, endpoint.Z = point[0], point[2]
			endpoint.Name = fmt.Sprintf("%s RWY endpoint %d", name, i+1)
			if i == 0 {
				endpoint.NextKey = fmt.Sprintf("%s:endpoint2", key)
			}
			if i == 1 {
				endpoint.Heading = airfieldBearing(end, start)
			}
			markers = append(markers, endpoint)
		}
	}
	return markers
}

func airfieldPrefabToWorld(tm [4][3]float64, local [3]float64) [3]float64 {
	var result [3]float64
	for axis := range result {
		result[axis] = tm[3][axis]
		for row := 0; row < 3; row++ {
			result[axis] += tm[row][axis] * local[row]
		}
	}
	return result
}

func airfieldBearing(from, to [3]float64) float64 {
	angle := math.Atan2(to[0]-from[0], to[2]-from[2]) * 180 / math.Pi
	return math.Mod(angle+360, 360)
}

func matchAirfieldPlacement(header wrpl.WRPLHeader, mapID string, g *airfieldRadarGroup, catalog *airfieldCatalogData) (airfieldPlacement, bool) {
	if mapID != catalog.MapID || !strings.EqualFold(headerText(header.Raw_BattleType[:]), catalog.Mission) ||
		!strings.EqualFold(headerText(header.Raw_Level[:]), "levels/"+catalog.MapID+".bin") {
		return airfieldPlacement{}, false
	}
	for _, r := range g.radars {
		if r == nil || r.info.Team == nil {
			return airfieldPlacement{}, false
		}
	}
	var matches []airfieldPlacement
	for _, placement := range catalog.Placements {
		if placement.Team != *g.radars[0].info.Team {
			continue
		}
		ok := true
		for role, r := range g.radars {
			if *r.info.Team != placement.Team {
				ok = false
				break
			}
			expected := airfieldPrefabToWorld(placement.TM, catalog.Radars[role])
			p := r.info.Position
			if math.Hypot(float64(p.X)-expected[0], float64(p.Z)-expected[2]) > 2 {
				ok = false
				break
			}
		}
		if ok {
			matches = append(matches, placement)
		}
	}
	if len(matches) != 1 {
		return airfieldPlacement{}, false
	}
	return matches[0], true
}
