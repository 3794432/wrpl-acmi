package main

import (
	"testing"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

func TestDisplayModelNameKnownClasses(t *testing.T) {
	tests := map[string]string{
		"yak-38m":    "Yak-38M",
		"yak_38m":    "Yak-38M",
		"f-86f-2":    "F-86F-2",
		"f_86f_2":    "F-86F-2",
		"f-5e":       "F-5E",
		"f_5e":       "F-5E",
		"f-104a":     "F-104A",
		"f-104c":     "F-104C",
		"la-15":      "La-15",
		"su-7b":      "Su-7B",
		"mig-21_f13": "MiG-21F-13",
		"su_30mkk":   "␗Su-30MKK",
	}
	for className, want := range tests {
		if got := displayModelName(className); got != want {
			t.Errorf("displayModelName(%q) = %q, want %q", className, got, want)
		}
	}
}

func TestDisplayModelNamePreservesUnknown(t *testing.T) {
	const className = "wt_unknown_aircraft_for_test"
	if got := displayModelName(className); got != className {
		t.Fatalf("displayModelName(%q) = %q, want unchanged", className, got)
	}
}

func TestManualNameOverridesEmbeddedModelName(t *testing.T) {
	tr := &track{
		key:     trackKey{air: true, id: 123},
		samples: []acmiSample{{t: 0}},
		entity:  &packetecs2.Entity{},
	}
	tr.entity.Data.AddComponent(&packetecs2.Component{Value: "f-5e"}, "unit__className")

	const manualName = "Custom Aircraft Label"
	names := map[string]string{"air:123": manualName}
	if got := trackName(tr, nil, names); got != manualName {
		t.Fatalf("trackName with manual override = %q, want %q", got, manualName)
	}
}
