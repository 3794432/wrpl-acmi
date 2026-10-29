package main

import (
	"fmt"
	"strings"
	"testing"

	ecs "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func TestAllCataloguedSAMLaunchersExportRecordedMissiles(t *testing.T) {
	if len(samLaunchers.Units) < 30 {
		t.Fatal("SAM catalogue unexpectedly incomplete")
	}
	for model := range samLaunchers.Units {
		t.Run(model, func(t *testing.T) {
			for _, trigger := range []uint32{19, 20, 21} {
				owner := groundIdentityTestInfo(192, 0x40034f, 100, model, 1)
				source := &projectiles.Projectile{ID: 0x4007e3, OwnerEID: owner.EID, ParentUID: 192, HasParent: true,
					Template: "rocket", Name: "Missile", WeaponRef: trigger << 16, SpawnSample: projectiles.Sample{Time: 200}, EndTime: 500, HasEnd: true}
				meta := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{source.ID: {source}}}
				positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
					adapterTestWireID(source.ID): {{Time: 250, X: 100, Y: 200, Z: 300}, {Time: 600, X: 999}},
				}}
				ground := &groundmeta.Parser{All: []groundmeta.Info{owner}}
				decoded := decodedMissileTracks(meta, positions, ground)
				if len(decoded) != 1 || decoded[0].SurfaceLaunch == nil || !equalTimes(adapterTestTimes(decoded[0].Samples), []uint32{200, 250}) {
					t.Fatalf("trigger %d: missing SAM or invalid lifetime: %+v", trigger, decoded)
				}
				// Metadata alone must not generate a stationary or estimated missile.
				if got := decodedMissileTracks(meta, projectiles.NewPositionParser(), ground); len(got) != 0 {
					t.Fatalf("trigger %d: construction-only SAM was exported", trigger)
				}
			}
		})
	}
}

func TestSAMRejectsMLRSAndUnprovenOwners(t *testing.T) {
	for _, name := range []string{"mlrs", "generation", "before birth", "after death", "uid mismatch", "conflicting model", "smoke", "bomb"} {
		t.Run(name, func(t *testing.T) {
			owner := groundIdentityTestInfo(192, 0x40034f, 100, "ussr_ai_sam_launcher_5p85se", 1)
			source := &projectiles.Projectile{ID: 0x4007e3, OwnerEID: owner.EID, ParentUID: 192, HasParent: true,
				Template: "rocket", WeaponRef: 19 << 16, SpawnSample: projectiles.Sample{Time: 200}}
			ground := &groundmeta.Parser{Ends: map[uint64]uint32{}}
			switch name {
			case "mlrs":
				owner.RawModel = "tankModels/us_ai_mlrs_m270"
				owner.ClassModel, owner.StorageModel = owner.RawModel, owner.RawModel
			case "generation":
				source.OwnerEID += 1 << 22
			case "before birth":
				owner.ConstructionTime = 300
			case "after death":
				ground.Ends[owner.EID] = 150
			case "uid mismatch":
				source.ParentUID++
			case "conflicting model":
				owner.StorageModel = "ussr_t_90m"
			case "smoke":
				source.WeaponRef = 14 << 16
			case "bomb":
				source.Kind = "bomb"
			}
			ground.All = []groundmeta.Info{owner}
			meta := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{source.ID: {source}}}
			if len(surfaceMissileLaunches(meta, ground)) != 0 {
				t.Fatal("classified unsupported projectile as SAM")
			}
		})
	}
}

