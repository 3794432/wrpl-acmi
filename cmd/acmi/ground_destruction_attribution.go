package main

import (
	"fmt"
	"strconv"
	"strings"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
)

type groundDestructionAttribution struct {
	text, weapon, weaponName, killerVehicle, killerPlayer, identitySource string
	killerUID                                                             uint16
	killerPID                                                             uint32
	hasUID, hasPID                                                        bool
}

// The weapon names describe exact aliases only. Unknown ammunition retains
// the report's raw weapon text. This never infers a weapon from blast radius.
var destructionWeaponNames = func() map[string]string {
	names := map[string]string{}
	missiles, err := loadMissileModels("embedded")
	if err != nil {
		panic("invalid destruction missile names: " + err.Error())
	}
	for key, entry := range missiles.Aliases {
		names[strings.ToLower(key)] = entry.Model
	}
	for _, input := range []struct {
		raw  []byte
		bomb bool
	}{{embeddedBombModelsJSON, true}} {
		var table *missileModelTable
		var err error
		if input.bomb {
			table, err = parseBombModelsJSON(input.raw)
		} else {
			table, err = parseMissileModelsJSON(input.raw)
		}
		if err != nil {
			panic("invalid destruction weapon names: " + err.Error())
		}
		for key, entry := range table.Aliases {
			key = strings.ToLower(key)
			if old := names[key]; old != "" && old != entry.Model {
				panic("conflicting destruction weapon name: " + key)
			}
			names[key] = entry.Model
		}
	}
	for key, entry := range samMissileNames.Names {
		if names[strings.ToLower(key)] == "" {
			names[strings.ToLower(key)] = entry.Model
		}
	}
	return names
}()

func destructionWeaponName(raw string) string {
	if name := destructionWeaponNames[strings.ToLower(strings.TrimSpace(raw))]; name != "" {
		return name
	}
	return strings.TrimSpace(raw)
}

// A dead/unresolved attacker need not exist in Tacview to be named in the
// victim's native Destroyed event. PID is resolved only through the roster at
// report time; it is never compared with a vehicle UID or used to revive it.
func buildGroundDestructionAttributions(tracks []*track, kills []packetkill.KillEntry, roster *optionalEventParser, names map[string]string) map[trackKey]groundDestructionAttribution {
	if roster == nil {
		roster = &optionalEventParser{}
	}
	index := newKillTimelineIndex(tracks)
	out := map[trackKey]groundDestructionAttribution{}
	conflicts := map[trackKey]bool{}
	for _, kill := range kills {
		if !kill.HasVictimUID || kill.VictimUid == 0x7ff || strings.TrimSpace(kill.PlayerWeapon) == "" {
			continue
		}
		victim := index.exactVictim(kill)
		if !isGroundTrack(victim) || !victim.hasEnd || !killVictimLifetimeContains(victim, kill.CurrentTime) || conflicts[victim.key] {
			continue
		}
		entry := groundDestructionAttribution{
			weapon: strings.TrimSpace(kill.PlayerWeapon), weaponName: destructionWeaponName(kill.PlayerWeapon),
			killerUID: kill.KillerUid, hasUID: kill.HasKillerUID && kill.KillerUid != 0x7ff,
			killerPID: kill.KillerPid, hasPID: kill.HasKillerPID,
			identitySource: "ReportVehicleOnly"}
		var killer *track
		if entry.hasUID {
			killer = index.exactKiller(kill)
		}
		historical := false
		if killer == nil && entry.hasUID {
			killer = historicalGroundKillAttacker(index, kill, roster)
			historical = killer != nil
		}
		if killer != nil && roster != nil {
			record := &optionalDamageRecord{attackerPID: kill.KillerPid, hasAttackerPID: kill.HasKillerPID}
			if !roster.optionalDamageAttackerTrackMatches(record, killer, kill.CurrentTime) {
				killer = nil
			}
		}
		rawVehicle := cleanOptionalText(kill.PlayerVehicle, optionalMaxStringBytes)
		_, groundVehicle := groundModelNames[groundModelKey(rawVehicle)]
		entry.killerVehicle = displayModelName(rawVehicle)
		if groundVehicle {
			entry.killerVehicle = groundDisplayModelName(rawVehicle)
		}
		if killer != nil && entry.killerVehicle == "" {
			entry.killerVehicle = cleanOptionalText(trackName(killer, roster.slot, names), optionalMaxStringBytes)
		}
		usePID := entry.hasPID
		if killer == nil && !groundKillRosterIdentityUnique(roster, kill.KillerPid, kill.CurrentTime) {
			// Without a live exact vehicle, a reused slot cannot establish which
			// occupant owned a delayed weapon. Keep vehicle/weapon text only.
			usePID = false
		}
		if killer == nil && groundVehicle {
			usePID = false
		}
		// AI ground objects often report PID zero without a player slot. Do
		// not borrow the occupant of roster slot zero for such an object.
		if killer != nil && isGroundTrack(killer) {
			if _, ok := roster.optionalDamageTrackSlot(killer); !ok {
				usePID = false
			}
		}
		entry.killerPlayer = roster.optionalDamagePlayerAt(kill.KillerPid, usePID, kill.CurrentTime)
		if entry.killerPlayer != "" {
			entry.identitySource = "ReportPIDRosterAtTime"
			if historical {
				entry.identitySource = "ReportPIDHistoricalUID"
			}
		} else if killer != nil {
			entry.killerPlayer = roster.optionalDamageTrackPlayer(killer, kill.CurrentTime)
			entry.identitySource = "ExactUIDAtTime"
		}
		attacker := entry.killerVehicle
		if entry.killerPlayer != "" {
			if attacker != "" {
				attacker += " (" + entry.killerPlayer + ")"
			} else {
				attacker = entry.killerPlayer
			}
		}
		victimName := trackName(victim, roster.slot, names)
		if attacker == "" {
			entry.text = fmt.Sprintf("%s destroyed with %s (attacker unknown)", victimName, entry.weaponName)
			entry.identitySource = "WeaponOnly"
		} else {
			entry.text = fmt.Sprintf("%s destroyed by %s with %s", victimName, attacker, entry.weaponName)
		}
		if previous, exists := out[victim.key]; exists && previous != entry {
			delete(out, victim.key)
			conflicts[victim.key] = true
			continue
		}
		out[victim.key] = entry
	}
	return out
}

