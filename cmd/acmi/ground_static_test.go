package main

import (
	"math"
	"strings"
	"testing"

	ecs "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

func TestStaticGroundRequiresExplicitStructurePosition(t *testing.T) {
	valid := groundmeta.Info{UID: 0, EID: 10, RawModel: "structures/unit_airfield_main_radar_char", Storage: "unit_storage__fortification", HasConstructionTime: true, ConstructionTime: 1500, Position: &ecs.Point3{X: 100, Y: 200, Z: 300}}
	makeParser := func(info groundmeta.Info) *groundmeta.Parser {
		return &groundmeta.Parser{Models: map[uint64]groundmeta.Info{uint64(info.UID): info}, All: []groundmeta.Info{info}, Ends: map[uint64]uint32{10: 4000}}
	}
	tr := appendStaticGroundTracks(nil, makeParser(valid))
	if len(tr) != 1 || tr[0].samples[0].t != 1500 || !tr[0].hasEnd || tr[0].end != 4000 {
		t.Fatalf("wrong static lifetime: %+v", tr)
	}
	data := renderTestACMI(t, tr, nil, nil)
	for _, want := range []string{"Type=Ground+Static+Building", "WT_GroundPositionSource=ECSConstructionTransform", "WT_GroundStatic=1", "-1"} {
		if !strings.Contains(data, want) {
			t.Fatalf("missing %s: %s", want, data)
		}
	}
	cases := []struct {
		name   string
		change func(*groundmeta.Info)
	}{
		{"no position", func(i *groundmeta.Info) { i.Position = nil }},
		{"no timestamp", func(i *groundmeta.Info) { i.HasConstructionTime = false }},
		{"no static evidence", func(i *groundmeta.Info) { i.RawModel = "tankModels/fr_crotale_ng"; i.Storage = "unit_storage__tank" }},
		{"identity conflict", func(i *groundmeta.Info) { i.Conflict = true }},
		{"mission dummy", func(i *groundmeta.Info) { i.RawModel = "structures/dummy_immortal" }},
		{"nan", func(i *groundmeta.Info) { i.Position = &ecs.Point3{X: float32(math.NaN())} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := valid
			tc.change(&info)
			if got := appendStaticGroundTracks(nil, makeParser(info)); len(got) != 0 {
				t.Fatalf("unsupported static object: %+v", got)
			}
		})
	}
	existing := &track{key: trackKey{groundEntity: true, id: groundUnitUIDNamespace}, samples: []acmiSample{{t: 1200}}}
	if got := appendStaticGroundTracks([]*track{existing}, makeParser(valid)); len(got) != 1 || got[0] != existing {
		t.Fatal("duplicated synced UID")
	}
	p := makeParser(valid)
	moved := valid
	moved.Position = &ecs.Point3{X: 101, Y: 200, Z: 300}
	p.All = append(p.All, moved)
	if got := appendStaticGroundTracks(nil, p); len(got) != 0 {
		t.Fatal("moving construction exported as static")
	}
	p = makeParser(valid)
	recycled := valid
	recycled.EID = 11
	recycled.Position = nil
	p.All = append([]groundmeta.Info{recycled}, p.All...)
	if got := appendStaticGroundTracks(nil, p); len(got) != 0 {
		t.Fatal("recycled UID exported")
	}
}
