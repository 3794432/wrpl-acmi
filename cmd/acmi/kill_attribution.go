package main

import (
	"fmt"
	"strings"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

type killTimelineIndex struct {
	airByUID    map[uint16][]*track
	groundByUID map[uint16][]*track
	byEntity    map[*packetecs2.Entity][]*track
}

// buildKillTimeline uses only exact UID or ECS entity identity. PID values are
// retained by the packet parser for its existing consumers, but they are never
// compared with UIDs here. A ground victim must either resolve to the exact ECS
// entity or carry the explicit ground UID namespace; a legacy object whose id
// merely equals the UID is not evidence of identity.
func buildKillTimeline(tracks []*track, kills []packetkill.KillEntry, slot *packetslot.PacketSlotParser, names map[string]string) []timelineEvent {
	index := newKillTimelineIndex(tracks)
	out := make([]timelineEvent, 0)
	for _, kill := range kills {
		if !kill.HasKillerUID || !kill.HasVictimUID ||
			kill.KillerUid == 0x7ff || kill.VictimUid == 0x7ff {
			continue
		}
		weapon := strings.TrimSpace(kill.PlayerWeapon)
		if weapon == "" {
			continue
		}
		killer := index.exactKiller(kill)
		victim := index.exactVictim(kill)
		if killer == nil || victim == nil {
			continue
		}

		// A kill report after the killer track has ended is posthumous.
		// timelineLines requires every attributed object to exist at the event
		// time. Dropping the killer key would silently turn the report into an
		// unattributed message, and moving the time would change the evidence, so
		// the event is omitted instead.
		if !killTrackContains(killer, kill.CurrentTime) {
			continue
		}
		if !killVictimLifetimeContains(victim, kill.CurrentTime) {
			continue
		}
		victimName := strings.TrimSpace(trackName(victim, slot, names))
		if victimName == "" {
			continue
		}
		out = append(out, timelineEvent{
			t:    kill.CurrentTime,
			keys: []trackKey{killer.key},
			text: fmt.Sprintf("Killed %s with %s", victimName, weapon),
		})
	}
	return out
}

func newKillTimelineIndex(tracks []*track) killTimelineIndex {
	index := killTimelineIndex{
		airByUID:    make(map[uint16][]*track),
		groundByUID: make(map[uint16][]*track),
		byEntity:    make(map[*packetecs2.Entity][]*track),
	}
	for _, tr := range tracks {
		if tr == nil {
			continue
		}
		switch {
		case isKillAircraft(tr):
			if tr.key.id <= 0x7ff {
				uid := uint16(tr.key.id)
				index.airByUID[uid] = append(index.airByUID[uid], tr)
			}
			if tr.entity != nil {
				index.byEntity[tr.entity] = append(index.byEntity[tr.entity], tr)
			}
		case isGroundTrack(tr):
			if tr.key.groundEntity && tr.key.id&groundUnitUIDNamespace != 0 {
				uid := tr.key.id &^ groundUnitUIDNamespace
				if uid <= 0x7ff {
					index.groundByUID[uint16(uid)] = append(index.groundByUID[uint16(uid)], tr)
				}
			}
			if tr.entity != nil {
				index.byEntity[tr.entity] = append(index.byEntity[tr.entity], tr)
			}
		}
	}
	return index
}

func isKillAircraft(tr *track) bool {
	return tr != nil && tr.key.air && !tr.key.missile && !tr.key.bomb && !tr.key.decoy && !tr.key.groundEntity
}

func (index killTimelineIndex) exactKiller(kill packetkill.KillEntry) *track {
	var candidates []*track
	if kill.ResolvedKiller != nil {
		for _, tr := range index.byEntity[kill.ResolvedKiller] {
			switch {
			case isKillAircraft(tr):
				if tr.key.id == uint64(kill.KillerUid) {
					candidates = append(candidates, tr)
				}
			case isGroundTrack(tr):
				candidates = append(candidates, tr)
			}
		}
	} else {
		candidates = append(candidates, index.airByUID[kill.KillerUid]...)
		candidates = append(candidates, index.groundByUID[kill.KillerUid]...)
	}
	var found *track
	for _, tr := range candidates {
		if !killTrackContains(tr, kill.CurrentTime) {
			continue
		}
		if found != nil {
			return nil
		}
		found = tr
	}
	return found
}

func (index killTimelineIndex) exactVictim(kill packetkill.KillEntry) *track {
	var candidates []*track
	if kill.ResolvedVictim != nil {
		for _, tr := range index.byEntity[kill.ResolvedVictim] {
			switch {
			case isKillAircraft(tr):
				if tr.key.id == uint64(kill.VictimUid) {
					candidates = append(candidates, tr)
				}
			case isGroundTrack(tr):
				candidates = append(candidates, tr)
			}
		}
	} else {
		candidates = append(candidates, index.airByUID[kill.VictimUid]...)
		candidates = append(candidates, index.groundByUID[kill.VictimUid]...)
	}
	var found *track
	for _, tr := range candidates {
		if !killTrackContains(tr, kill.CurrentTime) {
			continue
		}
		if found != nil {
			return nil
		}
		found = tr
	}
	return found
}

// Aircraft ends are populated from the F058 destruction timestamp, so only
// the report at that exact end time may describe the aircraft's death. A
// ground victim with an observed end follows the same rule; only an unknown
// ground end may use its sampled lifetime to contain the report.
func killVictimLifetimeContains(victim *track, t uint32) bool {
	if !killTrackContains(victim, t) {
		return false
	}
	if victim.key.air || victim.hasEnd {
		return victim.hasEnd && !victim.retired && victim.end == t
	}
	return true
}

func killTrackContains(tr *track, t uint32) bool {
	if tr == nil || len(tr.samples) == 0 || t < tr.samples[0].t {
		return false
	}
	return !tr.hasEnd || t <= tr.end
}