func groundKillRosterIdentityUnique(roster *optionalEventParser, pid, t uint32) bool {
	if roster == nil || pid >= 256 {
		return false
	}
	var identity optionalRosterEntry
	have := false
	for _, change := range roster.rosterLog[pid] {
		if change.t > t {
			break
		}
		if !change.entry.known {
			continue
		}
		if have && !optionalSamePlayer(identity, change.entry) {
			return false
		}
		identity, have = change.entry, true
	}
	return have
}

// A unique past aircraft identity can validate a reused roster slot when its
// pilot stayed the same from that sortie's birth through the delayed report.
// It supplies text only; the old aircraft is never referenced/recreated.
func historicalGroundKillAttacker(index killTimelineIndex, kill packetkill.KillEntry, roster *optionalEventParser) *track {
	if !kill.HasKillerPID || kill.KillerPid >= 256 || !kill.HasKillerUID || kill.KillerUid == 0x7ff {
		return nil
	}
	candidates := append([]*track(nil), index.airByUID[kill.KillerUid]...)
	if kill.ResolvedKiller != nil {
		candidates = index.byEntity[kill.ResolvedKiller]
	}
	var found *track
	for _, tr := range candidates {
		if killTrackContains(tr, kill.CurrentTime) {
			return nil // An unresolved active identity is ambiguous, not historical.
		}
		if !isKillAircraft(tr) || !tr.hasEnd || len(tr.samples) == 0 || tr.end >= kill.CurrentTime {
			continue
		}
		pid, ok := roster.optionalDamageTrackSlot(tr)
		if !ok || uint32(pid) != kill.KillerPid || !roster.optionalDamageTrackRosterStable(pid, tr, kill.CurrentTime) {
			continue
		}
		if kill.PlayerVehicle != "" {
			model, ok := packetecs2.GetObjectData[string](&tr.entity.Data, "unit__className")
			if !ok || displayModelName(model) != displayModelName(kill.PlayerVehicle) {
				continue
			}
		}
		if found != nil {
			return nil
		}
		found = tr
	}
	return found
}

func groundDestroyedEventLines(tr *track, id string, details map[trackKey]groundDestructionAttribution) []string {
	entry, ok := details[tr.key]
	if !isGroundTrack(tr) || !ok {
		return []string{"0,Event=Destroyed|" + id}
	}
	props := []string{"WT_KillSource=F058", "WT_KillWeapon=" + escapeACMIValue(entry.weapon),
		"WT_KillWeaponName=" + escapeACMIValue(entry.weaponName),
		"WT_KillerVehicle=" + escapeACMIValue(entry.killerVehicle), "WT_KillerPlayer=" + escapeACMIValue(entry.killerPlayer),
		"WT_KillerIdentitySource=" + entry.identitySource}
	if entry.hasUID {
		props = append(props, "WT_KillerUID="+strconv.FormatUint(uint64(entry.killerUID), 10))
	}
	if entry.hasPID {
		props = append(props, "WT_KillerPID="+strconv.FormatUint(uint64(entry.killerPID), 10))
	}
	text := strings.NewReplacer("|", "/", "\r", " ", "\n", " ").Replace(entry.text)
	return []string{id + "," + strings.Join(props, ","), "0,Event=Destroyed|" + id + "|" + escapeACMIValue(text)}
}

func killsWithoutGroundDestructionEvents(tracks []*track, kills []packetkill.KillEntry, details map[trackKey]groundDestructionAttribution) []packetkill.KillEntry {
	index := newKillTimelineIndex(tracks)
	out := make([]packetkill.KillEntry, 0, len(kills))
	for _, kill := range kills {
		if victim := index.exactVictim(kill); victim != nil {
			if _, exists := details[victim.key]; exists && kill.CurrentTime == victim.end {
				continue
			}
		}
		out = append(out, kill)
	}
	return out
}
