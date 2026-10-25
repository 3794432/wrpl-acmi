package main

import (
	"fmt"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

const (
	optionalDamageUIDMask    = uint16(0x7ff)
	optionalDamageUnknownUID = uint16(0x7ff)
)

// Damage reports use entity UIDs in the low eleven bits. Fields carrying a
// packed raw value may have higher bits set, so the raw value is never an
// entity id by itself. Field 3 names the attacker's vehicle; field 4 is the
// attacker's entity UID. Field 2 is the attacker's roster slot at that packet
// time, not an entity UID.
type optionalDamageRecord struct {
	severe bool

	victimRawUID uint16
	victimUID    uint16
	hasVictimUID bool

	attackerPID     uint32
	hasAttackerPID  bool
	attackerRawUID  uint16
	attackerUID     uint16
	hasAttackerUID  bool
	attackerVehicle string
}

type optionalDamageTrackIndex map[uint16][]*track

func newOptionalDamageTrackIndex(tracks []*track) optionalDamageTrackIndex {
	index := make(optionalDamageTrackIndex)
	for _, tr := range tracks {
		if !isKillAircraft(tr) || tr.key.id > uint64(optionalDamageUIDMask) {
			continue
		}
		uid := uint16(tr.key.id)
		index[uid] = append(index[uid], tr)
	}
	return index
}

// uniqueActiveDamageTrack refuses to choose between simultaneous objects with
// the same UID. It also excludes objects that have not appeared yet or whose
// existing aircraft lifecycle has already ended.
func (index optionalDamageTrackIndex) uniqueActiveDamageTrack(uid uint16, t uint32) *track {
	if uid == optionalDamageUnknownUID {
		return nil
	}
	var found *track
	for _, tr := range index[uid] {
		if !optionalDamageTrackActiveAt(tr, t) {
			continue
		}
		if found != nil && found != tr {
			return nil
		}
		found = tr
	}
	return found
}

func optionalDamageTrackActiveAt(tr *track, t uint32) bool {
	if tr == nil || len(tr.samples) == 0 || t < tr.samples[0].t {
		return false
	}
	return !tr.hasEnd || t <= tr.end
}

func (p *optionalEventParser) optionalDamageTimeline(stored optionalStoredEvent, index optionalDamageTrackIndex, names map[string]string) (timelineEvent, bool) {
	record := stored.damage
	if p == nil || record == nil {
		return timelineEvent{}, false
	}

	victimModel, victimPlayer, victim := p.optionalDamageVictim(record, index, stored.t, names)
	attackerModel, attackerPlayer, attacker := p.optionalDamageAttacker(record, index, stored.t, names)
	if victimModel == "" {
		victimModel = "Unknown vehicle"
	}
	if victimPlayer == "" {
		victimPlayer = "Unknown player"
	}
	if attackerModel == "" {
		attackerModel = "Unknown vehicle"
	}
	if attackerPlayer == "" {
		attackerPlayer = "Unknown player"
	}
	kind := "critical damaged"
	if record.severe {
		kind = "severe damaged"
	}
	event := timelineEvent{
		t:    stored.t,
		text: fmt.Sprintf("%s (%s) %s by %s (%s)", victimModel, victimPlayer, kind, attackerModel, attackerPlayer),
	}
	if victim != nil {
		event.keys = append(event.keys, victim.key)
	}
	if attacker != nil && (victim == nil || attacker.key != victim.key) {
		event.keys = append(event.keys, attacker.key)
	}
	return event, true
}

func (p *optionalEventParser) optionalDamageVictim(record *optionalDamageRecord, index optionalDamageTrackIndex, t uint32, names map[string]string) (string, string, *track) {
	if record == nil || !record.hasVictimUID {
		return "", "", nil
	}
	tr := index.uniqueActiveDamageTrack(record.victimUID, t)
	if tr == nil {
		return "", "", nil
	}
	return p.optionalDamageTrackModel(tr, names), p.optionalDamageTrackPlayer(tr, t), tr
}

func (p *optionalEventParser) optionalDamageAttacker(record *optionalDamageRecord, index optionalDamageTrackIndex, t uint32, names map[string]string) (string, string, *track) {
	if record == nil {
		return "", "", nil
	}
	var attacker *track
	if record.hasAttackerUID {
		if candidate := index.uniqueActiveDamageTrack(record.attackerUID, t); candidate != nil &&
			p.optionalDamageAttackerTrackMatches(record, candidate, t) {
			attacker = candidate
		}
	}
	model := cleanOptionalText(record.attackerVehicle, optionalMaxStringBytes)
	if model != "" {
		model = displayModelName(model)
	} else if attacker != nil {
		model = p.optionalDamageTrackModel(attacker, names)
	}
	player := p.optionalDamagePlayerAt(record.attackerPID, record.hasAttackerPID, t)
	return model, player, attacker
}

func (p *optionalEventParser) optionalDamageTrackModel(tr *track, names map[string]string) string {
	if tr == nil {
		return ""
	}
	name := cleanOptionalText(trackName(tr, p.slot, names), optionalMaxStringBytes)
	if name == "" || name == fmt.Sprintf("air:%d", tr.key.id) {
		return ""
	}
	return name
}

func (p *optionalEventParser) optionalDamageTrackPlayer(tr *track, t uint32) string {
	slot, ok := p.optionalDamageTrackSlot(tr)
	if !ok || !p.optionalDamageTrackRosterStable(slot, tr, t) {
		return ""
	}
	return p.optionalDamagePlayerAt(uint32(slot), true, t)
}

func (p *optionalEventParser) optionalDamageTrackSlot(tr *track) (int, bool) {
	if tr == nil || tr.entity == nil {
		return 0, false
	}
	pid, ok := packetecs2.GetObjectData[int32](&tr.entity.Data, "unit__playerId")
	if !ok || pid < 0 || pid >= 256 {
		return 0, false
	}
	return int(pid), true
}

// A track that spans a roster replacement has no reliable pilot for the later
// event. Keep its exact UID/model when it is the victim, but do not carry the
// later slot occupant into the message.
func (p *optionalEventParser) optionalDamageTrackRosterStable(slot int, tr *track, t uint32) bool {
	if p == nil || tr == nil || len(tr.samples) == 0 {
		return false
	}
	start, startOK := p.optionalRosterAt(slot, tr.samples[0].t)
	event, eventOK := p.optionalRosterAt(slot, t)
	if startOK && eventOK && start.known && event.known && !optionalSamePlayer(start, event) {
		return false
	}
	return true
}

// An attacker UID may identify an older aircraft generation after its entity
// UID was reused. The event PID is the authoritative slot snapshot. Reject the
// track reference when its roster identity conflicts with that snapshot; a
// rejected track must not supply a fallback vehicle model either.
func (p *optionalEventParser) optionalDamageAttackerTrackMatches(record *optionalDamageRecord, tr *track, t uint32) bool {
	slot, ok := p.optionalDamageTrackSlot(tr)
	if !ok {
		return true
	}
	if !p.optionalDamageTrackRosterStable(slot, tr, t) {
		return false
	}
	if record == nil || !record.hasAttackerPID || record.attackerPID >= 256 {
		return true
	}
	pidEntry, pidOK := p.optionalRosterAt(int(record.attackerPID), t)
	trackEntry, trackOK := p.optionalRosterAt(slot, t)
	if pidOK && trackOK && pidEntry.known && trackEntry.known && !optionalSamePlayer(pidEntry, trackEntry) {
		return false
	}
	return true
}

func (p *optionalEventParser) optionalDamagePlayerAt(pid uint32, present bool, t uint32) string {
	if p == nil || !present || pid >= 256 {
		return ""
	}
	entry, ok := p.optionalRosterAt(int(pid), t)
	if !ok || !entry.known {
		return ""
	}
	return cleanOptionalText(entry.name, optionalMaxStringBytes)
}
