package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

func TestACMISeparatesModelPilotAndCallSign(t *testing.T) {
	tr, slot := newTestTrack(true, 42, "F-16C Block 50", "Alpha", 1)
	line := renderFirstTrack(t, tr, slot, nil)

	want := "1,Name=F-16C Block 50,CallSign=F-16C Block 50,Pilot=Alpha,Type=Air+FixedWing,Coalition=Blue,Color=Blue"
	if line != want {
		t.Fatalf("unexpected track header:\n got: %q\nwant: %q", line, want)
	}
	if strings.Contains(line, "Shape=") {
		t.Fatalf("track header unexpectedly contains Shape: %q", line)
	}
}

func TestACMINameManualOverrideWinsAndStillSeparatesPilot(t *testing.T) {
	tr, slot := newTestTrack(true, 42, "internal-wt-model", "Alpha", 1)
	for _, overrideKey := range []string{"air:42", "42", "2A"} {
		t.Run(overrideKey, func(t *testing.T) {
			line := renderFirstTrack(t, tr, slot, map[string]string{overrideKey: "F-14A Tomcat"})
			want := "1,Name=F-14A Tomcat,CallSign=F-14A Tomcat,Pilot=Alpha,Type=Air+FixedWing,Coalition=Blue,Color=Blue"
			if line != want {
				t.Fatalf("unexpected overridden track header:\n got: %q\nwant: %q", line, want)
			}
		})
	}
}

func TestACMIValueEscapingPreservesUnicodeAndEscapesSyntax(t *testing.T) {
	input := "MïG\\29, Series\nA"
	want := "MïG\\\\29\\, Series\\nA"
	if got := escapeACMIValue(input); got != want {
		t.Fatalf("unexpected escaped value: got %q, want %q", got, want)
	}

	tr, slot := newTestTrack(true, 42, input, "Pilot, One", 1)
	line := renderFirstTrack(t, tr, slot, nil)
	if !strings.Contains(line, "Name="+want+",CallSign="+want) {
		t.Fatalf("escaped model missing from Name/CallSign: %q", line)
	}
	if !strings.Contains(line, `Pilot=Pilot\, One,Type=`) {
		t.Fatalf("pilot was not escaped as a separate property: %q", line)
	}
}

func TestACMINameUsesExplicitUIDFallbackAndNeverPilot(t *testing.T) {
	tests := []struct {
		name  string
		air   bool
		id    uint64
		pilot string
		want  string
	}{
		{
			name: "air without entity",
			air:  true,
			id:   77,
			want: "1,Name=air:77,CallSign=air:77,Type=Air+FixedWing,Coalition=Neutral,Color=White",
		},
		{
			name: "ground without entity",
			id:   8,
			want: "1,Name=ground:8,CallSign=ground:8,Type=Ground+Vehicle,Coalition=Neutral,Color=White",
		},
		{
			name:  "pilot present while model unresolved",
			air:   true,
			id:    77,
			pilot: "Alpha",
			want:  "1,Name=air:77,CallSign=air:77,Pilot=Alpha,Type=Air+FixedWing,Coalition=Blue,Color=Blue",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, slot := newTestTrack(tt.air, tt.id, "", tt.pilot, 1)
			line := renderFirstTrack(t, tr, slot, nil)
			if line != tt.want {
				t.Fatalf("unexpected fallback track header:\n got: %q\nwant: %q", line, tt.want)
			}
		})
	}
}

func newTestTrack(air bool, id uint64, model, pilot string, team byte) (*track, *packetslot.PacketSlotParser) {
	tr := &track{
		key:     trackKey{air: air, id: id},
		samples: []acmiSample{{t: 0}},
	}
	entity := &packetecs2.Entity{}
	var slot *packetslot.PacketSlotParser
	if model != "" {
		entity.Data.AddComponent(&packetecs2.Component{Value: model}, "unit__className")
	}
	if pilot != "" {
		entity.Data.AddComponent(&packetecs2.Component{Value: int32(0)}, "unit__playerId")
		tr.entity = entity
		slot = &packetslot.PacketSlotParser{}
		slot.Players[0] = &packetslot.Player{
			Uid:  packetslot.UID{Name: pilot},
			Team: team,
		}
	} else if model != "" {
		tr.entity = entity
	}
	return tr, slot
}

func renderFirstTrack(t *testing.T, tr *track, slot *packetslot.PacketSlotParser, names map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.acmi")
	if err := writeACMI(path, wrpl.WRPLHeader{}, []*track{tr}, slot, names); err != nil {
		t.Fatalf("writing ACMI: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading ACMI: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "1,") {
			return line
		}
	}
	t.Fatalf("track header not found in output:\n%s", data)
	return ""
}
