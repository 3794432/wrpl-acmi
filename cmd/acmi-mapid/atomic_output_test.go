package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedReplacementPreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "existing.acmi")
	want := []byte("existing replay must survive a failed replacement")
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
