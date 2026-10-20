package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
)

func fixture(mapID string) string {
	return "FileType=text/acmi/tacview\r\n" +
		"FileVersion=2.1\r\n" +
		"0,ReferenceTime=2026-01-01T00:00:00Z,ReferenceLongitude=91.25,ReferenceLatitude=47.5,ReferenceAltitude=321.75,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder." + mapID + ",Comment=keep\\,this\r\n" +
		"1,Name=Plane\\, One,CallSign=Pilot\r\n" +
		"2,Name=Missile\r\n" +
		"#0.000\r\n" +
		"1,T=10.0000000|20.0000000|1234.567|100.123|140.987|-15.5|12.5|-30.25,Color=Red,Note=keep=this\r\n" +
		"2,T=30.0000000|40.0000000|555.5|17|-42,Name=still-here\r\n"
}

func atlasCoordinate(anchorLat, anchorLon, u, v float64) (string, string) {
	lat := anchorLat + v/111320
	lon := anchorLon + u/(111320*math.Cos(lat*math.Pi/180))
	return strconv.FormatFloat(lon, 'f', 7, 64), strconv.FormatFloat(lat, 'f', 7, 64)
}

func TestMigratesRealT5AndT8AndPreservesTimelineMetadata(t *testing.T) {
	anchorLat, anchorLon, ok := terrainmap.AtlasReference("spain")
	if !ok {
		t.Fatal("spain has no Atlas reference")
	}
	var out bytes.Buffer
	info, err := transformACMI(strings.NewReader(fixture("spain")), &out, "spain")
	if err != nil {
		t.Fatal(err)
	}
	if info.transformed != 2 {
		t.Fatalf("transformed %d T records", info.transformed)
	}
	lon8, lat8 := atlasCoordinate(anchorLat, anchorLon, 12.5, -30.25)
	lon5, lat5 := atlasCoordinate(anchorLat, anchorLon, 17, -42)
	got := out.String()
	for _, want := range []string{
		"ReferenceLongitude=0",
		"ReferenceLatitude=0",
		"ReferenceAltitude=0",
		"MapId=WarThunder.Atlas1.spain",
		"WT_TerrainLayout=AtlasV1",
		"Comment=keep\\,this",
		"1,Name=Plane\\, One,CallSign=Pilot",
		"Color=Red,Note=keep=this\r\n",
		"2,Name=Missile",
		"T=" + lon8 + "|" + lat8 + "|1234.567|100.123|140.987|-15.5|12.5|-30.25,Color=Red,Note=keep=this",
		"T=" + lon5 + "|" + lat5 + "|555.5|17|-42,Name=still-here",
		"#0.000\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("migrated output does not contain %q:\n%s", want, got)
		}
	}
	for _, stale := range []string{"ReferenceLongitude=91.25", "ReferenceLatitude=47.5", "ReferenceAltitude=321.75", "WarThunder.spain"} {
		if strings.Contains(got, stale) {
			t.Errorf("migrated output retained %q", stale)
		}
	}
}

func TestDifferentAtlasAnchorsAndExplicitMapOverride(t *testing.T) {
	ids := []string{"spain", "air_grand_canyon"}
	contents := make([]string, len(ids))
	for i, id := range ids {
		var out bytes.Buffer
		if _, err := transformACMI(strings.NewReader(fixture("spain")), &out, id); err != nil {
			t.Fatal(err)
		}
		contents[i] = out.String()
		lat, lon, ok := terrainmap.AtlasReference(id)
		if !ok {
			t.Fatalf("%s has no Atlas reference", id)
		}
		wantLon, wantLat := atlasCoordinate(lat, lon, 17, -42)
		if !strings.Contains(contents[i], "T="+wantLon+"|"+wantLat+"|") {
			t.Fatalf("map %s did not use its own anchor", id)
		}
	}
	if contents[0] == contents[1] {
		t.Fatal("different map anchors produced identical migrated coordinates")
	}
}

