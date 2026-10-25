package main

import (
	"sort"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

// Successful owner-loadout identities use a source distinct from kill-report
// and replay-weapon-ID evidence. A propagation source identifies a model copied
// only inside one exact owner, generation, weapon-ID and template group.
const (
	missileAssociationSourceMatched    = "ReplayOwnerLoadoutMatched"
	missileAssociationSourcePropagated = "ReplayOwnerLoadoutPropagated"
)

type missileAssociationCandidate struct {
	model      string
	internal   string
	confidence string
	guidance   string
}

type missileAssociationOwnerKey struct {
	ownerEID   uint64
	parentUID  uint64
	generation uint64
	weaponID   uint32
	template   string
	kind       uint32
}

type missileAssociationEvidence struct {
	track          *track
	info           *missileModelInfo
	key            missileAssociationOwnerKey
	hasKey         bool
	template       string
	templateOK     bool
	kind           uint32
	constraintsOK  bool
	seekerKind     projectiles.SeekerKind
	seekerConflict bool
	candidates     []missileAssociationCandidate
	ambiguous      bool
	incomplete     bool
	conflict       string
}

type missileAssociationGroup struct {
	identity string
	model    string
	internal string
	guidance string
	conflict bool
	models   []string
}

type missileAssociationAliasIndex map[string][]missileAssociationCandidate

var missileAssociationEmbeddedGuidance = func() map[string]missileGuidanceEntry {
	table, err := parseMissileGuidanceJSON(embeddedMissileGuidanceJSON)
	if err != nil {
		return nil
	}
	return table.Models
}()

// applyMissileModelAssociations extends models only from an exact owner
// ammunition snapshot. It never chooses among mixed loadout rows, never uses
// slot order, proximity, speed or launch order, and never replaces a model that
// an earlier evidence stage already supplied.
func applyMissileModelAssociations(tracks []*track, table *missileModelTable) int {
	if table == nil {
		return 0
	}
	aliases := newMissileAssociationAliasIndex(table)
	evidence := collectMissileAssociationEvidence(tracks, aliases)
	groups := buildMissileAssociationGroups(evidence, aliases)

	adopted := 0
	for _, current := range evidence {
		info := current.info
		if info == nil {
			continue
		}
		if !current.constraintsOK {
			continue
		}
		candidates := missileAssociationFilterCandidates(current.candidates, current.kind, current.template, current.seekerKind)
		if info.Model != "" {
			// A model already established by a kill report or the replay-scoped
			// table is immutable here. Contradiction is retained as metadata.
			if len(candidates) > 1 {
				missileAssociationRecordCandidates(info, candidates)
			}
			if len(candidates) == 1 && !missileAssociationSameModel(info.Model, candidates[0].model) {
				missileAssociationRecordConflict(info, info.Model, candidates[0].model, candidates[0].model)
			} else if current.conflict != "" {
				missileAssociationRecordConflict(info, info.Model, current.conflict, missileAssociationCandidateModels(candidates)...)
			}
			if current.hasKey {
				if group := groups[current.key]; group != nil && group.conflict {
					missileAssociationRecordConflict(info, info.Model, "owner group "+strings.Join(group.models, " / "), group.models...)
				}
			}
			continue
		}

		if current.incomplete {
			// Unknown stores must not disappear from the candidate universe.
			missileAssociationRecordCandidates(info, candidates)
			continue
		}
		if current.conflict != "" || len(candidates) > 1 {
			missileAssociationRecordCandidates(info, candidates)
			if current.conflict != "" {
				missileAssociationRecordConflict(info, "unknown", current.conflict, missileAssociationCandidateModels(candidates)...)
			}
			continue
		}

		if len(candidates) == 1 {
			candidate := candidates[0]
			if current.hasKey {
				if group := groups[current.key]; group != nil {
					if group.conflict {
						missileAssociationRecordConflict(info, "unknown", "owner group "+strings.Join(group.models, " / "), missileAssociationCandidateModels(candidates)...)
						continue
					}
					if group.identity != missileAssociationIdentity(aliases, candidate.model, candidate.internal) {
						missileAssociationRecordConflict(info, group.model, candidate.model, candidate.model)
						continue
					}
				}
			}
			if missileAssociationAdopt(current, candidate.model, candidate.internal,
				missileAssociationSourceMatched, candidate.confidence) {
				adopted++
			}
			continue
		}
		if len(current.candidates) != 0 {
			// The alias-matched ammunition contradicts every known template
			// constraint. Keep it as a candidate, but do not name the missile.
			missileAssociationRecordCandidates(info, current.candidates)
			continue
		}

		// No usable owner ammunition name does not erase a model already
		// established for the same exact weapon group on this parent.
		if !current.hasKey {
			continue
		}
		group := groups[current.key]
		if group == nil {
			continue
		}
		if group.conflict {
			missileAssociationRecordConflict(info, "unknown", "owner group "+strings.Join(group.models, " / "), group.models...)
			continue
		}
		if !missileAssociationSeekerAllows(group.guidance, current.seekerKind) {
			continue
		}
		if missileAssociationAdopt(current, group.model, group.internal,
			missileAssociationSourcePropagated, "high") {
			adopted++
		}
	}
	return adopted
}

func collectMissileAssociationEvidence(tracks []*track, aliases missileAssociationAliasIndex) []*missileAssociationEvidence {
	result := make([]*missileAssociationEvidence, 0)
	for _, tr := range tracks {
		if tr == nil || !tr.key.missile {
			continue
		}
		info := tr.missileModel
		if info == nil {
			info = &missileModelInfo{Source: "Unknown", Confidence: "Unknown"}
			tr.missileModel = info
		}
		if tr.projectileSource != nil {
			info.LeadingULEB = tr.projectileSource.LeadingULEB
			info.HasLeadingULEB = true
		}
		if !info.HasWeaponID {
			info.WeaponID, info.HasWeaponID = missileGuidanceWeaponID(tr)
		}

		current := &missileAssociationEvidence{track: tr, info: info}
		if tr.projectileSource != nil {
			current.kind = tr.projectileSource.WeaponRef >> 16
			current.template, current.templateOK = missileAssociationTemplate(tr.projectileSource.Template)
			current.key.ownerEID = tr.projectileSource.OwnerEID
			current.key.parentUID = missileAssociationParentUID(tr)
			current.key.generation = tr.projectileSource.ID >> 22
			current.key.weaponID = info.WeaponID
			current.key.template = current.template
			current.key.kind = current.kind
			current.hasKey = current.key.ownerEID != 0 && current.key.parentUID != 0 &&
				info.HasWeaponID && current.templateOK &&
				(current.kind == missileNameArmamentTriggerAAM || current.kind == missileNameArmamentTriggerAGM)
			current.constraintsOK = current.templateOK && current.kind == missileNameArmamentTriggerAAM
			current.candidates, current.ambiguous, current.conflict =
				missileAssociationOwnerCandidates(tr.projectileSource, aliases)
			current.incomplete = missileAssociationHasUnresolvedAmmo(tr.projectileSource, aliases)
			current.seekerKind, current.seekerConflict = missileAssociationSeekerKind(tr)
			if current.seekerConflict {
				current.constraintsOK = false
			}
		}
		result = append(result, current)
	}
	return result
}

func missileAssociationSeekerKind(tr *track) (projectiles.SeekerKind, bool) {
	if tr == nil {
		return projectiles.SeekerKindUnknown, false
	}
	kind := projectiles.SeekerKindUnknown
	for _, sample := range tr.samples {
		if sample.projectile == nil || !sample.projectile.FromSync || sample.projectile.RawMotion == nil {
			continue
		}
		state, ok := sample.projectile.RawMotion.SeekerState()
		if !ok {
			continue
		}
		switch state.Kind {
		case projectiles.SeekerKindIR, projectiles.SeekerKindRadar:
			if kind != projectiles.SeekerKindUnknown && kind != state.Kind {
				return projectiles.SeekerKindUnknown, true
			}
			kind = state.Kind
		}
	}
	return kind, false
}

func missileAssociationParentUID(tr *track) uint64 {
	if tr == nil {
		return 0
	}
	if tr.projectileSource != nil && tr.projectileSource.HasParent {
		return tr.projectileSource.ParentUID
	}
	if tr.parent != nil {
		return tr.parent.key.id
	}
	return 0
}

func missileAssociationTemplate(template string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(template))
	switch value {
	case "rocket", "rocket+arh_shell":
		return value, true
	default:
		return value, false
	}
}

