package main

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const missileModelTableMaxFileBytes = 1 << 20

// Replay-scoped model rows remain evidence-owned data. The embedded table is
// the default; -missile-models can disable it or load a validated override.
//
//go:embed data/missile_models.json
var embeddedMissileModelsJSON []byte

//go:embed data/aam_models.json
var embeddedAAMModelsJSON []byte

//go:embed data/agm_models.json
var embeddedAGMModelsJSON []byte

type missileModelEntry struct {
	Model        string   `json:"model"`
	InternalName string   `json:"internal_name"`
	Confidence   string   `json:"confidence"`
	Source       string   `json:"source"`
	Candidates   []string `json:"candidates"`
}

type missileModelAliasEntry struct {
	Model        string `json:"model"`
	InternalName string `json:"internal_name"`
	Confidence   string `json:"confidence"`
	Source       string `json:"source"`
	Role         string `json:"role,omitempty"`
}

type missileModelReplay struct {
	WeaponIDs map[string]missileModelEntry `json:"weapon_ids"`
}

type missileModelTable struct {
	Schema        int                               `json:"schema"`
	GeneratedFrom []string                          `json:"generated_from"`
	Aliases       map[string]missileModelAliasEntry `json:"aliases,omitempty"`
	Replays       map[string]missileModelReplay     `json:"replays"`
}

func loadMissileModels(spec string) (*missileModelTable, error) {
	switch value := strings.ToLower(strings.TrimSpace(spec)); value {
	case "", "off":
		return nil, nil
	case "embedded":
		table, err := parseMissileModelsJSON(embeddedMissileModelsJSON)
		if err != nil {
			return nil, err
		}
		// Preserve reviewed display names across every spelling of the same
		// internal identity; generated aliases retain their asset provenance.
		reviewed := make(map[string]missileModelAliasEntry)
		for _, entry := range table.Aliases {
			reviewed[normalizeMissileWeaponAliasKey(entry.InternalName)] = entry
		}
		for _, raw := range [][]byte{embeddedAAMModelsJSON, embeddedAGMModelsJSON} {
			catalog, err := parseMissileModelsJSON(raw)
			if err != nil {
				return nil, err
			}
			for key, entry := range catalog.Aliases {
				if old, ok := reviewed[normalizeMissileWeaponAliasKey(entry.InternalName)]; ok {
					if old.Role != entry.Role {
						return nil, fmt.Errorf("conflicting embedded missile role %q", key)
					}
					entry.Model = old.Model
				}
				if old, exists := table.Aliases[key]; exists &&
					(normalizeMissileWeaponAliasKey(old.InternalName) != normalizeMissileWeaponAliasKey(entry.InternalName) || old.Role != entry.Role) {
					return nil, fmt.Errorf("conflicting embedded missile alias %q", key)
				}
				table.Aliases[key] = entry
			}
			table.GeneratedFrom = append(table.GeneratedFrom, catalog.GeneratedFrom...)
		}
		if err := validateMissileModelTable(table); err != nil {
			return nil, err
		}
		return table, nil
	default:
		data, err := readMissileModelsFile(spec)
		if err != nil {
			return nil, err
		}
		return parseMissileModelsJSON(data)
	}
}

func readMissileModelsFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening missile models table: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, missileModelTableMaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading missile models table: %w", err)
	}
	if len(data) > missileModelTableMaxFileBytes {
		return nil, fmt.Errorf("missile models table exceeds %d bytes", missileModelTableMaxFileBytes)
	}
	return data, nil
}

