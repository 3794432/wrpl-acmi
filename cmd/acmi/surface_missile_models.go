package main

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

//go:embed data/sam_missile_names.json
var samMissileNamesJSON []byte

type samMissileName struct {
	Model  string `json:"model"`
	Source string `json:"source"`
}

type samMissileNameCatalog struct {
	Schema       int                       `json:"schema"`
	SourceCommit string                    `json:"source_commit"`
	Names        map[string]samMissileName `json:"names"`
}

var samMissileNames = func() samMissileNameCatalog {
	var table samMissileNameCatalog
	if err := json.Unmarshal(samMissileNamesJSON, &table); err != nil {
		panic("invalid SAM missile names: " + err.Error())
	}
	if table.Schema != 1 || table.Names == nil || table.SourceCommit != samLaunchers.SourceCommit {
		panic("SAM missile names and launcher catalogue must use the same source revision")
	}
	for bullet, entry := range table.Names {
		if strings.TrimSpace(bullet) == "" || strings.TrimSpace(entry.Model) == "" || strings.TrimSpace(entry.Source) == "" {
			panic("SAM missile name has no source or identity")
		}
	}
	return table
}()

// A unique complete launcher configuration supplies a static inference, not
// a replay-decoded ammunition selection. Multi-ammo and incomplete profiles
// keep their candidates. Kill-report or replay weapon identities take priority.
func applySurfaceMissileModels(tracks []*track, names map[string]samMissileName) int {
	adopted := 0
	for _, tr := range tracks {
		if tr == nil || !tr.key.missile || tr.surfaceLaunch == nil {
			continue
		}
		profile := tr.surfaceLaunch.profile
		byBullet := map[string]samMissileName{}
		allNamed := true
		compatible := true
		var candidates []string
		for _, ammo := range profile.Missiles {
			name, ok := names[ammo.BulletName]
			if !ok || name.Model == "" || name.Source == "" {
				allNamed = false
				candidates = append(candidates, ammo.BulletName)
			} else {
				candidates = append(candidates, name.Model)
			}
			byBullet[ammo.BulletName] = name
			if tr.projectileSource != nil && tr.projectileSource.Template == "rocket+arh_shell" && ammo.Guidance != "radar" {
				compatible = false
			}
		}
		candidates = missileModelCandidates(candidates...)
		sort.Strings(candidates)
		if tr.missileModel == nil {
			tr.missileModel = &missileModelInfo{Source: "Unknown", Confidence: "Unknown"}
		}
		info := tr.missileModel
		info.ModelTableApplied = true
		// Keep earlier identities immutable, including confidence and source.
		if info.Model != "" {
			if len(candidates) > 1 || (len(candidates) == 1 && candidates[0] != info.Model) {
				info.ModelCandidates = missileModelCandidates(append([]string{info.Model}, candidates...)...)
			}
			continue
		}
		info.ModelCandidates = missileModelCandidates(append(info.ModelCandidates, candidates...)...)
		if !profile.CandidatesComplete || len(byBullet) != 1 || !allNamed || !compatible || len(info.ModelCandidates) != 1 || info.ModelConflict != "" {
			continue
		}
		for bullet, name := range byBullet {
			info.Model = name.Model
			info.InternalName = bullet
			info.Source = "LauncherConfigurationSingleAmmo"
			info.Confidence = "Inferred"
			info.ModelCandidates = nil
			tr.name = name.Model
			adopted++
		}
	}
	return adopted
}

func surfaceMissileModelProperties(tr *track) []string {
	if tr == nil || tr.surfaceLaunch == nil {
		return nil
	}
	props := []string{"WT_SAMCandidatesComplete=" + strconv.FormatBool(tr.surfaceLaunch.profile.CandidatesComplete)}
	if tr.missileModel != nil && tr.missileModel.Source == "LauncherConfigurationSingleAmmo" {
		props = append(props, "WT_SAMModelEvidence=StaticLauncherConfiguration")
		if name, ok := samMissileNames.Names[tr.missileModel.InternalName]; ok {
			props = append(props, "WT_SAMModelNameSource="+escapeACMIValue(name.Source))
		}
	}
	if tr.missileModel != nil && tr.missileModel.Source == samVariantFallbackSource {
		props = append(props, "WT_SAMModelEvidence=UserPreferenceFallback", "WT_SAMModelAssumed=1", "WT_SAMModelFallbackRule=PreferRadarThenLatestReviewedVariant")
		if name, ok := samMissileNames.Names[tr.missileModel.InternalName]; ok {
			props = append(props, "WT_SAMModelNameSource="+escapeACMIValue(name.Source))
		}
	}
	if len(tr.surfaceLaunch.profile.CandidateSources) > 0 {
		props = append(props, "WT_SAMConfigurationSources="+escapeACMIValue(strings.Join(tr.surfaceLaunch.profile.CandidateSources, " / ")))
	}
	return props
}
