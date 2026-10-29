package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed data/sam_variant_preferences.json
var samVariantPreferencesJSON []byte

const samVariantFallbackSource = "UserLatestSAMVariant"

var samVariantFamilies = func() map[string][]string {
	var table struct {
		Schema   int                 `json:"schema"`
		Policy   string              `json:"policy"`
		Families map[string][]string `json:"families"`
	}
	if err := json.Unmarshal(samVariantPreferencesJSON, &table); err != nil {
		panic("invalid SAM variant preferences: " + err.Error())
	}
	if table.Schema != 1 || table.Policy != "UserPreferenceNotReplayEvidence" || table.Families == nil {
		panic("unsupported SAM variant preferences")
	}
	seen := map[string]bool{}
	for _, preference := range table.Families {
		for _, bullet := range preference {
			if _, ok := samMissileNames.Names[bullet]; !ok || seen[bullet] {
				panic("unknown or duplicate SAM preference: " + bullet)
			}
			seen[bullet] = true
		}
	}
	return table.Families
}()

func parseSAMModelFallback(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "off" && value != "latest" {
		return "", fmt.Errorf("invalid -sam-model-fallback %q: use latest or off", value)
	}
	return value, nil
}

// This is an explicit display assumption. Only listed ammunition candidates
// can be selected; a policy order cannot invent a weapon the launcher lacks.
// Actual kill-report/loadout identities always take priority over this stage.
func applySurfaceMissileFallbacks(tracks []*track, names map[string]samMissileName) int {
	adopted := 0
	for _, tr := range tracks {
		if tr == nil || !tr.key.missile || tr.surfaceLaunch == nil || tr.missileModel == nil || tr.missileModel.Model != "" || tr.missileModel.ModelConflict != "" {
			continue
		}
		bullet := preferredSAMVariant(tr.surfaceLaunch.profile, tr.projectileSource != nil && tr.projectileSource.Template == "rocket+arh_shell")
		name, ok := names[bullet]
		if !ok || name.Model == "" || name.Source == "" {
			continue
		}
		tr.missileModel.Model = name.Model
		tr.missileModel.InternalName = bullet
		tr.missileModel.Source = samVariantFallbackSource
		tr.missileModel.Confidence = "Assumed"
		tr.missileModel.ModelTableApplied = true
		tr.name = name.Model
		adopted++
	}
	return adopted
}

func preferredSAMVariant(profile samLauncherProfile, requiresRadar bool) string {
	preferRadar := false
	for _, ammo := range profile.Missiles {
		preferRadar = preferRadar || ammo.Guidance == "radar"
	}
	if requiresRadar && !preferRadar {
		return ""
	}
	available := map[string]bool{}
	for _, ammo := range profile.Missiles {
		if !preferRadar || ammo.Guidance == "radar" {
			available[ammo.BulletName] = true
		}
	}
	if len(available) == 1 {
		for bullet := range available {
			return bullet
		}
	}
	for _, preference := range samVariantFamilies {
		matches := 0
		selected := ""
		for _, bullet := range preference {
			if available[bullet] {
				matches++
				if selected == "" {
					selected = bullet
				}
			}
		}
		if matches > 0 && matches == len(available) {
			return selected
		}
	}
	return ""
}
