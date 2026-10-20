package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedReplacementPreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "existing.acmi")
	want := []byte("existing Atlas output must survive a failed replacement")
	if err := os.WriteFile(output, want, 0644); err != nil {
		t.Fatal(err)
	}
	if err := installOutput(filepath.Join(dir, "missing.tmp"), output, true); err == nil {
		t.Fatal("replacement with a missing temporary file unexpectedly succeeded")
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != string(want) {
		t.Fatalf("existing output changed on failure: %q, %v", got, err)
	}
}

func TestFailedTransformPreservesExistingOutputWithOverwrite(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "spain.acmi")
	output := defaultOutput(input)
	bad := "FileType=text/acmi/tacview\n0,DataSource=War Thunder replay via wrpl-inspector,MapId=WarThunder.spain\n#0.0\n1,T=NaN|2|3|4|5\n"
	if err := os.WriteFile(input, []byte(bad), 0644); err != nil {
		t.Fatal(err)
	}
	want := []byte("previous-good-atlas")
	if err := os.WriteFile(output, want, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAndTransform(input, "", "", true); err == nil {
		t.Fatal("invalid T unexpectedly succeeded")
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != string(want) {
		t.Fatalf("failed transform replaced existing output: %q, %v", got, err)
	}
}
