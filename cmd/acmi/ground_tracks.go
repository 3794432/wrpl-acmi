package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/game"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

// ECS identities occupy 32 bits. A separate namespace retains a real replay
// unit UID when the recording has no matching ECS construction metadata.
const groundUnitUIDNamespace = uint64(1) << 63

// appendGroundTracks keeps complete ECS identities separate from legacy packed
// handles and appends new surface objects after existing aircraft and weapons.
// This preserves the existing ACMI ids used by parent and radar references.
func appendGroundTracks(tracks []*track, paths map[uint64][]game.SpaceTime, entities map[uint64]*packetecs2.Entity, ends map[uint64]uint32, metadata ...*groundmeta.Parser) []*track {
	// UID-level ends may belong to an older ECS generation. Preserve the full
	// recorded path whenever the later identity pass can resolve its lifetimes.
	hasHistory := map[uint64]bool{}
	if len(metadata) > 0 && metadata[0] != nil {
		for _, info := range metadata[0].All {
			if info.HasConstructionTime {
				hasHistory[groundUnitUIDNamespace|uint64(info.UID)] = true
			}
		}
	}
	out := append([]*track(nil), tracks...)
	ids := make([]uint64, 0, len(paths))
	for id := range paths {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	existing := make(map[trackKey]bool, len(tracks))
	for _, tr := range tracks {
		if tr != nil {
			existing[tr.key] = true
		}
	}
	for _, id := range ids {
		key := trackKey{groundEntity: true, id: id}
		if existing[key] {
			continue
		}
		tr := &track{key: key, entity: entities[id]}
		if id&groundUnitUIDNamespace != 0 {
			tr.name = fmt.Sprintf("Ground unit %d", id&^groundUnitUIDNamespace)
		}
		if !hasHistory[id] {
			tr.end, tr.hasEnd = ends[id]
		}
		for _, sample := range paths[id] {
			if tr.hasEnd && sample.Time > tr.end {
				continue
			}
			if !finiteGroundPosition(sample.X, sample.Y, sample.Z) {
				continue
			}
			tr.samples = append(tr.samples, acmiSample{t: sample.Time, x: sample.X, y: sample.Y, z: sample.Z})
		}
		sort.SliceStable(tr.samples, func(i, j int) bool { return tr.samples[i].t < tr.samples[j].t })
		unique := tr.samples[:0]
		for _, sample := range tr.samples {
			if len(unique) > 0 && unique[len(unique)-1].t == sample.t {
				unique[len(unique)-1] = sample
			} else {
				unique = append(unique, sample)
			}
		}
		tr.samples = unique
		if len(tr.samples) > 0 {
			out = append(out, tr)
			existing[key] = true
		}
	}
	return out
}

func finiteGroundPosition(x, y, z float64) bool {
	for _, v := range []float64{x, y, z} {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 10_000_000 {
			return false
		}
	}
	return true
}

func isGroundTrack(tr *track) bool {
	return tr != nil && !tr.key.air && !tr.key.missile && !tr.key.bomb && !tr.key.decoy
}

func groundTrackType(tr *track) string {
	if tr != nil && tr.groundStatic {
		return "Ground+Static+Building"
	}
	if tr != nil {
		if typ := groundModelType(tr.groundModel); typ != "" {
			return typ
		}
	}
	if tr != nil && tr.entity != nil {
		for _, part := range strings.Split(tr.entity.Template, "+") {
			switch part {
			case "ship":
				return "Sea+Watercraft"
			case "fortification":
				return "Ground+Static+Building"
			}
		}
	}
	return "Ground+Vehicle"
}
