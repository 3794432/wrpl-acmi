package main

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

//go:embed data/sam_launchers.json
var samLaunchersJSON []byte

type samAmmunition struct {
	BulletName string `json:"bullet_name"`
	WeaponPath string `json:"weapon_path"`
	Guidance   string `json:"guidance"`
}

type samLauncherProfile struct {
	Missiles           []samAmmunition `json:"missiles"`
	CandidatesComplete bool            `json:"candidates_complete"`
	CandidateSources   []string        `json:"candidate_sources,omitempty"`
}

type samLauncherCatalog struct {
	Schema       int                           `json:"schema"`
	SourceCommit string                        `json:"source_commit"`
	Units        map[string]samLauncherProfile `json:"units"`
}

var samLaunchers = func() samLauncherCatalog {
	var table samLauncherCatalog
	if err := json.Unmarshal(samLaunchersJSON, &table); err != nil {
		panic("invalid SAM launcher catalogue: " + err.Error())
	}
	if table.Schema != 1 || table.Units == nil || table.SourceCommit == "" {
		panic("unsupported SAM launcher catalogue")
	}
	units := make(map[string]samLauncherProfile, len(table.Units))
	for model, profile := range table.Units {
		key := strings.ToLower(groundModelKey(model))
		if _, exists := units[key]; exists {
			panic("duplicate SAM launcher model: " + key)
		}
		units[key] = profile
	}
	table.Units = units
	return table
}()

// The exact replay owner identifies the launcher. The static catalogue proves
// that it carries SAM ammunition; it does not prove which of several missiles
// was loaded. Retain every candidate without assigning a guessed missile model.
type surfaceMissileLaunch struct {
	owner   groundmeta.Info
	profile samLauncherProfile
}

func surfaceMissileLaunches(metadata *projectiles.MetadataParser, ground *groundmeta.Parser) map[*projectiles.Projectile]*surfaceMissileLaunch {
	result := map[*projectiles.Projectile]*surfaceMissileLaunch{}
	if metadata == nil || ground == nil {
		return result
	}
	byOwner := map[uint64][]groundmeta.Info{}
	for _, info := range ground.All {
		if info.HasConstructionTime {
			byOwner[info.EID] = append(byOwner[info.EID], info)
		}
	}
	for _, history := range metadata.History {
		for _, source := range history {
			if source == nil || isBombProjectile(source) || !isSurfaceWeaponTrigger(source.WeaponRef>>16) {
				continue
			}
			if end, ok := ground.Ends[source.OwnerEID]; ok && source.SpawnSample.Time > end {
				continue
			}
			var owner *groundmeta.Info
			conflict := false
			for _, info := range byOwner[source.OwnerEID] {
				if info.ConstructionTime > source.SpawnSample.Time {
					continue
				}
				if owner == nil || info.ConstructionTime > owner.ConstructionTime {
					copy := info
					owner = &copy
					conflict = false
				} else if info.ConstructionTime == owner.ConstructionTime &&
					(info.UID != owner.UID || !groundmeta.SameModelIdentity(info.RawModel, owner.RawModel) ||
						(info.Team != nil && owner.Team != nil && *info.Team != *owner.Team)) {
					conflict = true
				}
			}
			if owner == nil || conflict || owner.RawModel == "" ||
				(owner.ClassModel != "" && owner.StorageModel != "" && !groundmeta.SameModelIdentity(owner.ClassModel, owner.StorageModel)) ||
				(source.HasParent && source.ParentUID != uint64(owner.UID)) {
				continue
			}
			profile, ok := samLaunchers.Units[strings.ToLower(groundModelKey(owner.RawModel))]
			if !ok || len(profile.Missiles) == 0 {
				continue
			}
			result[source] = &surfaceMissileLaunch{owner: *owner, profile: profile}
		}
	}
	return result
}

// These ground weapon groups are observed in Rocket constructions. Group 19
// is shared with MLRS/ballistic rockets, so the trigger alone is insufficient.
// Groups for aircraft stores, smoke and countermeasures are deliberately absent.
func isSurfaceWeaponTrigger(trigger uint32) bool {
	return trigger >= 19 && trigger <= 21
}

func applySurfaceMissileOwnership(tracks []*track) {
	for _, tr := range tracks {
		if tr == nil || tr.surfaceLaunch == nil || tr.projectileSource == nil {
			continue
		}
		owner := tr.surfaceLaunch.owner
		launchTime := tr.projectileSource.SpawnSample.Time
		tr.parent = nil
		var parent *track
		ambiguous := false
		for _, candidate := range tracks {
			uid, ok := groundTrackUID(candidate)
			if !ok || uid != uint64(owner.UID) || candidate.entity == nil || candidate.entity != owner.Entity ||
				(candidate.hasEnd && launchTime > candidate.end) {
				continue
			}
			if parent != nil {
				ambiguous = true
				break
			}
			parent = candidate
		}
		if !ambiguous {
			tr.parent = parent
		}
	}
}

func (launch *surfaceMissileLaunch) coalition() string {
	if launch.owner.Team != nil && launch.owner.TeamSource != "" {
		switch *launch.owner.Team {
		case 1:
			return "Blue"
		case 2:
			return "Red"
		}
	}
	return "Neutral"
}

func surfaceMissileProperties(tr *track) []string {
	if tr == nil || tr.surfaceLaunch == nil {
		return nil
	}
	launch := tr.surfaceLaunch
	props := []string{
		"WT_WeaponRole=SAM",
		"WT_LauncherEntityID=" + strconv.FormatUint(launch.owner.EID, 16),
		"WT_LauncherUnitUID=" + strconv.FormatUint(uint64(launch.owner.UID), 10),
		"WT_LauncherModel=" + escapeACMIValue(launch.owner.RawModel),
		"WT_LauncherIdentitySource=ReplayOwnerEID",
		"WT_SAMAmmoSource=WT-datamine@" + samLaunchers.SourceCommit,
	}
	var candidates []string
	for _, ammo := range launch.profile.Missiles {
		candidates = append(candidates, ammo.BulletName)
	}
	if len(candidates) > 0 {
		props = append(props, "WT_SAMAmmoCandidates="+escapeACMIValue(strings.Join(candidates, " / ")))
	}
	props = append(props, surfaceMissileModelProperties(tr)...)
	return props
}
