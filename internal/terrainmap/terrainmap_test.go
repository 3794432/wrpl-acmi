package terrainmap

import "testing"

func TestCatalogHas68ActualMaps(t *testing.T) {
	ids := KnownIDs()
	if len(ids) != 68 {
		t.Fatalf("catalog size = %d, want 68", len(ids))
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

func TestMapIDNormalizationAndFilenameTokens(t *testing.T) {
	for input, want := range map[string]string{
		"air_grand_canyon":            "air_grand_canyon",
		"WarThunder.air_grand_canyon": "air_grand_canyon",
		"warthunder.AIR_GRAND_CANYON": "air_grand_canyon",
	} {
		if got, err := NormalizeMapID(input); err != nil || got != want {
			t.Errorf("NormalizeMapID(%q) = %q, %v", input, got, err)
		}
	}
	for _, bad := range []string{"", "grand canyon", "../air_grand_canyon", "WarThunder.unknown"} {
		if _, err := NormalizeMapID(bad); err == nil {
			t.Errorf("unsafe map id %q accepted", bad)
		}
	}
	if id, ok := FromFilename("2026_09_17_air_grand_canyon_named.acmi"); !ok || id != "air_grand_canyon" {
		t.Fatalf("filename token = %q, %v", id, ok)
	}
	if id, ok := FromFilename("arcade_phiphi_crater_rocks.acmi"); !ok || id != "arcade_phiphi_crater_rocks" {
		t.Fatalf("longest filename token = %q, %v", id, ok)
	}
	if id, ok := FromFilename("mozdok_winter.acmi"); !ok || id != "mozdok_winter" {
		t.Fatalf("contained prefix resolved as %q, %v", id, ok)
	}
	if _, ok := FromFilename("coordinate69_34_unknown.acmi"); ok {
		t.Fatal("unknown filename guessed")
	}
}

func TestFromFilenameRejectsSeparateTokensRegardlessOfLength(t *testing.T) {
	for _, name := range []string{
		"berlin_vs_air_grand_canyon.acmi",
		"spain_berlin.acmi",
		"air_afghan_vs_berlin.acmi",
		"mozdok_vs_air_grand_canyon.acmi",
	} {
		if id, ok := FromFilename(name); ok {
			t.Errorf("ambiguous filename %q resolved as %q", name, id)
		}
	}
}
