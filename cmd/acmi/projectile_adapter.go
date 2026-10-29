package main

import (
	"sort"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

// Join only identities supplied by the replay. A constructor's position is
// retained together with subsequent recorded WeaponSync positions.
func decodedMissileTracks(metadata *projectiles.MetadataParser, positions *projectiles.PositionParser, ground ...*groundmeta.Parser) []missileTrack {
	if metadata == nil || positions == nil {
		return nil
	}
	var result []missileTrack
	var owners *groundmeta.Parser
	if len(ground) > 0 {
		owners = ground[0]
	}
	surface := surfaceMissileLaunches(metadata, owners)
	byEntity := make(map[uint64][]projectiles.Sample)
	for wireID, samples := range positions.Samples {
		if id, ok := projectileEntityID(wireID); ok {
			byEntity[id] = append(byEntity[id], samples...)
		}
	}
	for id, instances := range metadata.History {
		guidedInstances := 0
		for _, source := range instances {
			if guidedProjectile(source) {
				guidedInstances++
			}
		}
		reservedIDs := map[uint64]bool{}
		for _, source := range instances {
			if guidedProjectile(source) {
				key := id
				if guidedInstances > 1 {
					key |= uint64(source.SpawnSample.Time) << 32
				}
				reservedIDs[key] = true
			}
		}
		for _, source := range instances {
			if !guidedProjectile(source) && surface[source] == nil {
				continue
			}
			tr := missileTrack{
				ID: id, Name: source.Name, ParentUID: source.ParentUID,
				HasParent: source.HasParent, EndTime: source.EndTime, HasEnd: source.HasEnd,
				Source:        source,
				SurfaceLaunch: surface[source],
			}
			if guidedInstances > 1 || surface[source] != nil {
				// Reconstruction may reuse even the same full EID. Keep lifetimes
				// separate without dropping the generation bits of the source EID.
				// New SAM identities always include the construction time so they
				// cannot renumber an existing air-launched lifetime on that EID.
				tr.ID = uint64(source.SpawnSample.Time)<<32 | id
			}
			if surface[source] != nil {
				// Initial snapshots can construct a SAM at t=0. Reserve existing
				// aircraft-store keys before allocating any new surface keys.
				for reservedIDs[tr.ID] {
					tr.ID += uint64(1) << 32
				}
				reservedIDs[tr.ID] = true
			}
			add := func(s projectiles.Sample) {
				if s.Time < source.SpawnSample.Time || (source.HasEnd && s.Time > source.EndTime) {
					return
				}
				sourceSample := s
				tr.Samples = append(tr.Samples, acmiSample{t: s.Time, x: s.X, y: s.Y, z: s.Z, projectile: &sourceSample})
			}
			for _, sample := range byEntity[id] {
				add(sample)
			}
			// A spawn marker alone does not establish a missile flight track.
			if len(tr.Samples) > 0 {
				updates := tr.Samples
				tr.Samples = nil
				add(source.SpawnSample)
				tr.Samples = append(tr.Samples, updates...)
				sort.SliceStable(tr.Samples, func(i, j int) bool { return tr.Samples[i].t < tr.Samples[j].t })
				unique := tr.Samples[:0]
				for _, sample := range tr.Samples {
					if len(unique) > 0 && unique[len(unique)-1].t == sample.t {
						// Several messages can update the same object in one game
						// tick. The last recorded update supersedes its predecessor.
						unique[len(unique)-1] = sample
					} else {
						unique = append(unique, sample)
					}
				}
				tr.Samples = unique
				result = append(result, tr)
			}
		}
	}
	return result
}

func guidedProjectile(source *projectiles.Projectile) bool {
	if source == nil || isBombProjectile(source) {
		return false
	}
	// Aircraft trigger IDs from the weapon reference: 8 = AAM, 7 = AGM.
	// Trigger 14 is countermeasures; its Rocket component is not a missile.
	trigger := source.WeaponRef >> 16
	return trigger == 8 || trigger == 7
}

func projectileEntityID(wireID uint64) (uint64, bool) {
	if wireID > 0xffffffff {
		return 0, false
	}
	// WeaponSync's observed packed handle uses 22 high index bits and a
	// 10-bit generation code. Code 1 is the compact generation-one form;
	// other generations are encoded as generation+2. This layout is inferred
	// from replay 0x00018C1C and verified against all 104 guided construction
	// identities, owners and lifetimes, rather than matching by proximity.
	code := wireID & 0x3ff
	if code == 0 || code > 257 {
		return 0, false
	}
	generation := uint64(1)
	if code != 1 {
		generation = code - 2
	}
	return (generation << 22) | (wireID >> 10), true
}