func newMissileAssociationAliasIndex(table *missileModelTable) missileAssociationAliasIndex {
	if table == nil || len(table.Aliases) == 0 {
		return nil
	}
	index := make(missileAssociationAliasIndex)
	for key, entry := range table.Aliases {
		candidate := missileAssociationCandidate{
			model: entry.Model, internal: entry.InternalName,
			confidence: entry.Confidence,
			guidance:   missileAssociationAliasGuidance(entry),
		}
		missileAssociationIndexCandidate(index, strings.ToLower(strings.TrimSpace(key)), candidate)
		if normalized := normalizeMissileWeaponAliasKey(key); normalized != "" {
			missileAssociationIndexCandidate(index, normalized, candidate)
		}
	}
	return index
}

func missileAssociationAliasGuidance(entry missileModelAliasEntry) string {
	for _, name := range []string{entry.InternalName, normalizeMissileWeaponAliasKey(entry.InternalName)} {
		if name == "" {
			continue
		}
		if static, ok := missileAssociationEmbeddedGuidance[name]; ok {
			if static.Confidence != "confirmed" && static.Confidence != "high" {
				continue
			}
			switch static.Guidance {
			case "ARH", "IR", "SARH":
				return static.Guidance
			}
		}
	}
	return missileAssociationGuidanceFromSource(entry.Source)
}

