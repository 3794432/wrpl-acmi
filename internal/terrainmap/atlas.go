package terrainmap

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// atlasReferencesJSON is the frozen War Thunder Atlas V1 placement table. The
// coordinates separate map footprints for inspection; they are not real-world
// locations. Keep the file and its order stable so MapId consumers see one
// reference position for every catalog map.
//
//go:embed atlas_references.json
var atlasReferencesJSON []byte

type atlasCoordinates struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

var atlasReferences = loadAtlasReferences(atlasReferencesJSON)

func loadAtlasReferences(data []byte) map[string]atlasCoordinates {
	refs := map[string]atlasCoordinates{}
	if err := json.Unmarshal(data, &refs); err != nil {
		panic(fmt.Sprintf("terrainmap: invalid embedded atlas_references.json: %v", err))
	}
	if len(refs) != len(knownIDs) {
		panic(fmt.Sprintf("terrainmap: atlas has %d references, want %d", len(refs), len(knownIDs)))
	}
	for _, id := range knownIDs {
		if _, ok := refs[id]; !ok {
			panic(fmt.Sprintf("terrainmap: atlas is missing %q", id))
		}
	}
	return refs
}

// AtlasReference returns the technical Atlas V1 anchor for id.
func AtlasReference(id string) (latitude, longitude float64, ok bool) {
	id = strings.TrimSpace(id)
	lower := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lower, strings.ToLower(atlasMapIDPrefix)):
		id = id[len(atlasMapIDPrefix):]
	case strings.HasPrefix(lower, strings.ToLower(mapIDPrefix)):
		id = id[len(mapIDPrefix):]
	}
	ref, ok := atlasReferences[strings.ToLower(id)]
	if !ok {
		return 0, 0, false
	}
	return ref.Latitude, ref.Longitude, true
}
