package terrainmap

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func TestAtlasReferencesAreFrozenInCatalogOrder(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(atlasReferencesJSON))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		t.Fatalf("atlas JSON must start with an object: token=%v err=%v", token, err)
	}
	index := 0
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		id, ok := token.(string)
		if !ok {
			t.Fatalf("atlas key %d is %T, want string", index, token)
		}
		if index >= len(knownIDs) || id != knownIDs[index] {
			t.Fatalf("atlas key %d = %q, want %q", index, id, knownIDs[index])
		}
		var ref atlasCoordinates
		if err := dec.Decode(&ref); err != nil {
			t.Fatalf("decoding %q: %v", id, err)
		}
		wantLat := float64(-60 + 15*(index/8))
		wantLon := float64(-140 + 40*(index%8))
		if ref.Latitude != wantLat || ref.Longitude != wantLon {
			t.Fatalf("%q atlas = (%v,%v), want (%v,%v)", id, ref.Latitude, ref.Longitude, wantLat, wantLon)
		}
		if math.Abs(ref.Latitude) > 60 {
			t.Fatalf("%q latitude %v exceeds the Atlas V1 limit", id, ref.Latitude)
		}
		index++
	}
	if index != 68 {
		t.Fatalf("atlas contains %d entries, want 68", index)
	}
}

func TestAtlasReferenceAndMapIDFormats(t *testing.T) {
	lat, lon, ok := AtlasReference("air_grand_canyon")
	if !ok || lat != -60 || lon != 100 {
		t.Fatalf("AtlasReference = (%v,%v,%v), want (-60,100,true)", lat, lon, ok)
	}
	if lat, lon, ok := AtlasReference(FormatAtlasMapID("zhengzhou")); !ok || lat != 60 || lon != -20 {
		t.Fatalf("prefixed AtlasReference = (%v,%v,%v)", lat, lon, ok)
	}
	if _, _, ok := AtlasReference("not_a_map"); ok {
		t.Fatal("unknown atlas reference was accepted")
	}
	if got := FormatAtlasMapID("air_grand_canyon"); got != "WarThunder.Atlas1.air_grand_canyon" {
		t.Fatalf("FormatAtlasMapID = %q", got)
	}
	for input, want := range map[string]string{
		"WarThunder.Atlas1.air_grand_canyon": "air_grand_canyon",
		"warthunder.atlas1.AIR_GRAND_CANYON": "air_grand_canyon",
	} {
		if got, err := NormalizeMapID(input); err != nil || got != want {
			t.Errorf("NormalizeMapID(%q) = %q, %v", input, got, err)
		}
	}
}
