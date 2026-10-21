package main

import (
	"fmt"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
)

// A writer scopes this projection to one replay. Every object kind goes through
// terrainPosition, while native flight parameters and altitude stay in game units.
var activeGeographicProfile *terrainmap.GeographicProfile

func terrainPosition(x, z float64) (latitude, longitude float64) {
	if activeGeographicProfile != nil {
		return activeGeographicProfile.Location(x, z)
	}
	return localToLatLon(x, z, *centerX, *centerZ, *lat0, *lon0)
}

func geographicFallbackReason(profile terrainmap.GeographicProfile, found bool) string {
	if found && profile.Reason != "" {
		return profile.Reason
	}
	return "no reviewed geographic reference"
}

func terrainGlobalProperties(layout terrainLayout, mapID string) []string {
	var properties []string
	if layout == terrainLayoutGeographic {
		profile, found := terrainmap.GeographicReference(mapID)
		properties = append(properties,
			"WT_GeoreferenceCommit="+escapeACMIValue(terrainmap.GeographicSourceCommit()),
			"WT_GeoreferenceConfigSHA256="+terrainmap.GeographicConfigSHA256())
		if found && profile.Enabled {
			properties = append(properties,
				"MapId="+terrainmap.FormatGeographicMapID(mapID),
				"WT_TerrainLayout=GeographicV1",
				"WT_GeoreferenceStatus=Estimated",
				"WT_GeoreferenceConfidence="+escapeACMIValue(profile.Confidence),
				"WT_GeoreferenceMethod="+escapeACMIValue(profile.Method),
				"WT_GeoreferenceSource="+escapeACMIValue(profile.Source),
				"WT_AltitudeDatum=GameHeight")
			if profile.UncertaintyKM > 0 {
				properties = append(properties, fmt.Sprintf("WT_GeoreferenceUncertaintyKm=%.3f", profile.UncertaintyKM))
			}
			return properties
		}
		properties = append(properties,
			"WT_GeoreferenceStatus=AtlasFallback",
			"WT_GeoreferenceReason="+escapeACMIValue(geographicFallbackReason(profile, found)))
		layout = terrainLayoutAtlas
	}
	if mapID != "" {
		if layout == terrainLayoutAtlas {
			properties = append(properties, "MapId="+terrainmap.FormatAtlasMapID(mapID))
		} else {
			properties = append(properties, "MapId="+terrainmap.FormatMapID(mapID))
		}
	}
	if layout == terrainLayoutAtlas {
		properties = append(properties, "WT_TerrainLayout=AtlasV1")
	}
	return properties
}
