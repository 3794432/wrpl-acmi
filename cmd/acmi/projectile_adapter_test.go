package main

import (
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func TestProjectileEntityIDKnownWireValuesAndInvalidCodes(t *testing.T) {
	tests := []struct {
		name string
		wire uint64
		want uint64
	}{
		{name: "generation one", wire: 0x1f8c01, want: 0x4007e3},
		{name: "generation one high index", wire: 0x214401, want: 0x400851},
		{name: "generation zero", wire: 0x20bc02, want: 0x82f},
		{name: "generation zero higher index", wire: 0x270002, want: 0x9c0},
		{name: "generation two", wire: 0x259c04, want: 0x800967},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := projectileEntityID(tt.wire)
			if !ok || got != tt.want {
				t.Fatalf("projectileEntityID(%#x) = %#x/%v, want %#x/true", tt.wire, got, ok, tt.want)
			}
			again, ok := projectileEntityID(tt.wire)
			if !ok || again != got {
				t.Fatalf("projectileEntityID is not deterministic: first=%#x second=%#x", got, again)
			}
		})
	}

	for _, wire := range []uint64{
		0x400,              // low ten-bit code zero
		0x102,              // low code 258, outside the supported range
		uint64(1) << 32,    // wider than WeaponSync's 32-bit handle
		uint64(0xffffffff), // reserved all-ones low code
	} {
		if got, ok := projectileEntityID(wire); ok {
			t.Fatalf("invalid wire ID %#x decoded as %#x", wire, got)
		}
	}
}

func TestGuidedProjectileUsesWeaponTriggerHighByte(t *testing.T) {
	tests := []struct {
		name      string
		weaponRef uint32
		want      bool
	}{
		{name: "AAM trigger eight", weaponRef: 8<<16 | 0x1234, want: true},
		{name: "AGM trigger seven", weaponRef: 7<<16 | 0x1234, want: true},
		{name: "countermeasure trigger fourteen", weaponRef: 14 << 16, want: false},
		{name: "unguided rocket trigger three", weaponRef: 3 << 16, want: false},
		{name: "missing trigger", weaponRef: 0, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &projectiles.Projectile{Template: "rocket+arh_shell", WeaponRef: tt.weaponRef}
			if got := guidedProjectile(source); got != tt.want {
				t.Fatalf("guidedProjectile(weaponRef=%#x) = %v, want %v", tt.weaponRef, got, tt.want)
			}
		})
	}
	if guidedProjectile(nil) {
		t.Fatal("nil projectile was classified as guided")
	}
}

func TestDecodedMissileTracksRequiresRealPositionAndFiltersLifetime(t *testing.T) {
	const entityID = uint64(0x4007e3)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		entityID: {{
			ID: entityID, Name: "Missile", ParentUID: 77, HasParent: true,
			WeaponRef: 8 << 16, SpawnSample: projectiles.Sample{Time: 100, X: 1, Y: 2, Z: 3},
			EndTime: 200, HasEnd: true,
		}},
		uint64(0x4007e4): {{
			ID: 0x4007e4, WeaponRef: 8 << 16,
			SpawnSample: projectiles.Sample{Time: 500, X: 9, Y: 9, Z: 9},
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(entityID): {
			{Time: 99, X: -1, Y: -1, Z: -1},
			{Time: 150, X: 20, Y: 30, Z: 40},
			{Time: 201, X: -1, Y: -1, Z: -1},
		},
	}}

	tracks := decodedMissileTracks(metadata, positions)
	if len(tracks) != 1 {
		t.Fatalf("decoded tracks=%d, want one; spawn-only projectile must be excluded", len(tracks))
	}
	if tracks[0].ID != entityID || !tracks[0].HasParent || tracks[0].ParentUID != 77 {
		t.Fatalf("track identity/parent=%+v", tracks[0])
	}
	if got := adapterTestTimes(tracks[0].Samples); !adapterTestEqualTimes(got, []uint32{100, 150}) {
		t.Fatalf("sample times=%v, want spawn and in-lifetime recorded position only", got)
	}
	if tracks[0].Samples[1].x != 20 || tracks[0].Samples[1].y != 30 || tracks[0].Samples[1].z != 40 {
		t.Fatalf("recorded coordinate was not preserved: %+v", tracks[0].Samples[1])
	}
}