func TestSAMSharedEIDPreservesExistingMissileIdentity(t *testing.T) {
	for _, birth := range []uint32{0, 100} {
		owner := groundIdentityTestInfo(192, 0x40034f, 0, "ussr_ai_sam_launcher_5p85se", 1)
		air := &projectiles.Projectile{ID: 0x4007e3, WeaponRef: 8 << 16, SpawnSample: projectiles.Sample{Time: 300}, EndTime: 500, HasEnd: true}
		sam := &projectiles.Projectile{ID: air.ID, OwnerEID: owner.EID, WeaponRef: 19 << 16, SpawnSample: projectiles.Sample{Time: birth}, EndTime: 200, HasEnd: true}
		meta := &projectiles.MetadataParser{History: map[uint64][]*projectiles.Projectile{air.ID: {sam, air}}}
		positions := &projectiles.PositionParser{Samples: map[uint64][]projectiles.Sample{
			adapterTestWireID(air.ID): {{Time: 150}, {Time: 400}},
		}}
		decoded := decodedMissileTracks(meta, positions, &groundmeta.Parser{All: []groundmeta.Info{owner}})
		if len(decoded) != 2 || decoded[0].ID == decoded[1].ID {
			t.Fatalf("shared EID lost a lifetime at t=%d: %+v", birth, decoded)
		}
		for _, tr := range decoded {
			if tr.SurfaceLaunch == nil && tr.ID != air.ID {
				t.Fatalf("SAM changed existing missile identity: %#x", tr.ID)
			}
		}
	}
}

func TestSAMOwnershipUsesExactGroundIdentityAndSurvivesLauncherDeath(t *testing.T) {
	owner := groundIdentityTestInfo(42, 0x40034f, 100, "ussr_ai_sam_launcher_5p85se", 1)
	owner.Entity = &ecs.Entity{Template: "tank"}
	launcher := groundIdentityTestTrack(42, []uint32{150, 300})
	launcher.entity, launcher.groundTeam, launcher.groundTeamSource = owner.Entity, 1, "ecs:team"
	launcher.hasEnd, launcher.end = true, 350
	other := groundIdentityTestTrack(42, []uint32{400, 700})
	other.key.sortie = 1
	other.entity = &ecs.Entity{Template: "tank"}
	other.groundTeam, other.groundTeamSource = 2, "ecs:team"
	air, _ := newTestTrack(true, 42, "F-16C", "Pilot", 2)
	air.samples = []acmiSample{{t: 100}, {t: 700}}
	source := &projectiles.Projectile{OwnerEID: owner.EID, WeaponRef: 19 << 16, SpawnSample: projectiles.Sample{Time: 200}}
	missile := &track{key: trackKey{missile: true, id: 99}, projectileSource: source,
		samples: []acmiSample{{t: 200}, {t: 500}}, end: 500, hasEnd: true,
		surfaceLaunch: &surfaceMissileLaunch{owner: owner, profile: samLaunchers.Units[owner.RawModel]}}
	tracks := []*track{air, launcher, other, missile}
	applySurfaceMissileOwnership(tracks)
	if missile.parent != launcher || trackCoalition(missile, nil) != "Blue" || missile.end != 500 || len(missile.samples) != 2 {
		t.Fatal("SAM inherited an aircraft/recycled UID or was cut off at launcher death")
	}
	applyMissileModelNames(tracks, nil)
	if !strings.HasPrefix(missile.name, "SAM (") || missile.missileModel.Model != "" {
		t.Fatalf("fallback invented a specific ammunition model: %q %+v", missile.name, missile.missileModel)
	}
	props := strings.Join(acmiTrackProperties(missile, nil, nil, acmiObjectIDs(tracks)), ",")
	for _, field := range []string{"Type=Weapon+Missile", "Parent=2", "Coalition=Blue", "WT_WeaponRole=SAM", "WT_LauncherEntityID=40034f"} {
		if !strings.Contains(props, field) {
			t.Fatalf("missing %s: %s", field, props)
		}
	}
	// Turning ground rendering off must preserve the actual launcher's team.
	missile.parent = nil
	applySurfaceMissileOwnership([]*track{air, missile})
	if missile.parent != nil || trackCoalition(missile, nil) != "Blue" {
		t.Fatal("missing ground display lost coalition or attached to colliding aircraft UID")
	}
	// A label names the launcher, not an arbitrary static ammunition candidate.
	if missile.name != fmt.Sprintf("SAM (%s)", groundDisplayModelName(owner.RawModel)) {
		t.Fatal(missile.name)
	}
}
