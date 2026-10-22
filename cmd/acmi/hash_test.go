package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestReadHashesDefaultsToEmbeddedData(t *testing.T) {
	if got := *hashPath; got != "embedded" {
		t.Fatalf("-ecshashes default = %q, want embedded", got)
	}
	chms, err := readHashes(*hashPath)
	if err != nil {
		t.Fatalf("read embedded hashes: %v", err)
	}
	if chms == nil || len(chms.ComponentNames) == 0 || len(chms.DataComponents) == 0 {
		t.Fatalf("embedded hashes parsed without component data: %+v", chms)
	}
	if len(chms.DataComponentParsers) == 0 {
		t.Fatal("embedded hashes initialized no data component parsers")
	}
}

func TestReadHashesEmptyDisables(t *testing.T) {
	chms, err := readHashes("")
	if err != nil {
		t.Fatalf("empty -ecshashes should disable without error: %v", err)
	}
	if chms != nil {
		t.Fatalf("empty -ecshashes returned hashes: %+v", chms)
	}
}

func TestReadHashesLoadsExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ecshashes.json")
	contents := `{
  "components": {"0x10000001": "ExplicitComponent"},
  "dataComponents": {
    "0x20000002": {"name": "unit__className", "comp": "0x10000001", "has_loader": false}
  }
}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write explicit hash map: %v", err)
	}
	chms, err := readHashes(path)
	if err != nil {
		t.Fatalf("read explicit hashes: %v", err)
	}
	if name, ok := chms.GetCompName(0x10000001); !ok || name != "ExplicitComponent" {
		t.Fatalf("explicit component name = %q, %t", name, ok)
	}
	if name, ok := chms.GetDataCompName(0x20000002); !ok || name != "unit__className" {
		t.Fatalf("explicit data component name = %q, %t", name, ok)
	}
}

func TestReadHashesExplicitPathErrorsAreClear(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	_, err := readHashes(missing)
	if err == nil {
		t.Fatal("missing explicit hash map did not return an error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing path error does not name the path: %v", err)
	}

	malformed := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed hash map: %v", err)
	}
	_, err = readHashes(malformed)
	if err == nil {
		t.Fatal("malformed explicit hash map did not return an error")
	}
	if !strings.Contains(err.Error(), strconv.Quote(malformed)) || !strings.Contains(err.Error(), "parsing") {
		t.Fatalf("malformed path error is not clear: %v", err)
	}
}