func missileAssociationIndexCandidate(index missileAssociationAliasIndex, key string, candidate missileAssociationCandidate) {
	if key == "" || candidate.model == "" || candidate.internal == "" {
		return
	}
	for _, existing := range index[key] {
		if existing == candidate {
			return
		}
	}
	index[key] = append(index[key], candidate)
}

func (index missileAssociationAliasIndex) candidatesFor(name string) []missileAssociationCandidate {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil
	}
	result := append([]missileAssociationCandidate(nil), index[key]...)
	if normalized := normalizeMissileWeaponAliasKey(key); normalized != key {
		result = append(result, index[normalized]...)
	}
	return result
}

func missileAssociationGuidanceFromSource(source string) string {
	value := strings.ToLower(source)
	if strings.Contains(value, "guidancetype=optical") {
		return "IR"
	}
	if strings.Contains(value, "guidancetype=radar") {
		if strings.Contains(value, "radarseeker.active=true") {
			return "ARH"
		}
		if strings.Contains(value, "radarseeker.active absent") || strings.Contains(value, "sarh") {
			return "SARH"
		}
	}
	return ""
}

func missileAssociationOwnerCandidates(source *projectiles.Projectile, aliases missileAssociationAliasIndex) ([]missileAssociationCandidate, bool, string) {
	if source == nil || len(source.OwnerWeapons) == 0 || len(aliases) == 0 {
		return nil, false, ""
	}
	type aggregate struct {
		model            string
		internal         string
		confidence       string
		guidance         string
		guidanceConflict bool
	}
	aggregates := make(map[string]*aggregate)
	for _, ammo := range source.OwnerWeapons {
		if ammo.Count == 0 {
			continue
		}
		for _, name := range []string{ammo.Launcher, ammo.Bullet} {
			for _, candidate := range aliases.candidatesFor(name) {
				current := aggregates[candidate.model]
				if current == nil {
					aggregates[candidate.model] = &aggregate{
						model: candidate.model, internal: candidate.internal,
						confidence: candidate.confidence, guidance: candidate.guidance,
					}
					continue
				}
				if current.confidence != "confirmed" && candidate.confidence == "confirmed" {
					current.confidence = "confirmed"
				}
				if current.guidance == "" {
					current.guidance = candidate.guidance
				} else if candidate.guidance != "" && current.guidance != candidate.guidance {
					current.guidanceConflict = true
				}
				current.internal = missileAssociationPreferredInternal(current.internal, candidate.internal)
			}
		}
	}
	if len(aggregates) == 0 {
		return nil, false, ""
	}
	result := make([]missileAssociationCandidate, 0, len(aggregates))
	conflict := ""
	for _, aggregate := range aggregates {
		candidate := missileAssociationCandidate{
			model: aggregate.model, internal: aggregate.internal,
			confidence: aggregate.confidence, guidance: aggregate.guidance,
		}
		result = append(result, candidate)
		if aggregate.guidanceConflict {
			conflict = "owner loadout aliases disagree on guidance for " + aggregate.model
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].model < result[j].model })
	return result, len(result) > 1, conflict
}

