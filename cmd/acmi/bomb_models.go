package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Bomb and Rocket share the construction serializer. Reuse its bounded kill
// correlation, with a distinct class so a bomb report can never name a missile.
const bombNameCorrelationClass = uint32(math.MaxUint32)

//go:embed data/bomb_models.json
var embeddedBombModelsJSON []byte

func loadBombModels(spec string) (*missileModelTable, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "", "off":
		return nil, nil
	case "embedded":
		return parseBombModelsJSON(embeddedBombModelsJSON)
	default:
		data, err := readMissileModelsFile(spec)
		if err != nil {
			return nil, err
		}
		return parseBombModelsJSON(data)
	}
}

func parseBombModelsJSON(data []byte) (*missileModelTable, error) {
	if len(data) == 0 || len(data) > missileModelTableMaxFileBytes {
		return nil, fmt.Errorf("bomb model table must contain 1..%d bytes", missileModelTableMaxFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	decoder.DisallowUnknownFields()
	var table missileModelTable
	if err := decoder.Decode(&table); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("bomb model table has trailing data")
	}
	if table.Schema != 1 || table.Aliases == nil || table.GeneratedFrom == nil || table.Replays == nil || len(table.Replays) != 0 {
		return nil, fmt.Errorf("bomb model table requires schema 1, generated_from, aliases and empty replays")
	}
	for _, source := range table.GeneratedFrom {
		if err := validateMissileModelText("bomb generated_from", source, 512); err != nil {
			return nil, err
		}
	}
	for key, entry := range table.Aliases {
		if key != strings.ToLower(key) || entry.Role != "" {
			return nil, fmt.Errorf("bomb alias %q must be lowercase without a missile role", key)
		}
		if err := validateMissileModelInternalName("bomb alias", key); err != nil {
			return nil, err
		}
		if err := validateMissileModelAliasEntry("bomb alias "+key, entry); err != nil {
			return nil, err
		}
	}
	// Unlike legacy AAM names, _default is not stripped: bomb assets with
	// that suffix can have their own localized name or variant identity.
	return &table, nil
}

func bombAlias(table *missileModelTable, key string) (missileModelAliasEntry, bool) {
	if table == nil {
		return missileModelAliasEntry{}, false
	}
	entry, ok := table.Aliases[strings.ToLower(strings.TrimSpace(key))]
	return entry, ok
}

func applyBombModelNames(tracks []*track, kills *missileNameKillParser, table *missileModelTable) int {
	if table == nil {
		return 0
	}
	aliases := make(map[string]missileWeaponAlias, len(table.Aliases))
	for key, entry := range table.Aliases {
		aliases[strings.ToLower(key)] = missileWeaponAlias{model: entry.Model, internal: entry.InternalName, role: "Bomb"}
	}
	var descriptors []*missileNameTrack
	for _, tr := range tracks {
		if tr == nil || !tr.key.bomb || tr.projectileSource == nil {
			continue
		}
		source := tr.projectileSource
		if !isBombProjectile(source) {
			continue
		}
		tr.bombModel = &missileModelInfo{Source: "Unknown", Confidence: "Unknown", LeadingULEB: source.LeadingULEB, HasLeadingULEB: true}
		d := &missileNameTrack{track: tr, source: source, template: source.Template,
			parentUID: source.ParentUID, hasParent: source.HasParent, trigger: bombNameCorrelationClass,
			endTime: source.EndTime, hasEnd: source.HasEnd}
		// Use the exact raw serializer token, never a guessed global weapon ID.
		// A synthetic replay-end marker is not an observed bomb destruction.
		if source.LeadingULEB > 0 && source.LeadingULEB <= math.MaxUint32 {
			d.weaponID, d.hasWeaponID = uint32(source.LeadingULEB), true
		}
		if tr.hasEnd && source.EndTime > tr.end {
			d.hasEnd = false
		}
		descriptors = append(descriptors, d)
	}
	var reports []missileNameKill
	if kills != nil {
		reports = kills.Kills
	}
	evidence, groups := correlateMissileNameKillsWithAliases(reports, descriptors, aliases)
	named := 0
	for _, d := range descriptors {
		info := d.track.bombModel
		var nearby []*missileNameEvidence
		for _, ev := range evidence {
			if d.hasParent && d.hasEnd && uint64(ev.kill.KillerUID) == d.parentUID && missileNameTimeDelta(ev.kill.Time, d.endTime) <= missileNameMatchWindowMS {
				nearby = append(nearby, ev)
			}
		}
		if d.hasWeaponID {
			key := missileNamePropagationKey{weaponID: d.weaponID, template: d.template, trigger: bombNameCorrelationClass}
			group := groups[key]
			conflict := false
			for _, ev := range nearby {
				if ev.ambiguous || !ev.hasGroup || ev.groupKey != key {
					conflict = true
				}
			}
			if group != nil && (group.conflicting || len(group.internals) > 1) {
				conflict = true
				info.ModelConflict = "ConflictingBombWeaponReports"
				for internal := range group.internals {
					if entry, ok := bombAlias(table, internal); ok {
						info.ModelCandidates = missileModelCandidates(append(info.ModelCandidates, entry.Model)...)
					}
				}
			}
			if group != nil && !conflict && group.model != "" {
				info.Model, info.Source, info.Confidence = group.model, "ReplayWeaponTokenFromKillReport", "Inferred"
				if len(group.internals) == 1 {
					for internal := range group.internals {
						info.InternalName = internal
					}
				}
				if len(nearby) == 1 && len(nearby[0].candidates) == 1 && nearby[0].candidates[0] == d {
					info.InternalName, info.Source, info.Confidence = nearby[0].internal, "KillReportMatched", "Matched"
				}
			}
		} else if len(nearby) == 1 && !nearby[0].ambiguous && nearby[0].directTrack == d {
			info.Model, info.InternalName = nearby[0].model, nearby[0].internal
			info.Source, info.Confidence = "KillReportMatched", "Matched"
		}
		candidates, complete := bombLoadoutCandidates(d, table)
		if info.Model == "" && info.ModelConflict == "" {
			info.ModelCandidates = missileModelCandidates(append(info.ModelCandidates, candidates...)...)
			if complete && len(candidates) == 1 {
				for _, ammo := range d.source.OwnerWeapons {
					if ammo.Count == 0 {
						continue
					}
					for _, key := range []string{ammo.Launcher, ammo.Bullet} {
						if entry, ok := bombAlias(table, key); ok && entry.Model == candidates[0] {
							info.Model, info.InternalName = entry.Model, entry.InternalName
							info.Source, info.Confidence = "ReplayOwnerLoadoutMatched", entry.Confidence
						}
					}
				}
			}
		}
		if info.Model != "" {
			d.track.name = info.Model
			named++
		}
	}
	return named
}

func bombLoadoutCandidates(d *missileNameTrack, table *missileModelTable) ([]string, bool) {
	var names []string
	identities := map[string]bool{}
	complete := len(d.source.OwnerWeapons) > 0
	for _, ammo := range d.source.OwnerWeapons {
		if ammo.Count == 0 {
			continue
		}
		matched := false
		for _, key := range []string{ammo.Launcher, ammo.Bullet} {
			if entry, ok := bombAlias(table, key); ok {
				names = missileModelCandidates(append(names, entry.Model)...)
				identities[entry.InternalName] = true
				matched = true
			}
		}
		if matched {
			continue
		}
		launcher := strings.ToLower(strings.ReplaceAll(ammo.Launcher, "\\", "/"))
		base := launcher[strings.LastIndex(launcher, "/")+1:]
		if strings.HasPrefix(base, "countermeasure_") || strings.HasPrefix(base, "countermeasures_") {
			continue
		}
		other := false
		for _, directory := range []string{"/cannons/", "/machineguns/", "/rocketguns/", "/torpedoguns/"} {
			other = other || strings.Contains("/"+launcher, directory)
		}
		if !other {
			complete = false
		}
	}
	return names, complete && len(identities) == 1
}

func bombModelProperties(tr *track) []string {
	if tr == nil || !tr.key.bomb || tr.bombModel == nil {
		return nil
	}
	info := tr.bombModel
	props := []string{"WT_BombModel=" + escapeACMIValue(info.Model),
		"WT_BombInternalName=" + escapeACMIValue(info.InternalName),
		"WT_BombModelSource=" + info.Source, "WT_BombModelConfidence=" + info.Confidence}
	if info.HasLeadingULEB {
		props = append(props, "WT_BombLeadingULEB="+strconv.FormatUint(info.LeadingULEB, 10))
	}
	if len(info.ModelCandidates) > 0 {
		props = append(props, "WT_BombModelCandidates="+escapeACMIValue(strings.Join(info.ModelCandidates, " / ")))
	}
	if info.ModelConflict != "" {
		props = append(props, "WT_BombModelConflict="+escapeACMIValue(info.ModelConflict))
	}
	return props
}
