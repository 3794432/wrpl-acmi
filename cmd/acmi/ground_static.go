package main

import (
	"math"
	"sort"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

// Only explicit structures/fortifications may retain a single construction
// transform for their lifetime. A vehicle with missing sync is not static.
func staticGroundEvidence(info groundmeta.Info) bool {
	// Mission dummies carry a real transform but are reference/trigger objects,
	// not physical buildings. Their storage category alone is insufficient.
	switch groundModelKey(info.RawModel) {
	case "dummy", "dummy_immortal":
		return false
	}
	if info.Storage == "unit_storage__fortification" {
		return true
	}
	if strings.HasPrefix(strings.ReplaceAll(info.RawModel, "\\", "/"), "structures/") {
		return true
	}
	label := groundModelNames[groundModelKey(info.RawModel)]
	return label.Kind == "building" && label.KindSource != ""
}

func appendStaticGroundTracks(tracks []*track, metadata *groundmeta.Parser) []*track {
	if metadata == nil {
		return tracks
	}
	existing := map[uint64]bool{}
	for _, tr := range tracks {
		if uid, ok := groundTrackUID(tr); ok {
			existing[uid] = true
		}
	}
	// All contains copied positions from successfully parsed constructions.
	first := map[uint64]groundmeta.Info{}
	conflict := map[uint64]bool{}
	identities := map[uint64]uint64{}
	for _, info := range metadata.All {
		uid := uint64(info.UID)
		if eid, known := identities[uid]; known && eid != info.EID {
			conflict[uid] = true
		}
		identities[uid] = info.EID
		if existing[uid] || info.Conflict || info.ClassConflict {
			continue
		}
		if old, found := first[uid]; found && old.EID != info.EID {
			conflict[uid] = true
		}
		if !staticGroundEvidence(info) || info.Position == nil || !info.HasConstructionTime {
			continue
		}
		p := info.Position
		if !finiteGroundPosition(float64(p.X), float64(p.Y), float64(p.Z)) {
			conflict[uid] = true
			continue
		}
		if old, found := first[uid]; found {
			o := old.Position
			if math.Abs(float64(p.X)-float64(o.X)) > 0.01 || math.Abs(float64(p.Y)-float64(o.Y)) > 0.01 || math.Abs(float64(p.Z)-float64(o.Z)) > 0.01 {
				conflict[uid] = true
			}
			continue
		}
		first[uid] = info
	}
	uids := make([]uint64, 0, len(first))
	for uid := range first {
		uids = append(uids, uid)
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	out := append([]*track(nil), tracks...)
	for _, uid := range uids {
		info := first[uid]
		latest, ok := metadata.Models[uid]
		if conflict[uid] || !ok || latest.Conflict || latest.ClassConflict || latest.EID != info.EID {
			continue
		}
		p := info.Position
		tr := &track{key: trackKey{groundEntity: true, id: groundUnitUIDNamespace | uid}, entity: info.Entity, groundStatic: true,
			samples: []acmiSample{{t: info.ConstructionTime, x: float64(p.X), y: float64(p.Y), z: float64(p.Z)}}}
		tr.end, tr.hasEnd = metadata.Ends[info.EID]
		if tr.hasEnd && tr.end < info.ConstructionTime {
			continue
		}
		out = append(out, tr)
	}
	return out
}
