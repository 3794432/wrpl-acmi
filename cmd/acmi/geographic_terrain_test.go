package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
)

func TestGeographicAllPositionFormatsUseSameTransform(t *testing.T) {
	old := activeGeographicProfile
	activeGeographicProfile = &terrainmap.GeographicProfile{
		Enabled: true, LocalBounds: [4]float64{-1000, -2000, 1000, 2000},
		GeoBounds: [4]float64{30, 40, 32, 44},
	}
	t.Cleanup(func() { activeGeographicProfile = old })
	s := acmiSample{x: 500, y: 123, z: -1000, roll: 1, pitch: 2, yaw: 3}
	for _, position := range []string{acmiPositionValue(s), acmiBombPositionValue(s), acmiMissilePositionValue(s)} {
		if !strings.HasPrefix(position, "31.5000000|41.0000000|") {
			t.Fatalf("object position bypassed shared georeference: %s", position)
		}
	}
	if !strings.HasSuffix(acmiMissilePositionValue(s), "|500.000|-1000.000") {
		t.Fatal("native missile U/V coordinates were changed")
	}
}

func TestGeographicWriterAndAtlasFallback(t *testing.T) {
	setTestTerrainLayout(t, terrainLayoutGeographic)
	oldLat, oldLon := atlasLatitude, atlasLongitude
	t.Cleanup(func() { atlasLatitude, atlasLongitude = oldLat, oldLon })
	for _, mapID := range []string{"air_afghan", "air_grand_canyon"} {
		if err := applyAtlasReference(mapID); err != nil {
			t.Fatal(err)
		}
		tr := &track{key: trackKey{air: true, id: 1}, samples: []acmiSample{{t: 1000, x: 1000, y: 123, z: 2000, roll: 1, pitch: 2, yaw: 3}}}
		path := filepath.Join(t.TempDir(), "geographic.acmi")
		if err := writeACMIWithEventsMapID(path, wrpl.WRPLHeader{}, []*track{tr}, nil, nil, nil, mapID); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		profile, found := terrainmap.GeographicReference(mapID)
		var wantLat, wantLon float64
		if mapID == "air_afghan" {
			if !found || !profile.Enabled {
				t.Fatal("reviewed Afghan georeference is missing")
			}
			wantLat, wantLon = profile.Location(1000, 2000)
			if !strings.Contains(text, "MapId=WarThunder.Geo1.air_afghan\n") || !strings.Contains(text, "WT_TerrainLayout=GeographicV1\n") {
				t.Fatal("geographic map metadata missing")
			}
		} else {
			if found && profile.Enabled {
				t.Fatal("known incorrect Grand Canyon georeference was enabled")
			}
			wantLat, wantLon = localToLatLon(1000, 2000, -60, 100)
			if !strings.Contains(text, "MapId=WarThunder.Atlas1.air_grand_canyon\n") || !strings.Contains(text, "WT_GeoreferenceStatus=AtlasFallback\n") {
				t.Fatal("fallback metadata missing")
			}
		}
		position := acmiFields(t, text, "1")
		if math.Abs(position[0]-wantLon) > 1e-7 || math.Abs(position[1]-wantLat) > 1e-7 || position[3] != 1 || position[4] != 2 || position[5] != 3 {
			t.Fatalf("incorrect position or changed attitude: %v", position)
		}
		if !strings.Contains(text, "WT_GeoreferenceConfigSHA256="+terrainmap.GeographicConfigSHA256()+"\n") {
			t.Fatal("configuration fingerprint missing")
		}
		if activeGeographicProfile != nil {
			t.Fatal("writer leaked its map transform into the next replay")
		}
	}
}
