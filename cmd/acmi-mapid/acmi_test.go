package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOldACMIPreservesBytesAndFixesTimelineOverride(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "unknown.acmi")
	old := "FileType=text/acmi/tacview\r\nFileVersion=2.1\r\n" +
		"0,ReferenceTime=2026-01-01T00:00:00Z,Comment=Alpha\\,Beta\r\n" +
		"1,Name=Plane\\, One,CallSign=Alpha\r\n" +
		"#1.000\r\n0,MapId=WarThunder.korea,Note=keep\\,this\r\n" +
		"1,T=1|2|3\r\n"
	if err := os.WriteFile(input, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	info, mapID, err := readAndTransform(input, "", "air_grand_canyon", false)
	if err != nil {
		t.Fatal(err)
	}
	if mapID != "air_grand_canyon" || !info.inserted || info.time != 1 {
		t.Fatalf("unexpected transform info: %+v, map=%q", info, mapID)
	}
	gotBytes, err := os.ReadFile(defaultOutput(input))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(old, "#1.000\r\n", "0,MapId=WarThunder.air_grand_canyon\r\n#1.000\r\n", 1)
	want = strings.Replace(want, "0,MapId=WarThunder.korea", "0,MapId=WarThunder.air_grand_canyon", 1)
	if string(gotBytes) != want {
		t.Fatalf("byte preservation failed\n got: %q\nwant: %q", gotBytes, want)
	}
	if _, _, err := readAndTransform(defaultOutput(input), "", "air_grand_canyon", true); err != nil {
		t.Fatal(err)
	}
	idempotent, err := os.ReadFile(defaultOutput(input))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, idempotent) {
		t.Fatal("rewriting an already normalized file changed its bytes")
	}
}

func TestMapInferenceUsesOnlyReliableEvidence(t *testing.T) {
	tests := []struct {
		name, content, filename, want string
	}{
		{"map id", "FileType=text/acmi/tacview\n0,MapId=WarThunder.spain\n", "unknown.acmi", "spain"},
		{"source level", "FileType=text/acmi/tacview\n0,SourceLevel=levels/air_ladoga.bin\n", "unknown.acmi", "air_ladoga"},
		{"filename token", "FileType=text/acmi/tacview\n", "2026_09_17_air_grand_canyon_named.acmi", "air_grand_canyon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := scanACMI(strings.NewReader(tt.content))
			if err != nil {
				t.Fatal(err)
			}
			got, err := chooseMapID(info, tt.filename)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	info, err := scanACMI(strings.NewReader("FileType=text/acmi/tacview\n0,Latitude=34,Longitude=69\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := chooseMapID(info, "coordinate69_34.acmi"); err == nil || got != "" {
		t.Fatalf("coordinates were guessed as %q", got)
	}
}

func TestUnknownAndExistingOutputCreateNothing(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "unknown.acmi")
	if err := os.WriteFile(input, []byte("FileType=text/acmi/tacview\n#0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(input, "", "", false); err == nil {
		t.Fatal("unknown map succeeded")
	}
	if _, err := os.Stat(defaultOutput(input)); !os.IsNotExist(err) {
		t.Fatalf("unknown map left output: %v", err)
	}
	valid := filepath.Join(dir, "air_grand_canyon.acmi")
	if err := os.WriteFile(valid, []byte("FileType=text/acmi/tacview\n#0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultOutput(valid), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(valid, "", "", false); err == nil {
		t.Fatal("existing output was accepted without -overwrite")
	}
	if b, _ := os.ReadFile(defaultOutput(valid)); string(b) != "keep" {
		t.Fatal("existing output was modified")
	}
	if _, _, err := readAndTransform(valid, "", "", true); err != nil {
		t.Fatalf("explicit overwrite failed: %v", err)
	}
	if b, _ := os.ReadFile(defaultOutput(valid)); !strings.Contains(string(b), "MapId=WarThunder.air_grand_canyon") {
		t.Fatalf("explicit overwrite did not write output: %s", b)
	}
}

func TestZipACMIIsRejectedWithoutOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "air_grand_canyon.zip.acmi")
	if err := os.WriteFile(input, []byte{'P', 'K', 3, 4, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(input, "", "", false); err == nil || !strings.Contains(err.Error(), "zip") {
		t.Fatalf("zip error = %v", err)
	}
	if _, err := os.Stat(defaultOutput(input)); !os.IsNotExist(err) {
		t.Fatalf("zip input left output: %v", err)
	}
}

func TestOversizedUnterminatedHeaderAndTimelineLinesAreRejectedWithoutOutput(t *testing.T) {
	cases := []struct {
		name, content string
	}{
		{"header", "FileType=text/acmi/tacview\n0,Comment=" + strings.Repeat("x", maxHeaderBytes)},
		{"timeline", "FileType=text/acmi/tacview\n#0.0\n0,Comment=" + strings.Repeat("x", maxHeaderBytes)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := scanACMI(strings.NewReader(tt.content)); err == nil || !strings.Contains(err.Error(), "line exceeds") {
				t.Fatalf("scan error = %v", err)
			}
			var transformed bytes.Buffer
			if _, err := transformACMI(strings.NewReader(tt.content), &transformed, "air_grand_canyon"); err == nil || !strings.Contains(err.Error(), "line exceeds") {
				t.Fatalf("transform error = %v", err)
			}
			dir := t.TempDir()
			input := filepath.Join(dir, "air_grand_canyon.acmi")
			if err := os.WriteFile(input, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readAndTransform(input, "", "", false); err == nil {
				t.Fatal("oversized line was accepted")
			}
			if _, err := os.Stat(defaultOutput(input)); !os.IsNotExist(err) {
				t.Fatalf("oversized input left an output: %v", err)
			}
		})
	}
}
