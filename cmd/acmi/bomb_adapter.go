package main

import (
	"sort"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

const (
	bombProjectileKind = "bomb"
	bombDisplayName    = "Bomb"
)

// bombTrack is the parser-to-writer adapter for real class-1 WeaponSync
// positions. ID is either the full ECS EID when confirmed bomb metadata exists,
// or the exact 32-bit wire identity when metadata is absent. Other fields come
// only from explicit construction evidence.
type bombTrack struct {
	ID           uint64
	ParentUID    uint64
	HasParent    bool
	Samples      []acmiSample
	EndTime      uint32
	HasEnd       bool
	Source       *projectiles.Projectile
	WireID       bool
	PositionOnly bool
}

func isBombProjectile(source *projectiles.Projectile) bool {
	return source != nil && source.Kind == bombProjectileKind
}

// decodedBombTracks keeps class-1 positions that the projectile parser stores
// separately from rocket samples. A wire identity is associated with metadata
// only when its full ECS EID has a construction explicitly marked as a bomb.
// Unmatched positions remain independent tracks keyed by the complete wire ID.
func decodedBombTracks(metadata *projectiles.MetadataParser, positions *projectiles.PositionParser) []bombTrack {
	if positions == nil {
		positions = &projectiles.PositionParser{}
	}

	rawIDs := make([]uint64, 0, len(positions.BombSamples))
	for rawID := range positions.BombSamples {
		rawIDs = append(rawIDs, rawID)
	}
	sort.Slice(rawIDs, func(i, j int) bool { return rawIDs[i] < rawIDs[j] })

	var histories map[uint64][]*projectiles.Projectile
	if metadata != nil {
		histories = metadata.History
	}
	associated := make(map[uint64][]projectiles.Sample)
	unmatched := make(map[uint64][]projectiles.Sample)
	for _, rawID := range rawIDs {
		samples := positions.BombSamples[rawID]
		if len(samples) == 0 {
			continue
		}
		if eid, ok := projectileEntityID(rawID); ok && countBombInstances(histories[eid]) > 0 {
			associated[eid] = append(associated[eid], samples...)
			continue
		}
		unmatched[rawID] = append(unmatched[rawID], samples...)
	}

	result := make([]bombTrack, 0, len(associated)+len(unmatched))
	associatedIDs := make([]uint64, 0, len(histories))
	for eid, instances := range histories {
		if countBombInstances(instances) > 0 {
			associatedIDs = append(associatedIDs, eid)
		}
	}
	sort.Slice(associatedIDs, func(i, j int) bool { return associatedIDs[i] < associatedIDs[j] })
	for _, eid := range associatedIDs {
		instances := histories[eid]
		bombInstances := countBombInstances(instances)
		for i, source := range instances {
			if !isBombProjectile(source) {
				continue
			}
			nextSpawnTime, hasNextConstruction := nextConstructionSpawnTime(instances, i)
			filtered := filterBombSamples(associated[eid], source, nextSpawnTime, hasNextConstruction)
			samples := bombACMiSamples(filtered)
			positionOnly := len(samples) == 0
			if positionOnly {
				// Server recordings may contain construction and destruction only.
				// Keep the known release position explicitly as a marker; do not
				// turn an unknown flight path into a simulated trajectory.
				samples = bombACMiSamples([]projectiles.Sample{source.SpawnSample})
			}
			end, hasEnd := source.EndTime, source.HasEnd
			if hasNextConstruction && (!hasEnd || nextSpawnTime < end) {
				end, hasEnd = nextSpawnTime, true
			}
			id := eid
			if bombInstances > 1 {
				// A full EID can be reconstructed. Include the real construction
				// tick so each reuse remains a distinct ACMI lifetime.
				id = uint64(source.SpawnSample.Time)<<32 | eid
			}
			result = append(result, bombTrack{
				ID:           id,
				ParentUID:    source.ParentUID,
				HasParent:    source.HasParent,
				Samples:      samples,
				EndTime:      end,
				HasEnd:       hasEnd,
				Source:       source,
				PositionOnly: positionOnly,
			})
		}
	}

	for _, rawID := range rawIDs {
		samples := bombACMiSamples(unmatched[rawID])
		if len(samples) == 0 {
			continue
		}
		result = append(result, bombTrack{ID: rawID, Samples: samples, WireID: true})
	}
	return result
}

func countBombInstances(instances []*projectiles.Projectile) int {
	count := 0
	for _, source := range instances {
		if isBombProjectile(source) {
			count++
		}
	}
	return count
}

func filterBombSamples(samples []projectiles.Sample, source *projectiles.Projectile, nextSpawnTime uint32, hasNextConstruction bool) []projectiles.Sample {
	if source == nil {
		return nil
	}
	filtered := make([]projectiles.Sample, 0, len(samples))
	for _, sample := range samples {
		if sample.Time < source.SpawnSample.Time {
			continue
		}
		if source.HasEnd && sample.Time > source.EndTime {
			continue
		}
		if hasNextConstruction && sample.Time >= nextSpawnTime {
			continue
		}
		filtered = append(filtered, sample)
	}
	return filtered
}

// bombACMiSamples preserves only recorded positions. Same-tick updates retain
// the last one, matching WeaponSync packet order. No spawn point is added when
// it is absent from class 1, and no point is interpolated.
func bombACMiSamples(samples []projectiles.Sample) []acmiSample {
	result := make([]acmiSample, 0, len(samples))
	for _, sample := range samples {
		result = append(result, acmiSample{t: sample.Time, x: sample.X, y: sample.Y, z: sample.Z})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].t < result[j].t })
	if len(result) < 2 {
		return result
	}
	unique := result[:0]
	for _, sample := range result {
		if len(unique) > 0 && unique[len(unique)-1].t == sample.t {
			unique[len(unique)-1] = sample
			continue
		}
		unique = append(unique, sample)
	}
	return unique
}

