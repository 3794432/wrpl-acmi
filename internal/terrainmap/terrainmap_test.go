package terrainmap

import "testing"

func TestCatalogHas71ActualMaps(t *testing.T) {
	ids := KnownIDs()
	if len(ids) != 71 {
		t.Fatalf("catalog size = %d, want 71", len(ids))
	}
	if !IsKnown("air_grand_canyon") || IsKnown("air_race_africa_canyon") {
		t.Fatal("catalog must include air_grand_canyon and exclude the missing race map")
	}
}

func TestResolveRealReplayFields(t *testing.T) {
	id, err := ResolveReplay("levels/air_grand_canyon.bin", "missions/_BfD_norespawn;location/air_grand_canyon")
	if err != nil || id != "air_grand_canyon" {
		t.Fatalf("resolved %q, %v", id, err)
	}
	if _, err := ResolveReplay("levels/air_grand_canyon.bin", "missions/x;location/korea"); err == nil {
		t.Fatal("conflicting real header fields must be rejected")
	}
	if _, err := ResolveReplay("levels/not_a_map.bin", "missions/x;location/also_not_a_map"); err == nil {
		t.Fatal("unknown header fields must not be guessed")
	}
}

func TestResolveAvgEgyptSinaiHeaderWithoutMap(t *testing.T) {
	id, err := ResolveReplay("levels/avg_egypt_sinai.bin", "")
	if err != nil || id != "avg_egypt_sinai" {
		t.Fatalf("resolved header level %q as %q, %v", "levels/avg_egypt_sinai.bin", id, err)
	}
}

func TestTournamentMapsUseIndependentAtlasFallback(t *testing.T) {
	for index, id := range []string{"avn_japan", "avg_red_desert"} {
		t.Run(id, func(t *testing.T) {
			got, err := ResolveReplay("levels/"+id+".bin", "missions/tournament;location/"+id)
			if err != nil || got != id {
				t.Fatalf("ResolveReplay = %q, %v", got, err)
			}
			lat, lon, ok := AtlasReference(id)
			if !ok || lat != 60 || lon != float64(60+40*index) {
				t.Fatalf("AtlasReference = (%v,%v,%v)", lat, lon, ok)
			}
			profile, ok := GeographicReference(id)
			if !ok || profile.Enabled {
				t.Fatal("unreviewed tournament map must have an explicit Atlas fallback")
			}
		})
	}
}

func TestNormalizeMapIDAcceptsCurrentFormats(t *testing.T) {
	for input, want := range map[string]string{
		"air_grand_canyon":                   "air_grand_canyon",
		"WarThunder.Geo1.air_grand_canyon":   "air_grand_canyon",
		"warthunder.GEO1.AIR_GRAND_CANYON":   "air_grand_canyon",
		"WarThunder.Atlas1.air_grand_canyon": "air_grand_canyon",
		"warthunder.atlas1.AIR_GRAND_CANYON": "air_grand_canyon",
	} {
		if got, err := NormalizeMapID(input); err != nil || got != want {
			t.Errorf("NormalizeMapID(%q) = %q, %v", input, got, err)
		}
	}
	for _, bad := range []string{
		"",
		"grand canyon",
		"../air_grand_canyon",
		"WarThunder.air_grand_canyon",
		"warthunder.AIR_GRAND_CANYON",
		"WarThunder.unknown",
	} {
		if _, err := NormalizeMapID(bad); err == nil {
			t.Errorf("unsupported map id %q accepted", bad)
		}
	}
}
