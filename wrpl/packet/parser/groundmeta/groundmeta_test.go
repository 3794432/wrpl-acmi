package groundmeta

import (
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	ecs "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

func TestConstructionPositionIsCopiedAndTimestamped(t *testing.T) {
	p := &Parser{}
	e := entityFor(0, "structures/unit_airfield_main_radar_char", "unit_storage__fortification", ecs.UnitStorage{}, 1)
	e.Data.AddComponent(&ecs.Component{Value: ecs.TMatrix{V4: ecs.Point3{X: 100, Y: 200, Z: 300}}}, "transform")
	info, ok := p.ObserveEntityAt(7, e, 1234)
	if !ok || !info.HasConstructionTime || info.ConstructionTime != 1234 || info.Position == nil || info.Position.X != 100 {
		t.Fatalf("missing real construction evidence: %+v", info)
	}
	e.Data.Components[len(e.Data.Components)-1].Comp.Value = ecs.TMatrix{V4: ecs.Point3{X: 999}}
	if info.Position.X != 100 {
		t.Fatal("retained translation aliased mutable entity")
	}
	if err := p.observeDestructions(&packet.Packet{CurrentTime: 2000, PacketPayload: []byte{0x26, 0, 0x1d, 0}}); err != nil {
		t.Fatal(err)
	}
	if p.Ends[7] != 2000 {
		t.Fatalf("ground end=%v", p.Ends)
	}
	before := len(p.Ends)
	if err := p.observeDestructions(&packet.Packet{CurrentTime: 3000, PacketPayload: []byte{0x26, 1, 0x1d, 0}}); err == nil {
		t.Fatal("truncated destruction accepted")
	}
	if len(p.Ends) != before || p.Ends[7] != 2000 {
		t.Fatal("bad destruction modified lifetime")
	}
	untimed, ok := p.ObserveEntity(7, e)
	if !ok || untimed.Position != nil || untimed.HasConstructionTime {
		t.Fatal("API without timestamp invented positional evidence")
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("empty fixture")
	}
	return raw
}

func TestUnitStorageRealFixture(t *testing.T) {
	raw := fixture(t, "tank-storage.hex")
	model, err := UnitStorageUnitName(raw)
	if err != nil {
		t.Fatalf("UnitStorageUnitName: %v", err)
	}
	t.Logf("fixture model=%q", model)
	if model == "" || !strings.HasPrefix(model, "tankModels/") {
		t.Fatalf("unexpected model %q", model)
	}
	layout, err := readStorageLayout(raw)
	if err != nil {
		t.Fatal(err)
	}
	if layout.fields < 50 {
		t.Fatalf("field count=%d, want a full tank storage record", layout.fields)
	}
}

func TestUnitStorageTruncationAndHeaderErrors(t *testing.T) {
	raw := fixture(t, "tank-storage.hex")
	for _, n := range []int{0, 1, 3, 4, 8, len(raw) / 2, len(raw) - 1} {
		if _, err := UnitStorageUnitName(raw[:n]); err == nil {
			t.Fatalf("truncated length %d was accepted", n)
		}
	}
	bad := append([]byte(nil), raw...)
	bad[0], bad[1] = 0xff, 0xff
	if _, err := UnitStorageUnitName(bad); !errors.Is(err, ErrStorageLayout) {
		t.Fatalf("bad offset error=%v, want ErrStorageLayout", err)
	}
	bad = append([]byte(nil), raw...)
	bad[2], bad[3] = 0, 0
	if _, err := UnitStorageUnitName(bad); !errors.Is(err, ErrStorageLayout) {
		t.Fatalf("zero field count error=%v, want ErrStorageLayout", err)
	}
}

func TestUnitStorageReadsFieldSixFromRecord(t *testing.T) {
	raw := fixture(t, "tank-storage.hex")
	original, err := UnitStorageUnitName(raw)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := readStorageLayout(raw)
	if err != nil {
		t.Fatal(err)
	}
	start := 32
	found := false
	for i := 0; i < layout.fields; i++ {
		if layout.indexes[i] == 6 {
			if layout.sizes[i] < 16 {
				t.Fatalf("field 6 size=%d", layout.sizes[i])
			}
			pos := start/8 + 1
			mutated := append([]byte(nil), raw...)
			mutated[pos] = 'X'
			got, err := UnitStorageUnitName(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if got == original || got[0] != 'X' {
				t.Fatalf("mutated field returned %q, original %q", got, original)
			}
			found = true
			break
		}
		start += int(layout.sizes[i])
	}
	if !found {
		t.Fatal("field 6 not found")
	}
}

func entityFor(uid int32, class, storageName string, storage ecs.UnitStorage, team int32) *ecs.Entity {
	e := &ecs.Entity{Template: "tank"}
	add := func(name string, value any) {
		e.Data.AddComponent(&ecs.Component{Value: value}, name)
	}
	add("uid", uid)
	add("team", team)
	if class != "" {
		add("unit__className", class)
	}
	if storageName != "" {
		add(storageName, storage)
	}
	return e
}

func TestObserveEntityKeyedByRealUID(t *testing.T) {
	raw := fixture(t, "tank-storage.hex")
	storageModel, err := UnitStorageUnitName(raw)
	if err != nil {
		t.Fatal(err)
	}
	p := &Parser{Models: map[uint64]Info{}}
	e7 := entityFor(7, "tankModels/class_seven", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1)
	info7, ok := p.ObserveEntity(7007, e7)
	if !ok {
		t.Fatal("UID 7 not observed")
	}
	if info7.RawModel != "tankModels/class_seven" || info7.StorageModel != storageModel {
		t.Fatalf("info7=%+v storageModel=%q", info7, storageModel)
	}
	if info7.Team == nil || *info7.Team != 1 || info7.TeamSource != "ecs:team" {
		t.Fatalf("team=%v source=%q", info7.Team, info7.TeamSource)
	}
	if !strings.Contains(info7.Source, "ecs:unit__className") || !strings.Contains(info7.Source, "#0x6") {
		t.Fatalf("source=%q", info7.Source)
	}
	e8 := entityFor(8, "", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 2)
	if _, ok = p.ObserveEntity(8008, e8); !ok {
		t.Fatal("UID 8 not observed")
	}
	if got := p.Models[8]; got.RawModel != storageModel || got.Source != "ecs:unit_storage__tank#0x6" {
		t.Fatalf("UID 8=%+v", got)
	}
	if got := p.Models[7]; got.RawModel != "tankModels/class_seven" {
		t.Fatalf("UID 7 changed to %+v", got)
	}
	if _, ok = p.Lookup(7); !ok {
		t.Fatal("lookup by real UID failed")
	}
	if _, ok = p.Lookup(7007); ok {
		t.Fatal("entity ID must not become the model key")
	}
}

func TestObserveEntityConflictAndUnknown(t *testing.T) {
	raw := fixture(t, "tank-storage.hex")
	p := &Parser{Models: map[uint64]Info{}}
	p.ObserveEntity(1, entityFor(9, "tankModels/first", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1))
	p.ObserveEntity(2, entityFor(9, "tankModels/second", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1))
	got := p.Models[9]
	if !got.Conflict || got.RawModel != "tankModels/second" || p.Conflicts != 1 {
		t.Fatalf("conflict info=%+v conflicts=%d", got, p.Conflicts)
	}
	p.ObserveEntity(2, entityFor(9, "tankModels/second", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1))
	if got := p.Models[9]; !got.Conflict || p.Conflicts != 1 {
		t.Fatalf("conflict was not sticky: info=%+v conflicts=%d", got, p.Conflicts)
	}
	p.ObserveEntity(3, entityFor(12, "tankModels/stable", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1))
	p.ObserveEntity(3, entityFor(12, "", "unit_storage__tank", ecs.UnitStorage{}, 1))
	if got := p.Models[12]; got.RawModel != "tankModels/stable" || got.Conflict {
		t.Fatalf("unknown observation erased valid model: %+v", got)
	}
	p.ObserveEntity(4, entityFor(12, "tankModels/stable", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1))
	if got := p.Models[12]; got.Conflict || got.RawModel != "tankModels/stable" {
		t.Fatalf("stable model became conflicting: %+v", got)
	}
	storageModel, err := UnitStorageUnitName(raw)
	if err != nil {
		t.Fatal(err)
	}
	classBefore := p.ClassConflicts
	p.ObserveEntity(5, entityFor(13, "tankModels/class_mismatch", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1))
	p.ObserveEntity(6, entityFor(13, storageModel, "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1))
	if got := p.Models[13]; !got.ClassConflict || p.ClassConflicts != classBefore+1 {
		t.Fatalf("class conflict was not sticky: info=%+v count=%d", got, p.ClassConflicts)
	}
	unknownBefore := p.UnknownModels
	p.ObserveEntity(3, entityFor(10, "", "unit_storage__tank", ecs.UnitStorage{}, 1))
	unknown := p.Models[10]
	if unknown.RawModel != "" || unknown.Source != "unknown" || p.UnknownModels != unknownBefore+1 {
		t.Fatalf("unknown info=%+v count=%d", unknown, p.UnknownModels)
	}
	air := &ecs.Entity{Template: "aircraft"}
	air.Data.AddComponent(&ecs.Component{Value: int32(11)}, "uid")
	air.Data.AddComponent(&ecs.Component{Value: "plane"}, "unit__className")
	air.Data.AddComponent(&ecs.Component{Value: ecs.UnitStorage{}}, "unit_storage__aircraft")
	if _, ok := p.ObserveEntity(4, air); ok {
		t.Fatal("aircraft storage was admitted to ground registry")
	}
	p.ObserveEntity(5, entityFor(3000, "tankModels/out_of_range", "unit_storage__tank", ecs.UnitStorage{Raw: raw}, 1))
	if p.InvalidUIDs != 1 {
		t.Fatalf("invalid UID count=%d", p.InvalidUIDs)
	}
}

func TestStorageMissingField(t *testing.T) {
	// A valid layout with one 16-bit field whose id is 1, not the unit name.
	raw := make([]byte, 16)
	raw[0], raw[1] = 8, 0
	raw[2], raw[3] = 1, 0x10 // one field, 16 id bits
	raw[6], raw[7] = 1, 0    // id=1, before the size table at byte 8
	raw[8] = 3 << 5          // ReadSize header 3 means 16 bits
	if _, err := UnitStorageUnitName(raw); !errors.Is(err, ErrUnitNameMissing) {
		t.Fatalf("error=%v, want ErrUnitNameMissing", err)
	}
}
