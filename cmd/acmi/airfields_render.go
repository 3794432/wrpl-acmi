package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// airfieldMarker is an already classified airport or approach/threshold
// anchor. Classification and evidence belong to the caller; this file only
// renders the supplied values into an ACMI object stream.
type airfieldMarker struct {
	Key             string
	Name            string
	Role            string
	Source          string
	Confidence      string
	MissionName     string
	Prefab          string
	GeometrySource  string
	PositionSource  string
	ElevationSource string
	HeadingSource   string
	ParentKey       string
	NextKey         string
	T               uint32
	X, Y, Z         float64
	Heading         float64
	HasHeading      bool
	Length, Width   float64
	Team            int32
	UnitUIDs        []uint64
}

const (
	airfieldPositionSource  = "AirfieldMarker"
	airfieldMaxLengthMeters = 50_000.0
	airfieldMaxWidthMeters  = 1_000.0
)

// airfieldLines reserves object IDs by input position. IDs for rejected
// markers remain gaps so later assignments never change under filtering.
func airfieldLines(markers []airfieldMarker, existingObjectCount int) map[uint32][]string {
	lines := make(map[uint32][]string)
	ids := make([]uint32, len(markers))
	valid := make([]bool, len(markers))
	byKey := make(map[string]uint32, len(markers))

	// Resolve the complete valid key set before rendering so Parent references
	// never depend on marker order.
	for i, marker := range markers {
		if strings.TrimSpace(marker.Key) == "" || !finiteGroundPosition(marker.X, marker.Y, marker.Z) {
			continue
		}
		if marker.HasHeading && !airfieldFinite(marker.Heading) {
			continue
		}
		if _, duplicate := byKey[marker.Key]; duplicate {
			continue
		}
		id, ok := airfieldObjectID(existingObjectCount, i)
		if !ok {
			continue
		}
		ids[i] = id
		valid[i] = true
		byKey[marker.Key] = id
	}

	for i, marker := range markers {
		if !valid[i] {
			continue
		}
		coalition, group := airfieldCoalition(marker.Team)
		color := coalition
		if color == "Neutral" {
			color = "White"
		}
		name := escapeACMIValue(marker.Name)
		props := []string{
			"Name=" + name,
			"CallSign=" + name,
		}
		if parentID, ok := byKey[marker.ParentKey]; ok && marker.ParentKey != "" {
			props = append(props, fmt.Sprintf("Parent=%X", parentID))
		}
		if nextID, ok := byKey[marker.NextKey]; ok && marker.NextKey != "" && marker.NextKey != marker.Key {
			props = append(props, fmt.Sprintf("Next=%X", nextID))
		}
		typ := "Navaid+Static+Waypoint"
		if marker.Role == "Airport" {
			typ = "Ground+Static+Aerodrome"
		}
		props = append(props,
			"Type="+typ,
			"Coalition="+coalition,
			"Color="+color,
			"Group="+escapeACMIValue(group),
		)

		props = airfieldTextEvidence(props, "WT_AirfieldRole", marker.Role)
		props = airfieldTextEvidence(props, "WT_AirfieldSource", marker.Source)
		props = airfieldTextEvidence(props, "WT_AirfieldConfidence", marker.Confidence)
		props = airfieldTextEvidence(props, "WT_AirfieldMissionName", marker.MissionName)
		props = airfieldTextEvidence(props, "WT_AirfieldPrefab", marker.Prefab)
		props = airfieldTextEvidence(props, "WT_AirfieldGeometrySource", marker.GeometrySource)
		props = airfieldTextEvidence(props, "WT_AirfieldElevationSource", marker.ElevationSource)
		props = airfieldTextEvidence(props, "WT_AirfieldHeadingSource", marker.HeadingSource)
		if airfieldDimension(marker.Length, airfieldMaxLengthMeters) {
			props = append(props, "WT_AirfieldLength="+strconv.FormatFloat(marker.Length, 'f', -1, 64))
			if marker.Role == "Airport" {
				props = append(props, "Length="+strconv.FormatFloat(marker.Length, 'f', 2, 64))
			}
		}
		if airfieldDimension(marker.Width, airfieldMaxWidthMeters) {
			props = append(props, "WT_AirfieldWidth="+strconv.FormatFloat(marker.Width, 'f', -1, 64))
			if marker.Role == "Airport" {
				props = append(props, "Width="+strconv.FormatFloat(marker.Width, 'f', 2, 64))
			}
		}
		if len(marker.UnitUIDs) > 0 {
			uids := make([]string, len(marker.UnitUIDs))
			for j, uid := range marker.UnitUIDs {
				uids[j] = strconv.FormatUint(uid, 10)
			}
			props = append(props, "WT_AirfieldUnitUIDs="+escapeACMIValue(strings.Join(uids, ";")))
		}
		positionSource := marker.PositionSource
		if positionSource == "" {
			positionSource = airfieldPositionSource
		}
		props = append(props, "WT_AirfieldPositionSource="+escapeACMIValue(positionSource))

		id := fmt.Sprintf("%X", ids[i])
		line := id + "," + strings.Join(props, ",") + ",T=" + airfieldPositionValue(marker)
		lines[marker.T] = append(lines[marker.T], line)
	}
	return lines
}

func airfieldObjectID(existingObjectCount, index int) (uint32, bool) {
	if existingObjectCount < 0 || index < 0 {
		return 0, false
	}
	const maxID = uint64(^uint32(0))
	sum := uint64(existingObjectCount) + uint64(index)
	if sum >= maxID {
		return 0, false
	}
	id := uint32(sum + 1)
	return id, id != 0
}

func airfieldFinite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func airfieldDimension(value, maximum float64) bool {
	return value > 0 && value <= maximum && airfieldFinite(value)
}

func airfieldTextEvidence(props []string, key, value string) []string {
	if value == "" {
		return props
	}
	return append(props, key+"="+escapeACMIValue(value))
}

func airfieldCoalition(team int32) (coalition, group string) {
	switch team {
	case 1:
		coalition = "Blue"
	case 2:
		coalition = "Red"
	default:
		coalition = "Neutral"
	}
	return coalition, "War Thunder " + coalition
}

func airfieldPositionValue(marker airfieldMarker) string {
	lat, lon := terrainPosition(marker.X, marker.Z)
	position := fmt.Sprintf("%.7f|%.7f|%.2f", lon, lat, *alt0+marker.Y)
	if marker.HasHeading {
		position += fmt.Sprintf("|0.00|0.00|%.2f", marker.Heading)
	}
	return position
}