func TestDecodedMissileTracksSameTimeKeepsLastSyncUpdate(t *testing.T) {
	const entityID = uint64(0x400851)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		entityID: {{
			ID: entityID, WeaponRef: 8 << 16,
			SpawnSample: projectiles.Sample{Time: 200, X: 1, Y: 1, Z: 1},
		}},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(entityID): {
			{Time: 200, X: 2, Y: 3, Z: 4},
			{Time: 300, X: 5, Y: 6, Z: 7},
		},
	}}

	tracks := decodedMissileTracks(metadata, positions)
	if len(tracks) != 1 {
		t.Fatalf("decoded tracks=%d, want 1", len(tracks))
	}
	if got := adapterTestTimes(tracks[0].Samples); !adapterTestEqualTimes(got, []uint32{200, 300}) {
		t.Fatalf("sample times=%v, want unique sorted times [200 300]", got)
	}
	if got := tracks[0].Samples[0].x; got != 2 {
		t.Fatalf("same-time coordinate x=%v, want last recorded update 2", got)
	}
}

func TestDecodedMissileTracksUsesOnlyGuidedHistoryInstance(t *testing.T) {
	const entityID = uint64(0x4007e7)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		entityID: {
			{
				ID: entityID, WeaponRef: 14 << 16,
				SpawnSample: projectiles.Sample{Time: 100, X: 1, Y: 1, Z: 1},
				EndTime:     200, HasEnd: true,
			},
			{
				ID: entityID, WeaponRef: 8 << 16,
				SpawnSample: projectiles.Sample{Time: 300, X: 2, Y: 2, Z: 2},
				EndTime:     400, HasEnd: true,
			},
		},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(entityID): {
			{Time: 150, X: 10, Y: 10, Z: 10},
			{Time: 350, X: 20, Y: 20, Z: 20},
		},
	}}

	tracks := decodedMissileTracks(metadata, positions)
	if len(tracks) != 1 {
		t.Fatalf("decoded tracks=%d, want only the guided history instance", len(tracks))
	}
	if tracks[0].ID != entityID {
		t.Fatalf("single guided instance ID=%#x, want source EID %#x", tracks[0].ID, entityID)
	}
	if got := adapterTestTimes(tracks[0].Samples); !adapterTestEqualTimes(got, []uint32{300, 350}) {
		t.Fatalf("sample times=%v, want only guided lifetime [300 350]", got)
	}
}

func TestDecodedMissileTracksRepeatedGuidedLifetimesGetUniqueIDs(t *testing.T) {
	const entityID = uint64(0x4007e8)
	metadata := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{
		entityID: {
			{
				ID: entityID, WeaponRef: 8 << 16,
				SpawnSample: projectiles.Sample{Time: 100, X: 1, Y: 1, Z: 1},
				EndTime:     200, HasEnd: true,
			},
			{
				ID: entityID, WeaponRef: 7 << 16,
				SpawnSample: projectiles.Sample{Time: 300, X: 2, Y: 2, Z: 2},
				EndTime:     400, HasEnd: true,
			},
		},
	}}
	positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
		adapterTestWireID(entityID): {
			{Time: 150, X: 10, Y: 10, Z: 10},
			{Time: 350, X: 20, Y: 20, Z: 20},
		},
	}}

	tracks := decodedMissileTracks(metadata, positions)
	if len(tracks) != 2 {
		t.Fatalf("decoded tracks=%d, want two guided lifetimes", len(tracks))
	}
	wantIDs := map[uint64]bool{
		uint64(100)<<32 | entityID: true,
		uint64(300)<<32 | entityID: true,
	}
	for _, tr := range tracks {
		if !wantIDs[tr.ID] {
			t.Fatalf("unexpected repeated-lifetime DTO ID %#x", tr.ID)
		}
		delete(wantIDs, tr.ID)
	}
	if len(wantIDs) != 0 {
		t.Fatalf("missing distinct repeated-lifetime IDs: %v", wantIDs)
	}
}

func adapterTestWireID(entityID uint64) uint64 {
	index := entityID & 0x3fffff
	generation := (entityID >> 22) & 0xff
	code := generation + 2
	if generation == 1 {
		code = 1
	}
	return index<<10 | code
}

func adapterTestTimes(samples []acmiSample) []uint32 {
	result := make([]uint32, len(samples))
	for i, sample := range samples {
		result[i] = sample.t
	}
	return result
}

func adapterTestEqualTimes(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