func missileAssociationPreferredInternal(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" || current == next {
		return current
	}
	currentDefault := strings.HasSuffix(strings.ToLower(current), "_default")
	nextDefault := strings.HasSuffix(strings.ToLower(next), "_default")
	if currentDefault != nextDefault {
		if nextDefault {
			return current
		}
		return next
	}
	if next < current {
		return next
	}
	return current
}

func missileAssociationHasUnresolvedAmmo(source *projectiles.Projectile, aliases missileAssociationAliasIndex) bool {
	if source == nil {
		return false
	}
	for _, ammo := range source.OwnerWeapons {
		if ammo.Count == 0 || len(aliases.candidatesFor(ammo.Launcher)) > 0 || len(aliases.candidatesFor(ammo.Bullet)) > 0 {
			continue
		}
		// Only explicit asset directories or countermeasure launcher names
		// exclude a row. An unfamiliar name is never assumed to be a gun.
		name := strings.ToLower(strings.ReplaceAll(ammo.Launcher, "\\", "/"))
		base := name[strings.LastIndex(name, "/")+1:]
		if strings.HasPrefix(base, "countermeasure_") || strings.HasPrefix(base, "countermeasures_") {
			continue
		}
		knownOther := false
		for _, directory := range []string{"/cannons/", "/machineguns/", "/bombguns/", "/torpedoguns/"} {
			if strings.Contains("/"+name, directory) {
				knownOther = true
				break
			}
		}
		if !knownOther {
			return true
		}
	}
	return false
}

func missileAssociationCandidateAllowed(candidate missileAssociationCandidate, kind uint32, template string) bool {
	if kind != missileNameArmamentTriggerAAM {
		// The explicit alias table currently describes air-to-air missiles.
		// An AGM trigger therefore cannot select one of these rows.
		return false
	}
	switch template {
	case "rocket+arh_shell":
		return candidate.guidance == "" || candidate.guidance == "ARH"
	case "rocket":
		return candidate.guidance == "" || candidate.guidance == "IR" || candidate.guidance == "SARH"
	default:
		return false
	}
}

func missileAssociationFilterCandidates(candidates []missileAssociationCandidate, kind uint32, template string, seekerKind projectiles.SeekerKind) []missileAssociationCandidate {
	result := make([]missileAssociationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if missileAssociationCandidateAllowed(candidate, kind, template) &&
			missileAssociationSeekerAllows(candidate.guidance, seekerKind) {
			result = append(result, candidate)
		}
	}
	return result
}

func missileAssociationSeekerAllows(guidance string, kind projectiles.SeekerKind) bool {
	switch kind {
	case projectiles.SeekerKindIR:
		return guidance == "IR"
	case projectiles.SeekerKindRadar:
		return guidance == "ARH" || guidance == "SARH"
	default:
		return true
	}
}

