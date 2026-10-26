package main

import (
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
	"sort"
)

type groundIdentityInterval struct {
	info     groundmeta.Info
	conflict bool
}

// ECS destruction can describe later removal of a wreck. An exact F058 victim
// report supplies the earlier death time without borrowing a recycled UID's
// other lifetime, keeping kill attribution consistent with object removal.
func applyGroundKillLifetimes(tracks []*track, kills []packetkill.KillEntry) {
	index := newKillTimelineIndex(tracks)
	for _, kill := range kills {
		if !kill.HasVictimUID || kill.VictimUid == 0x7ff {
			continue
		}
		victim := index.exactVictim(kill)
		if !isGroundTrack(victim) {
			continue
		}
		if victim.hasEnd && victim.end < kill.CurrentTime {
			continue
		}
		victim.end = kill.CurrentTime
		victim.hasEnd = true
		victim.retired = false
		n := sort.Search(len(victim.samples), func(i int) bool { return victim.samples[i].t > kill.CurrentTime })
		victim.samples = victim.samples[:n]
	}
}

// A UID can be reassigned to a different vehicle and even a different team.
// Construction timestamps and full EIDs distinguish those identities; position
// jumps, sample gaps and the final UID lookup are never identity evidence.
func applyGroundIdentityHistory(tracks []*track, metadata *groundmeta.Parser, identifyModels bool) []*track {
	if metadata == nil || len(metadata.All) == 0 {
		return tracks
	}
	byUID := map[uint64][]*groundIdentityInterval{}
	byEID := map[uint64]*groundIdentityInterval{}
	for _, info := range metadata.All {
		if !info.HasConstructionTime {
			continue
		}
		localConflict := info.ClassModel != "" && info.StorageModel != "" && !groundmeta.SameModelIdentity(info.ClassModel, info.StorageModel)
		g := byEID[info.EID]
		if g == nil {
			g = &groundIdentityInterval{info: info, conflict: localConflict}
			byEID[info.EID] = g
			byUID[uint64(info.UID)] = append(byUID[uint64(info.UID)], g)
			continue
		}
		if info.UID != g.info.UID || localConflict || (g.info.RawModel != "" && info.RawModel != "" && !groundmeta.SameModelIdentity(g.info.RawModel, info.RawModel)) {
			g.conflict = true
		}
		if g.info.Team != nil && info.Team != nil && *g.info.Team != *info.Team {
			g.conflict = true
		}
		if info.ConstructionTime < g.info.ConstructionTime {
			g.info.ConstructionTime = info.ConstructionTime
		}
		if g.info.RawModel == "" && info.RawModel != "" {
			birth := g.info.ConstructionTime
			g.info = info
			g.info.ConstructionTime = birth
		}
	}
	for _, history := range byUID {
		sort.SliceStable(history, func(i, j int) bool { return history[i].info.ConstructionTime < history[j].info.ConstructionTime })
		for i := 1; i < len(history); i++ {
			if history[i-1].info.ConstructionTime == history[i].info.ConstructionTime {
				history[i-1].conflict = true
				history[i].conflict = true
			}
		}
	}
	out := make([]*track, 0, len(tracks))
	var extra []*track
	for _, tr := range tracks {
		if !isGroundTrack(tr) || !tr.key.groundEntity || tr.key.id&groundUnitUIDNamespace == 0 || len(tr.samples) == 0 {
			out = append(out, tr)
			continue
		}
		uid := tr.key.id &^ groundUnitUIDNamespace
		history := byUID[uid]
		if len(history) == 0 {
			out = append(out, tr)
			continue
		}
		var pieces []*track
		start := 0
		for start < len(tr.samples) {
			sampleTime := tr.samples[start].t
			index := sort.Search(len(history), func(i int) bool { return history[i].info.ConstructionTime > sampleTime }) - 1
			end := len(tr.samples)
			if index+1 < len(history) {
				boundary := history[index+1].info.ConstructionTime
				end = start + sort.Search(len(tr.samples)-start, func(i int) bool { return tr.samples[start+i].t >= boundary })
			}
			if end <= start {
				end = start + 1
			}
			part := *tr
			part.key.sortie = tr.key.sortie + uint32(len(pieces))
			part.samples = append([]acmiSample(nil), tr.samples[start:end]...)
			part.entity = nil
			part.groundModel = nil
			part.groundModelsDisabled = !identifyModels
			part.groundTeam = 0
			part.groundTeamSource = ""
			part.end = 0
			part.hasEnd = false
			part.retired = false
			if index+1 < len(history) && history[index+1].info.ConstructionTime > 0 {
				part.end = history[index+1].info.ConstructionTime - 1
				part.hasEnd = true
				part.retired = true
			}
			if index >= 0 {
				interval := history[index]
				info := interval.info
				if death, ok := metadata.Ends[info.EID]; ok && (!part.hasEnd || death <= part.end) {
					part.end = death
					part.hasEnd = true
					part.retired = false
				}
				if !interval.conflict {
					// Global UID conflict flags describe other generations. This
					// interval was checked independently against its exact EID.
					info.Conflict = false
					info.ClassConflict = false
					part.entity = info.Entity
					applyGroundTeamEvidence([]*track{&part}, map[uint64]groundmeta.Info{uid: info})
					if identifyModels {
						applyGroundModelEvidence([]*track{&part}, replayGroundModelEvidence(map[uint64]groundmeta.Info{uid: info}))
					}
				}
			}
			if part.hasEnd {
				n := sort.Search(len(part.samples), func(i int) bool { return part.samples[i].t > part.end })
				part.samples = part.samples[:n]
			}
			if len(part.samples) > 0 {
				pieces = append(pieces, &part)
			}
			start = end
		}
		if len(pieces) > 0 {
			out = append(out, pieces[0])
			extra = append(extra, pieces[1:]...)
		}
	}
	return append(out, extra...)
}
