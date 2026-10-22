package projectiles

import (
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"testing"
)

func TestCountermeasureOwnerAmmoSnapshot(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), true)
	const owner = uint64(123)
	p.ownerWeapons[owner] = []groundmeta.StorageWeapon{{Launcher: "countermeasure_large_split_launcher_jet", Count: 10}}
	r := danet.NewBitReader(spawnRocketValue(owner, 0, 14<<16, 1, 2, 3))
	s := p.readProjectileConstruction(456, r, 100, "rocket", nil, projectileKindRocket)
	if s == nil || len(s.OwnerWeapons) != 1 || s.OwnerWeapons[0].Count != 10 {
		t.Fatalf("missing snapshot: %+v", s)
	}
	p.ownerWeapons[owner][0].Count = 0
	if s.OwnerWeapons[0].Count != 10 {
		t.Fatal("owner mutation changed prior release")
	}
	if _, err := p.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if err := p.parseDestruction(spawnDestructionPacket(owner), 200); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.ownerWeapons[owner]; ok {
		t.Fatal("destroyed owner retained ammunition")
	}
	if s.OwnerWeapons[0].Count != 10 {
		t.Fatal("destruction erased prior release evidence")
	}
	// A different generation with the same entity index cannot inherit ammo.
	r = danet.NewBitReader(spawnRocketValue(owner+1, 0, 14<<16, 1, 2, 3))
	other := p.readProjectileConstruction(457, r, 300, "rocket", nil, projectileKindRocket)
	if len(other.OwnerWeapons) != 0 {
		t.Fatal("unrelated owner inherited ammunition")
	}
}
