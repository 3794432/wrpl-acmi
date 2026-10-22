package data

import (
	"bytes"
	"testing"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

func TestEmbeddedECSHashesJSONIncludesComponentDefinitions(t *testing.T) {
	raw := ECSHashesJSON()
	if len(raw) == 0 {
		t.Fatal("embedded ECS hash map is empty")
	}
	chms, err := packetecs2.ReadComponentHashMaps(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse embedded ECS hash map: %v", err)
	}
	if len(chms.ComponentNames) < 100 {
		t.Fatalf("embedded component names = %d, want at least 100", len(chms.ComponentNames))
	}
	if len(chms.DataComponents) < 1000 {
		t.Fatalf("embedded data components = %d, want at least 1000", len(chms.DataComponents))
	}
	if len(chms.DataComponentParsers) == 0 {
		t.Fatal("embedded ECS hash map initialized no data component parsers")
	}

	names := make(map[string]bool, len(chms.DataComponents))
	for _, component := range chms.DataComponents {
		names[component.Name] = true
	}
	for _, want := range []string{"unit__className", "unit__playerId"} {
		if !names[want] {
			t.Errorf("embedded ECS hash map is missing %q", want)
		}
	}
}