func buildMissileAssociationGroups(evidence []*missileAssociationEvidence, aliases missileAssociationAliasIndex) map[missileAssociationOwnerKey]*missileAssociationGroup {
	groups := make(map[missileAssociationOwnerKey]*missileAssociationGroup)
	for _, current := range evidence {
		if !current.hasKey || current.info == nil || !current.constraintsOK || current.incomplete {
			continue
		}
		if !missileAssociationStrongIdentity(current.info) {
			continue
		}
		if current.ambiguous || current.conflict != "" {
			continue
		}
		if len(current.candidates) == 1 && !missileAssociationSameModel(current.info.Model, current.candidates[0].model) {
			continue
		}
		guidance := missileAssociationGuidanceForModel(aliases, current.info.Model, current.info.InternalName)
		candidate := missileAssociationCandidate{model: current.info.Model, internal: current.info.InternalName, guidance: guidance}
		if !missileAssociationCandidateAllowed(candidate, current.kind, current.template) ||
			!missileAssociationSeekerAllows(guidance, current.seekerKind) {
			continue
		}
		internal := missileAssociationCanonicalInternal(aliases, current.info.Model, current.info.InternalName)
		identity := missileAssociationIdentity(aliases, current.info.Model, internal)
		group := groups[current.key]
		if group == nil {
			groups[current.key] = &missileAssociationGroup{
				identity: identity, model: current.info.Model, internal: internal,
				guidance: guidance, models: []string{current.info.Model},
			}
			continue
		}
		if group.identity != identity {
			group.conflict = true
			group.models = missileModelCandidates(append(group.models, current.info.Model)...)
		}
	}
	return groups
}

func missileAssociationStrongIdentity(info *missileModelInfo) bool {
	if info == nil || info.Model == "" || info.InternalName == "" || info.ModelConflict != "" {
		return false
	}
	switch info.Confidence {
	case "confirmed", "high", "Matched":
		return true
	default:
		return false
	}
}

func missileAssociationGuidanceForModel(aliases missileAssociationAliasIndex, model, internal string) string {
	for _, candidate := range aliases.candidatesFor(internal) {
		if missileAssociationSameModel(candidate.model, model) && candidate.guidance != "" {
			return candidate.guidance
		}
	}
	return ""
}

func missileAssociationCanonicalInternal(aliases missileAssociationAliasIndex, model, internal string) string {
	best := ""
	for _, candidate := range aliases.candidatesFor(internal) {
		if missileAssociationSameModel(candidate.model, model) {
			best = missileAssociationPreferredInternal(best, candidate.internal)
		}
	}
	if best != "" {
		return best
	}
	return internal
}

func missileAssociationIdentity(aliases missileAssociationAliasIndex, model, internal string) string {
	return strings.ToLower(strings.TrimSpace(model)) + "\x00" + missileAssociationCanonicalInternal(aliases, model, internal)
}

func missileAssociationSameModel(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func missileAssociationCandidateModels(candidates []missileAssociationCandidate) []string {
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.model != "" {
			result = append(result, candidate.model)
		}
	}
	return result
}

func missileAssociationRecordCandidates(info *missileModelInfo, candidates []missileAssociationCandidate) {
	if info == nil || len(candidates) == 0 {
		return
	}
	values := append([]string(nil), info.ModelCandidates...)
	values = append(values, missileAssociationCandidateModels(candidates)...)
	info.ModelCandidates = missileModelCandidates(values...)
	info.ModelTableApplied = true
}

func missileAssociationRecordConflict(info *missileModelInfo, existing, owner string, candidates ...string) {
	if info == nil {
		return
	}
	existing = strings.TrimSpace(existing)
	if existing == "" {
		existing = "unknown"
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "unknown"
	}
	detail := "existing=" + existing + "; owner=" + owner
	if info.ModelConflict == "" {
		info.ModelConflict = detail
	} else if !strings.Contains(info.ModelConflict, detail) {
		info.ModelConflict += "; " + detail
	}
	values := append([]string(nil), info.ModelCandidates...)
	values = append(values, candidates...)
	info.ModelCandidates = missileModelCandidates(values...)
	info.ModelTableApplied = true
}

func missileAssociationAdopt(current *missileAssociationEvidence, model, internal, source, confidence string) bool {
	if current == nil || current.info == nil || current.track == nil || current.info.Model != "" {
		return false
	}
	if confidence != "confirmed" && confidence != "high" {
		return false
	}
	if confidence != "confirmed" {
		confidence = "high"
	}
	current.info.Model = model
	current.info.InternalName = internal
	current.info.Source = source
	current.info.Confidence = confidence
	current.info.ModelCandidates = nil
	current.info.ModelTableApplied = true
	current.track.name = model
	return true
}