func TestMapInferenceUsesRealEvidenceAndRejectsConflicts(t *testing.T) {
	tests := []struct {
		name, content, filename, want string
	}{
		{"map id", "FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain\n", "korea.acmi", "spain"},
		{"source level", "FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector,SourceLevel=levels/korea.bin\n", "unknown.acmi", "korea"},
		{"filename", "FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector\n", "replay_air_grand_canyon.acmi", "air_grand_canyon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := scanACMI(strings.NewReader(tt.content))
			if err != nil {
				t.Fatal(err)
			}
			got, err := chooseMapID(info, tt.filename)
			if err != nil || got != tt.want {
				t.Fatalf("chooseMapID() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}

	conflicts := []string{
		"FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain,SourceLevel=levels/korea.bin\n",
		"FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain\n#0.0\n0,MapId=WarThunder.korea\n",
	}
	for _, content := range conflicts {
		info, err := scanACMI(strings.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		if id, err := chooseMapID(info, "air_grand_canyon.acmi"); err == nil {
			t.Fatalf("conflicting sources resolved as %q", id)
		}
	}
}

func TestExplicitMapOverridesConflictingSources(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "replay.acmi")
	content := "FileType=text/acmi/tacview\n" +
		"0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain,SourceLevel=levels/korea.bin\n" +
		"#0.0\n1,T=1|2|3|4|5\n"
	if err := os.WriteFile(input, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(input, "", "", false); err == nil {
		t.Fatal("conflicting sources were accepted without -map")
	}
	if _, err := os.Stat(defaultOutput(input)); !os.IsNotExist(err) {
		t.Fatalf("conflict left an output: %v", err)
	}
	if _, mapID, err := readAndTransform(input, "", "air_grand_canyon", false); err != nil || mapID != "air_grand_canyon" {
		t.Fatalf("explicit map override = %q, %v", mapID, err)
	}
	got, err := os.ReadFile(defaultOutput(input))
	if err != nil || !strings.Contains(string(got), "MapId=WarThunder.Atlas1.air_grand_canyon") {
		t.Fatalf("explicit map output: %v\n%s", err, got)
	}
}

func TestRejectsForeignSourceAndUnsafeTForms(t *testing.T) {
	_, err := scanACMI(strings.NewReader("FileType=text/acmi/tacview\n0,MapId=WarThunder.spain\n"))
	if err == nil || !strings.Contains(err.Error(), "DataSource") {
		t.Fatalf("foreign source error = %v", err)
	}
	for _, tValue := range []string{
		"1|2|3|4|5|6",
		"1|2|3|4|5|6|7|8|9",
		"NaN|2|3|4|5",
		"1|2|3|4",
	} {
		content := "FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain\n#0.0\n1,T=" + tValue + "\n"
		if _, err := scanACMI(strings.NewReader(content)); err == nil {
			t.Fatalf("unsafe T=%s was accepted", tValue)
		}
	}
}

func TestAtlasMigrationIsIdempotent(t *testing.T) {
	var first bytes.Buffer
	if _, err := transformACMI(strings.NewReader(fixture("spain")), &first, "spain"); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if _, err := transformACMI(bytes.NewReader(first.Bytes()), &second, "spain"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("reprocessing Atlas coordinates moved the trajectory a second time")
	}
	if strings.Count(second.String(), "WT_TerrainLayout=") != 1 {
		t.Fatal("reprocessing inserted a duplicate terrain layout property")
	}
}

func TestExistingAtlasFileRemainsByteIdempotent(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "spain.acmi")
	if err := os.WriteFile(input, []byte(fixture("spain")), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(input, "", "", false); err != nil {
		t.Fatal(err)
	}
	output := defaultOutput(input)
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if _, mapID, err := readAndTransform(output, "", "", true); err != nil || mapID != "spain" {
		t.Fatalf("in-place Atlas refresh = %q, %v", mapID, err)
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("existing Atlas file moved again: %v", err)
	}
}

func TestOriginalAndExistingOutputAreProtected(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "spain.acmi")
	original := []byte(fixture("spain"))
	if err := os.WriteFile(input, original, 0644); err != nil {
		t.Fatal(err)
	}
	info, mapID, err := readAndTransform(input, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if mapID != "spain" || info.transformed != 2 {
		t.Fatalf("migrated %q with %d T records", mapID, info.transformed)
	}
	if got, err := os.ReadFile(input); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("original input changed: %v", err)
	}

	output := defaultOutput(input)
	if err := os.WriteFile(output, []byte("keep-existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(input, "", "", false); err == nil {
		t.Fatal("existing output was replaced without -overwrite")
	}
	if got, _ := os.ReadFile(output); string(got) != "keep-existing" {
		t.Fatal("existing output changed after refused replacement")
	}
	if _, _, err := readAndTransform(input, "", "", true); err != nil {
		t.Fatalf("-overwrite failed: %v", err)
	}
	if got, _ := os.ReadFile(output); !strings.Contains(string(got), "WT_TerrainLayout=AtlasV1") {
		t.Fatal("explicit overwrite did not write the migrated Atlas file")
	}
}

func TestBadTProducesNoFinalFile(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "spain.acmi")
	bad := "FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain\n#0.0\n1,T=1|2|3|4|5|6\n"
	if err := os.WriteFile(input, []byte(bad), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(input, "", "", false); err == nil {
		t.Fatal("T record without UV was accepted")
	}
	if _, err := os.Stat(defaultOutput(input)); !os.IsNotExist(err) {
		t.Fatalf("failed migration left a final file: %v", err)
	}
}

func TestDirectorySkipsGeneratedAtlasFiles(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "spain.acmi")
	generated := defaultOutput(legacy)
	if err := os.WriteFile(legacy, []byte(fixture("spain")), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generated, []byte("generated-sentinel"), 0644); err != nil {
		t.Fatal(err)
	}
	inputs, err := collectInputs([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].path != legacy {
		t.Fatalf("directory inputs = %+v", inputs)
	}
	if code := run([]string{dir}); code != 0 {
		t.Fatalf("directory run exit code = %d", code)
	}
	if got, _ := os.ReadFile(generated); string(got) != "generated-sentinel" {
		t.Fatal("directory run replaced an already generated Atlas output")
	}
}

func TestDirectoryWithOnlyGeneratedAtlasIsANoOp(t *testing.T) {
	dir := t.TempDir()
	generated := filepath.Join(dir, "spain.atlas.acmi")
	if err := os.WriteFile(generated, []byte(fixture("spain")), 0644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{dir}); code != 0 {
		t.Fatalf("generated-only directory exit code = %d", code)
	}
	got, _ := os.ReadFile(generated)
	if string(got) != fixture("spain") {
		t.Fatal("generated-only directory changed an Atlas file")
	}
}

func TestDirectoryOverwriteReplacesExistingOutputs(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "spain.acmi")
	generated := defaultOutput(legacy)
	if err := os.WriteFile(legacy, []byte(fixture("spain")), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generated, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-overwrite", dir}); code != 0 {
		t.Fatalf("directory overwrite exit code = %d", code)
	}
	got, err := os.ReadFile(generated)
	if err != nil || !strings.Contains(string(got), "WT_TerrainLayout=AtlasV1") {
		t.Fatalf("directory overwrite did not replace stale output: %v\n%s", err, got)
	}
}

func TestOversizedLineAndZipAreRejectedWithoutOutput(t *testing.T) {
	dir := t.TempDir()
	oversized := filepath.Join(dir, "spain.acmi")
	content := "FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain\n#0.0\n0,Comment=" + strings.Repeat("x", maxLineBytes) + "\n"
	if err := os.WriteFile(oversized, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(oversized, "", "", false); err == nil || !strings.Contains(err.Error(), "line exceeds") {
		t.Fatalf("oversized line error = %v", err)
	}
	if _, err := os.Stat(defaultOutput(oversized)); !os.IsNotExist(err) {
		t.Fatalf("oversized input left an output: %v", err)
	}

	zipped := filepath.Join(dir, "compressed.acmi")
	if err := os.WriteFile(zipped, []byte{'P', 'K', 3, 4, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(zipped, "", "", false); err == nil || !strings.Contains(err.Error(), "zip") {
		t.Fatalf("zip error = %v", err)
	}
	if _, err := os.Stat(defaultOutput(zipped)); !os.IsNotExist(err) {
		t.Fatalf("zip input left an output: %v", err)
	}
}

func TestExplicitOverrideRewritesLateGlobalMapAndReferences(t *testing.T) {
	content := "FileType=text/acmi/tacview\n" +
		"0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain,ReferenceLongitude=8\n" +
		"#0.0\n" +
		"0,MapId=WarThunder.korea,ReferenceLongitude=9,ReferenceLatitude=10,ReferenceAltitude=11\n" +
		"1,T=1|2|3|4|5\n"
	var out bytes.Buffer
	if _, err := transformACMI(strings.NewReader(content), &out, "air_grand_canyon"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "WarThunder.spain") || strings.Contains(got, "WarThunder.korea") {
		t.Fatalf("old MapId survived explicit override:\n%s", got)
	}
	if strings.Count(got, "MapId=WarThunder.Atlas1.air_grand_canyon") != 2 {
		t.Fatalf("all global MapId values were not rewritten:\n%s", got)
	}
	if strings.Contains(got, "ReferenceLongitude=8") || strings.Contains(got, "ReferenceLongitude=9") || strings.Contains(got, "ReferenceLatitude=10") || strings.Contains(got, "ReferenceAltitude=11") {
		t.Fatalf("late global reference values survived:\n%s", got)
	}
}

func TestMapIDAndLayoutAreInsertedWithoutChangingTimeline(t *testing.T) {
	content := "FileType=text/acmi/tacview\n0,ReferenceTime=2026-01-01T00:00:00Z,DataSource=War Thunder replay via wrpl-inspector,ReferenceLongitude=1,ReferenceLatitude=2,ReferenceAltitude=3\n#12.500\n1,Name=Alpha\n"
	var out bytes.Buffer
	if _, err := transformACMI(strings.NewReader(content), &out, "spain"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "\n0,MapId=WarThunder.Atlas1.spain,WT_TerrainLayout=AtlasV1\n#12.500\n") {
		t.Fatalf("missing globals were not inserted immediately before the timeline:\n%s", got)
	}
	if !strings.Contains(got, "1,Name=Alpha\n") {
		t.Fatal("timeline data changed")
	}
}

func TestHeaderLimitIncludesInsertedGlobals(t *testing.T) {
	prefix := "FileType=text/acmi/tacview\n0,Comment="
	suffix := ",DataSource=War Thunder replay via wrpl-inspector\n#0.0\n"
	padding := maxLineBytes - len(prefix) - len(suffix) - 1
	content := fmt.Sprintf("%s%s%s", prefix, strings.Repeat("x", padding), suffix)
	var out bytes.Buffer
	if _, err := transformACMI(strings.NewReader(content), &out, "spain"); err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("header limit error = %v", err)
	}
}
