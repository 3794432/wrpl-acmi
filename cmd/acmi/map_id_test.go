package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
)

func TestACMIWritesGlobalAtlasMapID(t *testing.T) {
	setTestTerrainLayout(t, terrainLayoutAtlas)
	path := filepath.Join(t.TempDir(), "map.acmi")
	if err := writeACMIWithEventsMapID(path, wrpl.WRPLHeader{}, nil, nil, nil, nil, "air_grand_canyon"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\n0,MapId=WarThunder.Atlas1.air_grand_canyon\n") {
		t.Fatalf("global Atlas MapId missing:\n%s", b)
	}
	if !strings.Contains(string(b), "\n0,WT_TerrainLayout=AtlasV1\n") {
		t.Fatalf("Atlas audit metadata missing:\n%s", b)
	}
}

func TestAtlasLayoutAppliesCatalogAnchor(t *testing.T) {
	oldLat, oldLon := atlasLatitude, atlasLongitude
	t.Cleanup(func() { atlasLatitude, atlasLongitude = oldLat, oldLon })
	if err := applyAtlasReference("air_grand_canyon"); err != nil {
		t.Fatal(err)
	}
	if atlasLatitude != -60 || atlasLongitude != 100 {
		t.Fatalf("atlas anchor = (%v,%v)", atlasLatitude, atlasLongitude)
	}
	if err := applyAtlasReference(""); err != nil {
		t.Fatal(err)
	}
	if atlasLatitude != 0 || atlasLongitude != 0 {
		t.Fatal("unknown map retained a previous replay anchor")
	}
}

func TestTerrainLayoutRejectsRemovedLegacyMode(t *testing.T) {
	if _, err := parseTerrainLayout("legacy"); err == nil {
		t.Fatal("removed terrain layout was accepted")
	}
}

func TestReplayMapIDFromRealHeaderFieldsAndOverride(t *testing.T) {
	var header wrpl.WRPLHeader
	copy(header.Raw_Level[:], "levels/air_grand_canyon.bin")
	copy(header.Raw_LocName[:], "missions/_BfD_norespawn;location/air_grand_canyon")
	if got, err := resolveReplayMapID(header, ""); err != nil || got != "air_grand_canyon" {
		t.Fatalf("real fields resolved %q, %v", got, err)
	}
	if got, err := resolveReplayMapID(header, "WarThunder.Geo1.spain"); err != nil || got != "spain" {
		t.Fatalf("explicit override resolved %q, %v", got, err)
	}
	if _, err := resolveReplayMapID(header, "WarThunder.not_a_map"); err == nil {
		t.Fatal("unknown explicit map was accepted")
	}
	if got, err := resolveReplayMapID(wrpl.WRPLHeader{}, ""); err != nil || got != "" {
		t.Fatalf("unknown header guessed %q, %v", got, err)
	}
}

func TestInvalidExplicitMapIDFailsBeforeOpeningInputOrWritingOutput(t *testing.T) {
	if os.Getenv("ACMI_TEST_INVALID_MAP") == "1" {
		dir := os.Getenv("ACMI_TEST_INVALID_DIR")
		*mapIDOverride = "WarThunder.not_a_map"
		*inPath = filepath.Join(dir, "missing.wrpl")
		*outPath = filepath.Join(dir, "must-not-exist.acmi")
		main()
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestInvalidExplicitMapIDFailsBeforeOpeningInputOrWritingOutput$")
	cmd.Env = append(os.Environ(), "ACMI_TEST_INVALID_MAP=1", "ACMI_TEST_INVALID_DIR="+dir)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid explicit map unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "unknown War Thunder map") {
		t.Fatalf("validation did not fail first: %s", output)
	}
	if _, err := os.Stat(filepath.Join(dir, "must-not-exist.acmi")); !os.IsNotExist(err) {
		t.Fatalf("invalid explicit map left an output: %v", err)
	}
}
