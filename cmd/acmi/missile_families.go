package main

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

//go:embed data/missile_carriers.json
var embeddedMissileCarriersJSON []byte

type missileCarrierProfile struct {
	Weapons         []string            `json:"weapons"`
	Complete        map[string]bool     `json:"complete"`
	Unresolved      map[string][]string `json:"unresolved"`
	Source          string              `json:"source"`
	Scope           string              `json:"scope"`
	BlobSHA1        string              `json:"blob_sha1"`
	ContainerInputs map[string]string   `json:"container_inputs,omitempty"`
}

type missileCarrierCatalog struct {
	Schema   int                              `json:"schema"`
	Commit   string                           `json:"commit"`
	Families map[string]string                `json:"families"`
	Carriers map[string]missileCarrierProfile `json:"carriers"`
}

var missileCarriers = func() missileCarrierCatalog {
	var catalog missileCarrierCatalog
	if err := json.Unmarshal(embeddedMissileCarriersJSON, &catalog); err != nil || catalog.Schema != 1 || len(catalog.Commit) != 40 || len(catalog.Families) == 0 {
		panic("invalid embedded missile carrier catalogue")
	}
	return catalog
}()

// Family-only identities are deliberately created after all exact-model
// association stages. An empty InternalName and Inferred confidence prevent
// a display family from masquerading as a decoded variant or propagating one.
func applyMissileFamilyNames(tracks []*track, table *missileModelTable) int {
	return applyMissileFamilyNamesWithCatalog(tracks, table, missileCarriers)
}

func applyMissileFamilyNamesWithCatalog(tracks []*track, table *missileModelTable, catalog missileCarrierCatalog) int {
	if table == nil {
		return 0
	}
	aliases := newMissileAssociationAliasIndex(table)
	adopted := 0
	for _, tr := range tracks {
		if tr == nil || !tr.key.missile || tr.surfaceLaunch != nil || tr.projectileSource == nil || tr.missileModel == nil {
			continue
		}
		info := tr.missileModel
		if info.Model != "" || info.ModelConflict != "" {
			continue
		}
		source := tr.projectileSource
		kind := source.WeaponRef >> 16
		if kind != missileNameArmamentTriggerAAM && kind != missileNameArmamentTriggerAGM {
			continue
		}
		role := "AAM"
		if kind == missileNameArmamentTriggerAGM {
			role = "AGM"
		}
		template, valid := missileAssociationTemplate(source.Template)
		seeker, conflicting := missileAssociationSeekerKind(tr)
		if !valid || conflicting {
			continue
		}
		actual, _, conflict := missileAssociationOwnerCandidates(source, aliases)
		if conflict != "" {
			continue
		}
		candidates := missileAssociationFilterCandidates(actual, kind, template, seeker)
		evidenceSource, carrier := "ReplayOwnerLoadoutFamily", ""
		if len(actual) == 0 || missileAssociationHasUnresolvedAmmo(source, aliases) {
			// The parent was selected at construction time by exact UID and
			// sortie lifetime. Never look up a later sortie or another player.
			if tr.parent == nil || !tr.parent.key.air || tr.parent.entity == nil {
				continue
			}
			carrier, _ = packetecs2.GetObjectData[string](&tr.parent.entity.Data, "unit__className")
			profile, ok := catalog.Carriers[strings.ToLower(strings.TrimSpace(carrier))]
			if !ok || !profile.Complete[role] || profile.Scope != "DeclaredWeaponSlotsAndCommonWeapons" {
				continue
			}
			var options []missileAssociationCandidate
			complete := true
			for _, weapon := range profile.Weapons {
				entries := aliases.candidatesFor(weapon)
				if len(entries) == 0 {
					complete = false
					break
				}
				options = append(options, entries...)
			}
			if !complete {
				continue
			}
			options = missileAssociationFilterCandidates(options, kind, template, seeker)
			if len(options) == 0 {
				continue
			}
			// Retain recognized actual stores too: a static profile must not
			// discard contradictory replay evidence to manufacture uniqueness.
			candidates = append(candidates, options...)
			evidenceSource = "CarrierOptionsGuidanceFamily"
		}
		family, variants := commonMissileFamily(candidates, catalog.Families)
		if family == "" {
			continue
		}
		info.Model, info.Family, info.FamilyCarrier = family, family, carrier
		info.InternalName = ""
		info.Source, info.Confidence = evidenceSource, "Inferred"
		info.ModelCandidates, info.ModelTableApplied = variants, true
		tr.name = family
		adopted++
	}
	return adopted
}

func commonMissileFamily(candidates []missileAssociationCandidate, families map[string]string) (string, []string) {
	if len(candidates) == 0 {
		return "", nil
	}
	family := ""
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.confidence != "confirmed" && candidate.confidence != "high" {
			return "", nil
		}
		current := families[candidate.model]
		if current == "" || family != "" && family != current {
			return "", nil
		}
		family = current
		seen[candidate.model] = true
	}
	variants := make([]string, 0, len(seen))
	for model := range seen {
		variants = append(variants, model)
	}
	sort.Strings(variants)
	return family, variants
}