// appendBombTracks adds bombs after every existing track, including estimated
// countermeasures, so existing ACMI object IDs remain stable. Parent links use
// only an explicit construction owner that resolves to an aircraft track.
func appendBombTracks(tracks []*track, inputs []bombTrack) []*track {
	out := append([]*track(nil), tracks...)
	existing := make(map[trackKey]bool, len(tracks))
	for _, tr := range tracks {
		if tr != nil {
			existing[tr.key] = true
		}
	}

	replayEnd, hasReplayEnd := lastRecordedTrackTime(tracks)
	added := make([]*track, 0, len(inputs))
	for _, input := range inputs {
		if input.PositionOnly && len(input.Samples) > 0 && !input.HasEnd && hasReplayEnd && replayEnd >= input.Samples[0].t {
			input.EndTime, input.HasEnd = replayEnd, true
		}
		samples := append([]acmiSample(nil), input.Samples...)
		sort.SliceStable(samples, func(i, j int) bool { return samples[i].t < samples[j].t })
		if input.HasEnd {
			end := sort.Search(len(samples), func(i int) bool { return samples[i].t > input.EndTime })
			samples = samples[:end]
		}
		if len(samples) == 0 {
			continue
		}
		key := trackKey{bomb: true, bombWire: input.WireID, id: input.ID}
		if existing[key] {
			continue
		}
		tr := &track{
			key:              key,
			name:             bombDisplayName,
			samples:          samples,
			end:              input.EndTime,
			hasEnd:           input.HasEnd,
			projectileSource: input.Source,
			bombPositionOnly: input.PositionOnly,
		}
		if input.HasParent {
			parentTime := samples[0].t
			if input.Source != nil {
				parentTime = input.Source.SpawnSample.Time
			}
			tr.parent = aircraftParentAt(tracks, input.ParentUID, parentTime)
		}
		added = append(added, tr)
		existing[key] = true
	}
	sort.Slice(added, func(i, j int) bool {
		if added[i].key.id != added[j].key.id {
			return added[i].key.id < added[j].key.id
		}
		// A metadata-backed EID precedes a same-numbered wire fallback so a
		// numeric collision remains deterministic as well as distinct.
		return !added[i].key.bombWire && added[j].key.bombWire
	})
	return append(out, added...)
}