func parseMissileModelsJSON(data []byte) (*missileModelTable, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if len(data) == 0 {
		return nil, fmt.Errorf("missile models table is empty")
	}
	if len(data) > missileModelTableMaxFileBytes {
		return nil, fmt.Errorf("missile models table exceeds %d bytes", missileModelTableMaxFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var table missileModelTable
	if err := decoder.Decode(&table); err != nil {
		return nil, fmt.Errorf("parsing missile models table: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("missile models table has trailing JSON")
		}
		return nil, fmt.Errorf("parsing missile models table suffix: %w", err)
	}
	if err := validateMissileModelTable(&table); err != nil {
		return nil, err
	}
	return &table, nil
}

func validateMissileModelTable(table *missileModelTable) error {
	if table.Schema != 1 {
		return fmt.Errorf("missile models schema = %d, want 1", table.Schema)
	}
	if table.GeneratedFrom == nil {
		return fmt.Errorf("missile models table is missing generated_from")
	}
	for i, source := range table.GeneratedFrom {
		if err := validateMissileModelText(fmt.Sprintf("generated_from[%d]", i), source, 512); err != nil {
			return err
		}
	}
	if table.Replays == nil {
		return fmt.Errorf("missile models table is missing replays")
	}

	if table.Aliases != nil {
		keys := make([]string, 0, len(table.Aliases))
		for key := range table.Aliases {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		type seenAlias struct {
			key   string
			entry missileModelAliasEntry
		}
		normalized := make(map[string]seenAlias, len(keys))
		for _, key := range keys {
			if err := validateMissileModelInternalName("alias", key); err != nil {
				return err
			}
			normalizedKey := normalizeMissileWeaponAliasKey(key)
			entry := table.Aliases[key]
			if previous, exists := normalized[normalizedKey]; exists {
				// A table can list both the observed _default spelling and its
				// canonical name. They may normalize to one kill key only when
				// they identify the same model at the same confidence.
				defaultVariant := strings.HasSuffix(strings.ToLower(key), "_default") ||
					strings.HasSuffix(strings.ToLower(previous.key), "_default")
				if !defaultVariant || previous.entry.Model != entry.Model || previous.entry.Confidence != entry.Confidence || previous.entry.Role != entry.Role {
					return fmt.Errorf("missile models has conflicting duplicate alias %q", normalizedKey)
				}
			}
			normalized[normalizedKey] = seenAlias{key: key, entry: entry}
			if err := validateMissileModelAliasEntry("aliases["+key+"]", entry); err != nil {
				return err
			}
		}
	}

	replayKeys := make([]string, 0, len(table.Replays))
	for key := range table.Replays {
		replayKeys = append(replayKeys, key)
	}
	sort.Strings(replayKeys)
	normalizedReplays := make(map[string]missileModelReplay, len(replayKeys))
	for _, key := range replayKeys {
		sha, err := normalizeMissileModelReplaySHA(key)
		if err != nil {
			return err
		}
		if _, exists := normalizedReplays[sha]; exists {
			return fmt.Errorf("missile models has duplicate replay SHA %q", sha)
		}
		replay := table.Replays[key]
		if replay.WeaponIDs == nil {
			return fmt.Errorf("missile models replay %q is missing weapon_ids", key)
		}
		ids := make([]string, 0, len(replay.WeaponIDs))
		for id := range replay.WeaponIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if err := validateMissileModelWeaponID(id); err != nil {
				return fmt.Errorf("missile models replay %q: %w", key, err)
			}
			if err := validateMissileModelEntry("replays["+key+"].weapon_ids["+id+"]", replay.WeaponIDs[id]); err != nil {
				return err
			}
		}
		normalizedReplays[sha] = replay
	}
	table.Replays = normalizedReplays
	return nil
}

func validateMissileModelEntry(label string, entry missileModelEntry) error {
	switch entry.Confidence {
	case "confirmed", "high", "medium", "low":
	default:
		return fmt.Errorf("missile models %s has invalid confidence %q", label, entry.Confidence)
	}
	specific := modelEntryIsSpecific(entry)
	if specific && (entry.Model == "" || entry.InternalName == "") {
		return fmt.Errorf("missile models %s requires model and internal_name at %s confidence", label, entry.Confidence)
	}
	if entry.Model != "" {
		if err := validateMissileModelText(label+".model", entry.Model, 128); err != nil {
			return err
		}
	}
	if entry.InternalName != "" {
		if err := validateMissileModelInternalName(label+".internal_name", entry.InternalName); err != nil {
			return err
		}
	}
	if err := validateMissileModelText(label+".source", entry.Source, 512); err != nil {
		return err
	}
	if entry.Candidates == nil {
		return fmt.Errorf("missile models %s is missing candidates", label)
	}
	if len(entry.Candidates) > 32 {
		return fmt.Errorf("missile models %s has too many candidates", label)
	}
	seen := make(map[string]bool, len(entry.Candidates))
	for i, candidate := range entry.Candidates {
		candidateLabel := fmt.Sprintf("%s.candidates[%d]", label, i)
		if err := validateMissileModelText(candidateLabel, candidate, 128); err != nil {
			return err
		}
		if seen[candidate] {
			return fmt.Errorf("missile models %s has duplicate candidate %q", label, candidate)
		}
		seen[candidate] = true
	}
	if (entry.Confidence == "medium" || entry.Confidence == "low") && len(entry.Candidates) == 0 {
		return fmt.Errorf("missile models %s requires candidates at %s confidence", label, entry.Confidence)
	}
	return nil
}

func validateMissileModelAliasEntry(label string, entry missileModelAliasEntry) error {
	if entry.Role != "" && entry.Role != "AGM" {
		return fmt.Errorf("missile models %s has invalid role %q", label, entry.Role)
	}
	if err := validateMissileModelText(label+".model", entry.Model, 128); err != nil {
		return err
	}
	if err := validateMissileModelInternalName(label+".internal_name", entry.InternalName); err != nil {
		return err
	}
	if entry.Confidence != "confirmed" && entry.Confidence != "high" {
		return fmt.Errorf("missile models %s has invalid confidence %q", label, entry.Confidence)
	}
	return validateMissileModelText(label+".source", entry.Source, 512)
}

func validateMissileModelInternalName(label, name string) error {
	if name == "" {
		return fmt.Errorf("missile models %s is empty", label)
	}
	if len(name) > 128 {
		return fmt.Errorf("missile models %s exceeds 128 bytes", label)
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("missile models %s is not valid UTF-8", label)
	}
	if strings.TrimSpace(name) != name || strings.ContainsAny(name, " \t\r\n") {
		return fmt.Errorf("missile models %s %q is not exact", label, name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("missile models %s %q contains a control character", label, name)
		}
	}
	return nil
}

func validateMissileModelText(label, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("missile models %s is empty", label)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("missile models %s exceeds %d bytes", label, maxBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("missile models %s has surrounding whitespace", label)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("missile models %s is not valid UTF-8", label)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("missile models %s contains a control character", label)
		}
	}
	return nil
}

func normalizeMissileModelReplaySHA(key string) (string, error) {
	if len(key) != 64 {
		return "", fmt.Errorf("missile models replay SHA %q must be 64 hex characters", key)
	}
	if _, err := hex.DecodeString(key); err != nil {
		return "", fmt.Errorf("missile models replay SHA %q is not hexadecimal", key)
	}
	return strings.ToLower(key), nil
}

func validateMissileModelWeaponID(key string) error {
	value, err := strconv.ParseUint(key, 10, 32)
	if err != nil {
		return fmt.Errorf("weapon ID %q is not a uint32", key)
	}
	if strconv.FormatUint(value, 10) != key {
		return fmt.Errorf("weapon ID %q is not canonical decimal", key)
	}
	return nil
}

func (table *missileModelTable) weaponIDsForReplay(replaySHA string) map[string]missileModelEntry {
	if table == nil || replaySHA == "" {
		return nil
	}
	sha, err := normalizeMissileModelReplaySHA(strings.ToLower(strings.TrimSpace(replaySHA)))
	if err != nil {
		return nil
	}
	replay, ok := table.Replays[sha]
	if !ok {
		return nil
	}
	return replay.WeaponIDs
}

func (table *missileModelTable) killAliases() map[string]missileWeaponAlias {
	if table == nil || len(table.Aliases) == 0 {
		return nil
	}
	aliases := make(map[string]missileWeaponAlias, len(table.Aliases))
	for pass := 0; pass < 2; pass++ {
		for key, entry := range table.Aliases {
			if strings.HasSuffix(strings.ToLower(strings.TrimSpace(key)), "_default") != (pass == 0) {
				continue
			}
			aliases[normalizeMissileWeaponAliasKey(key)] = missileWeaponAlias{
				model: entry.Model, internal: entry.InternalName, role: entry.Role,
			}
		}
	}
	return aliases
}

func applyMissileModelsForReplay(tracks []*track, table *missileModelTable, replaySHA string) int {
	if table == nil {
		return 0
	}
	scoped := table.weaponIDsForReplay(replaySHA)
	adopted := 0
	for _, tr := range tracks {
		if tr == nil || !tr.key.missile {
			continue
		}
		info := tr.missileModel
		if info == nil {
			info = &missileModelInfo{Source: "Unknown", Confidence: "Unknown"}
			tr.missileModel = info
		}
		info.ModelTableApplied = true
		if tr.projectileSource != nil {
			info.LeadingULEB = tr.projectileSource.LeadingULEB
			info.HasLeadingULEB = true
		}
		if !info.HasWeaponID {
			info.WeaponID, info.HasWeaponID = missileGuidanceWeaponID(tr)
		}
		if scoped == nil || !info.HasWeaponID {
			continue
		}
		entry, ok := scoped[strconv.FormatUint(uint64(info.WeaponID), 10)]
		if !ok {
			continue
		}

		if info.Model != "" {
			if modelEntryIsSpecific(entry) && missileModelEntryConflicts(info, entry) {
				info.ModelConflict = missileModelConflictValue(info, entry)
				info.ModelCandidates = missileModelCandidates(info.Model, entry.Model)
			}
			continue
		}
		if entry.Confidence == "confirmed" || entry.Confidence == "high" {
			info.Model = entry.Model
			info.InternalName = entry.InternalName
			info.Source = "ReplayWeaponIDFromModelTable"
			info.Confidence = entry.Confidence
			info.ModelCandidates = append([]string(nil), entry.Candidates...)
			tr.name = info.Model
			adopted++
			continue
		}
		info.Source = "ReplayWeaponIDFromModelTable"
		info.Confidence = entry.Confidence
		info.ModelCandidates = append([]string(nil), entry.Candidates...)
	}
	return adopted
}

func modelEntryIsSpecific(entry missileModelEntry) bool {
	return entry.Confidence == "confirmed" || entry.Confidence == "high"
}

func missileModelEntryConflicts(info *missileModelInfo, entry missileModelEntry) bool {
	return info.Model != "" && entry.Model != "" && info.Model != entry.Model
}

func missileModelConflictValue(info *missileModelInfo, entry missileModelEntry) string {
	existing := info.Model
	if existing == "" {
		existing = info.InternalName
	}
	table := entry.Model
	if table == "" {
		table = entry.InternalName
	}
	return "existing=" + existing + "; table=" + table
}

func missileModelCandidates(values ...string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func missileModelCSVValues(tr *track) []string {
	if tr == nil || !tr.key.missile || tr.missileModel == nil {
		return []string{"", "", "", "", ""}
	}
	info := tr.missileModel
	weaponID := ""
	if info.HasWeaponID {
		weaponID = strconv.FormatUint(uint64(info.WeaponID), 10)
	}
	return []string{
		weaponID,
		info.Model,
		missileNameSource(info.Source),
		missileNameConfidence(info.Confidence),
		strings.Join(info.ModelCandidates, " / "),
	}
}

func tracksHaveMissileModelColumns(tracks []*track) bool {
	for _, tr := range tracks {
		if tr != nil && tr.key.missile && tr.missileModel != nil && tr.missileModel.ModelTableApplied {
			return true
		}
	}
	return false
}
