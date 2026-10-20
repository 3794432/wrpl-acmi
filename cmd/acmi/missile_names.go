package main

import (
	"math"
	"strconv"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/idfieldserializer"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

const (
	missileNameKillMessageID       = 0xF058
	missileNameMatchWindowMS       = uint32(250)
	missileNameMaxPacketBytes      = 1 << 16
	missileNameMaxFieldBits        = 1 << 20
	missileNameMaxKillRecords      = 1 << 16
	missileNameLeadingBase         = uint64(129)
	missileNameArmamentTriggerAAM  = uint32(8)
	missileNameArmamentTriggerAGM  = uint32(7)
	missileNameUnknownKillerUID    = uint16(0x7ff)
	missileNameUnknownKillFieldSet = uint32(1<<4 | 1<<10)
)

type missileModelInfo struct {
	WeaponID     uint32
	HasWeaponID  bool
	Model        string
	InternalName string
	Source       string
	Confidence   string
}

type missileNameKill struct {
	Time      uint32
	KillerUID uint16
	Weapon    string
}

// missileNameKillParser deliberately does not depend on ECS resolution. A
// malformed or incomplete report is skipped without preventing the remaining
// replay from being exported.
type missileNameKillParser struct {
	Kills   []missileNameKill
	Skipped uint64
}

func newMissileNameKillParser() *missileNameKillParser {
	return &missileNameKillParser{}
}

func (p *missileNameKillParser) Name() string { return "missile_name_kills" }

func (p *missileNameKillParser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	return map[byte][][]packet.ParsingCondition{
		4: {{
			packet.NewParsingCondition(0, 0x02),
			packet.NewParsingCondition(1, 0x58),
			packet.NewParsingCondition(2, 0x58),
			packet.NewParsingCondition(3, 0xf0),
		}},
	}
}

func (p *missileNameKillParser) Parse(pk *packet.Packet) (any, error) {
	if p == nil {
		return nil, nil
	}
	kill, ok := parseMissileNameKill(pk)
	if !ok {
		p.Skipped++
		return nil, nil
	}
	// Retaining an unbounded number of reports from a hostile replay would
	// make the export itself a memory amplification path.
	if len(p.Kills) >= missileNameMaxKillRecords {
		p.Skipped++
		return nil, nil
	}
	p.Kills = append(p.Kills, kill)
	return kill, nil
}

func parseMissileNameKill(pk *packet.Packet) (missileNameKill, bool) {
	var result missileNameKill
	if pk == nil || pk.PacketType != 4 || len(pk.PacketPayload) < 8 || len(pk.PacketPayload) > missileNameMaxPacketBytes {
		return result, false
	}
	payload := pk.PacketPayload
	if payload[0] != 0x02 || payload[1] != 0x58 || payload[2] != 0x58 || payload[3] != 0xf0 {
		return result, false
	}

	r := danet.NewBitReader(payload[4:])
	offset, err := r.ReadU16LE()
	if err != nil {
		return result, false
	}
	fields, err := r.ReadCompressed()
	if err != nil || fields == 0 || fields > math.MaxUint32 {
		return result, false
	}
	fieldMask := uint32(fields)
	if fieldMask&missileNameUnknownKillFieldSet != missileNameUnknownKillFieldSet {
		// Neither a default UID nor a default weapon is evidence. Both fields
		// have to be present on the wire.
		return result, false
	}

	bodyStart := r.BitOffset
	sizesStart := int(offset) * 8
	if sizesStart < bodyStart || sizesStart > len(r.Data)*8 {
		return result, false
	}
	r.BitOffset = sizesStart
	var sizes [32]uint32
	for field := uint8(0); field < 32; field++ {
		if fieldMask>>field&1 == 0 {
			continue
		}
		size, err := idfieldserializer.ReadSize(r)
		if err != nil || size > missileNameMaxFieldBits {
			return result, false
		}
		sizes[field] = size
	}
	if sizes[4] < 16 || sizes[10] < 8 {
		return result, false
	}

	r.BitOffset = bodyStart
	for field := uint8(0); field < 32 && fieldMask != 0; field++ {
		if fieldMask>>field&1 == 0 {
			continue
		}
		size := sizes[field]
		fieldStart := r.BitOffset
		remaining := len(r.Data)*8 - fieldStart
		if fieldStart < 0 || remaining < 0 || size > uint32(remaining) {
			return result, false
		}
		fieldEnd := fieldStart + int(size)
		if fieldEnd > sizesStart {
			return result, false
		}
		switch field {
		case 4:
			raw, err := r.ReadU16LE()
			if err != nil {
				return result, false
			}
			result.KillerUID = raw & 0x7ff
			if result.KillerUID == missileNameUnknownKillerUID {
				return result, false
			}
		case 10:
			weapon, ok := readMissileNameKillString(r, size)
			if !ok {
				return result, false
			}
			result.Weapon = weapon
		}
		r.BitOffset = fieldEnd
	}
	result.Time = pk.CurrentTime
	return result, result.Weapon != ""
}

func readMissileNameKillString(r *danet.BitReader, fieldBits uint32) (string, bool) {
	if fieldBits < 8 {
		return "", false
	}
	length, err := r.ReadByte()
	if err != nil || length == 0 || uint32(length)*8+8 > fieldBits {
		return "", false
	}
	raw, err := r.ReadBytes(int(length))
	if err != nil {
		return "", false
	}
	weapon := strings.TrimSpace(string(raw))
	return weapon, weapon != ""
}

type missileWeaponAlias struct {
	model    string
	internal string
}

// Internal names are accepted only through this explicit list. In particular,
// no prefix or substring matching is used to turn an unknown weapon into a
// guessed model.
var missileWeaponAliases = map[string]missileWeaponAlias{
	"cn_pl12":       {model: "PL-12", internal: "cn_pl12"},
	"us_aim_120a":   {model: "AIM-120A", internal: "us_aim_120a"},
	"us_aim_120c_5": {model: "AIM-120C-5", internal: "us_aim_120c_5"},
	"su_rvv_ae":     {model: "R-77 (RVV-AE)", internal: "su_rvv_ae"},
	"jp_aam4":       {model: "AAM-4", internal: "jp_aam4"},
	"swd_rb99":      {model: "RB 99", internal: "swd_rb99"},
}

func missileWeaponAliasFor(name string) (missileWeaponAlias, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	for strings.HasSuffix(key, "_default") {
		key = strings.TrimSuffix(key, "_default")
	}
	alias, ok := missileWeaponAliases[key]
	return alias, ok
}

func missileWeaponIDFromLeading(leading uint64) (uint32, bool) {
	if leading < missileNameLeadingBase {
		return 0, false
	}
	delta := leading - missileNameLeadingBase
	if delta > math.MaxUint32 {
		return 0, false
	}
	return uint32(delta), true
}

type missileNameTrack struct {
	track       *track
	source      *projectiles.Projectile
	template    string
	parentUID   uint64
	hasParent   bool
	endTime     uint32
	hasEnd      bool
	weaponID    uint32
	hasWeaponID bool
}

type missileNamePropagationKey struct {
	weaponID uint32
	template string
}

type missileNameEvidence struct {
	kill        missileNameKill
	model       string
	internal    string
	candidates  []*missileNameTrack
	groupKey    missileNamePropagationKey
	hasGroup    bool
	directTrack *missileNameTrack
	ambiguous   bool
}

type missileNameGroupEvidence struct {
	model       string
	conflicting bool
	internals   map[string]bool
}

func applyMissileModelNames(tracks []*track, kills *missileNameKillParser) {
	infos := make(map[*missileNameTrack]*missileModelInfo)
	descriptors := make([]*missileNameTrack, 0)
	for _, tr := range tracks {
		if tr == nil || !tr.key.missile {
			continue
		}
		tr.missileModel = nil
		if tr.projectileSource != nil {
			// The source remains immutable: CSV creation data and every flight
			// field continue to come from its original Projectile record.
			tr.name = tr.projectileSource.Name
		} else {
			tr.name = ""
		}
		d := &missileNameTrack{track: tr, source: tr.projectileSource}
		if d.source != nil {
			d.template = d.source.Template
			d.parentUID, d.hasParent = d.source.ParentUID, d.source.HasParent
			d.endTime, d.hasEnd = tr.end, tr.hasEnd
			if !d.hasEnd && d.source.HasEnd {
				d.endTime, d.hasEnd = d.source.EndTime, true
			}
			d.weaponID, d.hasWeaponID = missileWeaponIDFromLeading(d.source.LeadingULEB)
		}
		if !d.hasParent && tr.parent != nil {
			d.parentUID, d.hasParent = tr.parent.key.id, true
		}
		infos[d] = &missileModelInfo{
			WeaponID: d.weaponID, HasWeaponID: d.hasWeaponID,
			Source: "Unknown", Confidence: "Unknown",
		}
		descriptors = append(descriptors, d)
	}

	if kills != nil && len(kills.Kills) > 0 && len(descriptors) > 0 {
		evidence, groups := correlateMissileNameKills(kills.Kills, descriptors)
		byParent := make(map[uint64][]*missileNameEvidence)
		for _, ev := range evidence {
			if len(ev.candidates) == 0 {
				continue
			}
			byParent[ev.candidates[0].parentUID] = append(byParent[ev.candidates[0].parentUID], ev)
		}

		for _, d := range descriptors {
			var nearby []*missileNameEvidence
			if d.hasParent && d.hasEnd {
				for _, ev := range byParent[d.parentUID] {
					if missileNameTimeDelta(d.endTime, ev.kill.Time) <= missileNameMatchWindowMS {
						nearby = append(nearby, ev)
					}
				}
			}
			info := infos[d]
			if d.hasWeaponID {
				key := missileNamePropagationKey{weaponID: d.weaponID, template: d.template}
				group := groups[key]
				conflict := group == nil || group.conflicting || group.model == ""
				for _, ev := range nearby {
					if ev.ambiguous || !ev.hasGroup || ev.groupKey != key {
						conflict = true
						break
					}
				}
				if conflict {
					continue
				}
				info.Model = group.model
				info.Source = "ReplayWeaponIDFromKillReport"
				info.Confidence = "Inferred"
				if len(group.internals) == 1 {
					for internal := range group.internals {
						info.InternalName = internal
					}
				}
				// A kill-report match names one exact track only when both sides
				// have exactly one candidate in the complete 250 ms window.
				if len(nearby) == 1 && len(nearby[0].candidates) == 1 && nearby[0].candidates[0] == d {
					info.InternalName = nearby[0].internal
					info.Source = "KillReportMatched"
					info.Confidence = "Matched"
				}
				d.track.missileModel = info
				d.track.name = info.Model
				continue
			}

			// Without a usable LeadingULEB there is no group that can safely
			// carry an inference. Only an unambiguous one-to-one report is used.
			if !d.hasParent || !d.hasEnd || len(nearby) != 1 || nearby[0].ambiguous || nearby[0].hasGroup || nearby[0].directTrack != d {
				continue
			}
			info.Model = nearby[0].model
			info.InternalName = nearby[0].internal
			info.Source = "KillReportMatched"
			info.Confidence = "Matched"
			d.track.missileModel = info
			d.track.name = info.Model
		}
	}

	for _, d := range descriptors {
		if d.track.missileModel == nil {
			d.track.missileModel = infos[d]
		}
		if d.track.missileModel.Model == "" {
			d.track.name = missileNameFallback(d.track)
		}
	}
}

func correlateMissileNameKills(kills []missileNameKill, tracks []*missileNameTrack) ([]*missileNameEvidence, map[missileNamePropagationKey]*missileNameGroupEvidence) {
	groups := make(map[missileNamePropagationKey]*missileNameGroupEvidence)
	result := make([]*missileNameEvidence, 0)
	tracksByParent := make(map[uint64][]*missileNameTrack)
	for _, d := range tracks {
		if d.hasParent {
			tracksByParent[d.parentUID] = append(tracksByParent[d.parentUID], d)
		}
	}
	type killKey struct {
		time     uint32
		killer   uint16
		internal string
	}
	seen := make(map[killKey]bool)

	for _, kill := range kills {
		alias, ok := missileWeaponAliasFor(kill.Weapon)
		if !ok {
			continue
		}
		key := killKey{time: kill.Time, killer: kill.KillerUID, internal: alias.internal}
		if seen[key] {
			continue
		}
		seen[key] = true

		ev := &missileNameEvidence{
			kill: kill, model: alias.model, internal: alias.internal,
		}
		leadingGroups := make(map[missileNamePropagationKey]bool)
		withoutLeading := 0
		for _, d := range tracksByParent[uint64(kill.KillerUID)] {
			if !d.hasEnd {
				continue
			}
			if missileNameTimeDelta(d.endTime, kill.Time) > missileNameMatchWindowMS {
				continue
			}
			ev.candidates = append(ev.candidates, d)
			if d.hasWeaponID {
				leadingGroups[missileNamePropagationKey{weaponID: d.weaponID, template: d.template}] = true
			} else {
				withoutLeading++
			}
		}
		if len(ev.candidates) == 0 {
			continue
		}

		switch len(leadingGroups) {
		case 0:
			if len(ev.candidates) == 1 {
				ev.directTrack = ev.candidates[0]
			} else {
				ev.ambiguous = true
			}
		case 1:
			for key := range leadingGroups {
				ev.groupKey = key
			}
			if withoutLeading != 0 {
				ev.ambiguous = true
			} else {
				ev.hasGroup = true
			}
		default:
			// Several missile identities end close enough to this report.
			// Picking the nearest one would turn timing overlap into a guess.
			ev.ambiguous = true
		}
		result = append(result, ev)
	}

	for _, ev := range result {
		if ev.ambiguous || !ev.hasGroup {
			continue
		}
		group := groups[ev.groupKey]
		if group == nil {
			group = &missileNameGroupEvidence{model: ev.model, internals: make(map[string]bool)}
			groups[ev.groupKey] = group
		} else if group.model != ev.model {
			group.conflicting = true
		}
		group.internals[ev.internal] = true
	}
	return result, groups
}

func missileNameTimeDelta(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

func missileNameFallback(tr *track) string {
	if tr == nil || tr.projectileSource == nil {
		return ""
	}
	trigger := tr.projectileSource.WeaponRef >> 16
	if trigger == missileNameArmamentTriggerAAM {
		if strings.Contains(strings.ToLower(tr.projectileSource.Template), "arh_shell") {
			return "AAM (ARH)"
		}
		return "AAM (unknown)"
	}
	if trigger == missileNameArmamentTriggerAGM {
		return "AGM (unknown)"
	}
	return tr.name
}

func missileModelProperties(tr *track) []string {
	if tr == nil || !tr.key.missile || tr.missileModel == nil {
		return nil
	}
	model := tr.missileModel
	props := make([]string, 0, 5)
	if model.HasWeaponID {
		props = append(props, "WT_WeaponID="+strconv.FormatUint(uint64(model.WeaponID), 10))
	}
	props = append(props,
		"WT_MissileModel="+model.Model,
		"WT_MissileInternalName="+model.InternalName,
		"WT_MissileModelSource="+missileNameSource(model.Source),
		"WT_MissileModelConfidence="+missileNameConfidence(model.Confidence),
	)
	return props
}

func missileNameSource(source string) string {
	switch source {
	case "KillReportMatched", "ReplayWeaponIDFromKillReport":
		return source
	default:
		return "Unknown"
	}
}

func missileNameConfidence(confidence string) string {
	switch confidence {
	case "Matched", "Inferred":
		return confidence
	default:
		return "Unknown"
	}
}

var _ packet.PacketParser = (*missileNameKillParser)(nil)
