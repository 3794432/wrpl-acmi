package main

import (
	"bytes"
	"crypto/sha256"
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

const missileGuidanceMaxFileBytes = 1 << 20

// The guidance table is evidence-owned data. Keeping it embedded makes the
// standalone exporter independent of the source checkout while still leaving
// a path for an explicit, validated override.
//
//go:embed data/missile_guidance.json
var embeddedMissileGuidanceJSON []byte

type missileGuidanceEntry struct {
	Guidance   string `json:"guidance"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
}

type missileGuidanceReplay struct {
	WeaponIDs map[string]missileGuidanceEntry `json:"weapon_ids"`
}

type missileGuidanceTable struct {
	Schema        int                             `json:"schema"`
	GeneratedFrom []string                        `json:"generated_from"`
	Models        map[string]missileGuidanceEntry `json:"models"`
	// Replays is optional. IDs below a SHA-256 key are evidence for that exact
	// replay only; they are never applied to another replay or by file name.
	Replays map[string]missileGuidanceReplay `json:"replays,omitempty"`
}

// missileGuidanceInfo is resolved once per missile track. Its fields are
// emitted as static ACMI properties on the creation frame only.
type missileGuidanceInfo struct {
	Type       string
	Source     string
	Confidence string
	Candidates string
}

func loadMissileGuidance(spec string) (*missileGuidanceTable, error) {
	switch value := strings.ToLower(strings.TrimSpace(spec)); value {
	case "", "off":
		return nil, nil
	case "embedded":
		return parseMissileGuidanceJSON(embeddedMissileGuidanceJSON)
	default:
		data, err := readMissileGuidanceFile(spec)
		if err != nil {
			return nil, err
		}
		return parseMissileGuidanceJSON(data)
	}
}

func readMissileGuidanceFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening missile guidance table: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, missileGuidanceMaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading missile guidance table: %w", err)
	}
	if len(data) > missileGuidanceMaxFileBytes {
		return nil, fmt.Errorf("missile guidance table exceeds %d bytes", missileGuidanceMaxFileBytes)
	}
	return data, nil
}

// hashMissileGuidanceReplay streams the input replay through SHA-256. The
// digest is the only replay selector; file name, version, and size are not
// used as substitutes.
func hashMissileGuidanceReplay(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening replay for guidance SHA-256: %w", err)
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", fmt.Errorf("hashing replay for guidance: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parseMissileGuidanceJSON(data []byte) (*missileGuidanceTable, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if len(data) == 0 {
		return nil, fmt.Errorf("missile guidance table is empty")
	}
	if len(data) > missileGuidanceMaxFileBytes {
		return nil, fmt.Errorf("missile guidance table exceeds %d bytes", missileGuidanceMaxFileBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var table missileGuidanceTable
	if err := decoder.Decode(&table); err != nil {
		return nil, fmt.Errorf("parsing missile guidance table: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("missile guidance table has trailing JSON")
		}
		return nil, fmt.Errorf("parsing missile guidance table suffix: %w", err)
	}
	if err := validateMissileGuidanceTable(&table); err != nil {
		return nil, err
	}
	return &table, nil
}

func validateMissileGuidanceTable(table *missileGuidanceTable) error {
	if table.Schema != 1 {
		return fmt.Errorf("missile guidance schema = %d, want 1", table.Schema)
	}
	if table.GeneratedFrom == nil {
		return fmt.Errorf("missile guidance table is missing generated_from")
	}
	for i, source := range table.GeneratedFrom {
		if err := validateMissileGuidanceText(fmt.Sprintf("generated_from[%d]", i), source, 512); err != nil {
			return err
		}
	}
	if table.Models == nil {
		return fmt.Errorf("missile guidance table is missing models")
	}

	names := make([]string, 0, len(table.Models))
	for name := range table.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := validateMissileGuidanceInternalName(name); err != nil {
			return err
		}
		if err := validateMissileGuidanceEntry("models["+name+"]", table.Models[name]); err != nil {
			return err
		}
	}
	if table.Replays != nil {
		normalized := make(map[string]missileGuidanceReplay, len(table.Replays))
		replayKeys := make([]string, 0, len(table.Replays))
		for key := range table.Replays {
			replayKeys = append(replayKeys, key)
		}
		sort.Strings(replayKeys)
		for _, key := range replayKeys {
			sha, err := normalizeMissileGuidanceReplaySHA(key)
			if err != nil {
				return err
			}
			if _, exists := normalized[sha]; exists {
				return fmt.Errorf("missile guidance has duplicate replay SHA %q", sha)
			}
			replay := table.Replays[key]
			if replay.WeaponIDs == nil {
				return fmt.Errorf("missile guidance replay %q is missing weapon_ids", key)
			}
			ids := make([]string, 0, len(replay.WeaponIDs))
			for id := range replay.WeaponIDs {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				if err := validateMissileGuidanceWeaponID(id); err != nil {
					return fmt.Errorf("missile guidance replay %q: %w", key, err)
				}
				if err := validateMissileGuidanceEntry("replays["+key+"].weapon_ids["+id+"]", replay.WeaponIDs[id]); err != nil {
					return err
				}
			}
			normalized[sha] = replay
		}
		table.Replays = normalized
	}
	return nil
}

func validateMissileGuidanceEntry(label string, entry missileGuidanceEntry) error {
	switch entry.Guidance {
	case "ARH", "IR", "SARH":
	default:
		return fmt.Errorf("missile guidance %s has invalid guidance %q", label, entry.Guidance)
	}
	switch entry.Confidence {
	case "confirmed", "high":
	default:
		return fmt.Errorf("missile guidance %s has invalid confidence %q", label, entry.Confidence)
	}
	return validateMissileGuidanceText(label+".source", entry.Source, 512)
}

func normalizeMissileGuidanceReplaySHA(key string) (string, error) {
	if len(key) != 64 {
		return "", fmt.Errorf("missile guidance replay SHA %q must be 64 hex characters", key)
	}
	if _, err := hex.DecodeString(key); err != nil {
		return "", fmt.Errorf("missile guidance replay SHA %q is not hexadecimal", key)
	}
	return strings.ToLower(key), nil
}

func validateMissileGuidanceWeaponID(key string) error {
	value, err := strconv.ParseUint(key, 10, 32)
	if err != nil {
		return fmt.Errorf("weapon ID %q is not a uint32", key)
	}
	if strconv.FormatUint(value, 10) != key {
		return fmt.Errorf("weapon ID %q is not canonical decimal", key)
	}
	return nil
}

func validateMissileGuidanceInternalName(name string) error {
	if name == "" {
		return fmt.Errorf("missile guidance contains an empty internal name")
	}
	if len(name) > 128 {
		return fmt.Errorf("missile guidance internal name exceeds 128 bytes")
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("missile guidance internal name is not valid UTF-8")
	}
	if strings.TrimSpace(name) != name || strings.ContainsAny(name, " \t\r\n") {
		return fmt.Errorf("missile guidance internal name %q is not exact", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("missile guidance internal name %q contains a control character", name)
		}
	}
	return nil
}

func validateMissileGuidanceText(label, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("missile guidance %s is empty", label)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("missile guidance %s exceeds %d bytes", label, maxBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("missile guidance %s has surrounding whitespace", label)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("missile guidance %s is not valid UTF-8", label)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("missile guidance %s contains a control character", label)
		}
	}
	return nil
}

// applyMissileGuidance resolves guidance for every missile after model names
// have been established. It does not alter manual names or model inference.
func applyMissileGuidance(tracks []*track, table *missileGuidanceTable) int {
	return applyMissileGuidanceForReplay(tracks, table, "")
}

// applyMissileGuidanceForReplay enables replay-scoped weapon-ID evidence only
// when replaySHA exactly matches a key in the table. An empty or unknown SHA
// never enables scoped rows.
func applyMissileGuidanceForReplay(tracks []*track, table *missileGuidanceTable, replaySHA string) int {
	scoped := table.weaponIDsForReplay(replaySHA)
	resolved := 0
	for _, tr := range tracks {
		if tr == nil || !tr.key.missile {
			continue
		}
		tr.missileGuidance = resolveMissileGuidance(tr, table, scoped)
		if tr.missileGuidance != nil {
			resolved++
		}
	}
	return resolved
}

func (table *missileGuidanceTable) weaponIDsForReplay(replaySHA string) map[string]missileGuidanceEntry {
	if table == nil || table.Replays == nil || replaySHA == "" {
		return nil
	}
	sha := strings.ToLower(strings.TrimSpace(replaySHA))
	if len(sha) != 64 {
		return nil
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return nil
	}
	replay, ok := table.Replays[sha]
	if !ok {
		return nil
	}
	return replay.WeaponIDs
}

func resolveMissileGuidance(tr *track, table *missileGuidanceTable, scopedIDs map[string]missileGuidanceEntry) *missileGuidanceInfo {
	if tr == nil || !tr.key.missile || table == nil {
		return nil
	}

	template := ""
	if tr.projectileSource != nil {
		template = tr.projectileSource.Template
	}
	hints := missileTemplateGuidanceHints(template)

	var scopedEntry *missileGuidanceEntry
	if scopedIDs != nil {
		if weaponID, ok := missileGuidanceWeaponID(tr); ok {
			if entry, ok := scopedIDs[strconv.FormatUint(uint64(weaponID), 10)]; ok {
				entryCopy := entry
				scopedEntry = &entryCopy
			}
		}
	}

	modelGuidance := ""
	modelSource := ""
	modelConfidence := ""
	if tr.missileModel != nil && tr.missileModel.InternalName != "" {
		if entry, ok := table.Models[tr.missileModel.InternalName]; ok {
			switch tr.missileModel.Confidence {
			case "Matched", "confirmed":
				modelGuidance, modelSource, modelConfidence = entry.Guidance, entry.Source, entry.Confidence
			case "high":
				// A high-confidence model identity can establish guidance, but it
				// cannot raise that evidence to the guidance table's confirmed level.
				modelGuidance, modelSource, modelConfidence = entry.Guidance, entry.Source, "high"
			case "Inferred":
				// A propagated model identity keeps its lower confidence even if
				// the table row itself is confirmed.
				modelGuidance, modelSource, modelConfidence = entry.Guidance, entry.Source, "inferred"
			}
		}
	}

	constraints := make([][]string, 0, 3)
	if scopedEntry != nil {
		constraints = append(constraints, []string{scopedEntry.Guidance})
	}
	if modelGuidance != "" {
		constraints = append(constraints, []string{modelGuidance})
	}
	if len(hints) == 1 {
		constraints = append(constraints, hints)
	}
	plainAAM := missilePlainRocketAAM(tr)
	if plainAAM {
		constraints = append(constraints, []string{"IR", "SARH"})
	}

	if len(constraints) == 0 {
		return &missileGuidanceInfo{Type: "Unknown", Source: "None", Confidence: "unknown"}
	}
	intersection := missileGuidanceIntersection(constraints)
	if len(intersection) == 1 {
		selected := intersection[0]
		if scopedEntry != nil && scopedEntry.Guidance == selected {
			return &missileGuidanceInfo{Type: selected, Source: scopedEntry.Source, Confidence: scopedEntry.Confidence}
		}
		if modelGuidance == selected {
			return &missileGuidanceInfo{Type: selected, Source: modelSource, Confidence: modelConfidence}
		}
		if len(hints) == 1 && hints[0] == selected {
			return &missileGuidanceInfo{
				Type:       selected,
				Source:     "Template" + selected + "Shell",
				Confidence: "high",
			}
		}
	}

	candidates := missileGuidanceCandidateList(missileGuidanceUnion(constraints))
	if len(constraints) == 1 && plainAAM {
		return &missileGuidanceInfo{
			Type:       "Unknown",
			Source:     "TemplateNonARH",
			Confidence: "unknown",
			Candidates: candidates,
		}
	}
	source := "TemplateModelConflict"
	if scopedEntry != nil {
		source = scopedEntry.Source
	}
	return &missileGuidanceInfo{
		Type:       "Unknown",
		Source:     source,
		Confidence: "conflict",
		Candidates: candidates,
	}
}

func missileGuidanceWeaponID(tr *track) (uint32, bool) {
	if tr == nil {
		return 0, false
	}
	if tr.projectileSource != nil {
		return missileWeaponIDFromLeading(tr.projectileSource.LeadingULEB)
	}
	if tr.missileModel != nil && tr.missileModel.HasWeaponID {
		return tr.missileModel.WeaponID, true
	}
	return 0, false
}

func missileGuidanceIntersection(sets [][]string) []string {
	counts := map[string]int{}
	for _, set := range sets {
		seen := map[string]bool{}
		for _, value := range set {
			if !seen[value] {
				counts[value]++
				seen[value] = true
			}
		}
	}
	result := make([]string, 0)
	for _, value := range []string{"ARH", "IR", "SARH"} {
		if counts[value] == len(sets) {
			result = append(result, value)
		}
	}
	return result
}

func missileGuidanceUnion(sets [][]string) []string {
	set := map[string]bool{}
	for _, values := range sets {
		for _, value := range values {
			set[value] = true
		}
	}
	return missileGuidanceOrderedTypes(set)
}

func missileTemplateGuidanceHints(template string) []string {
	found := map[string]bool{}
	for _, token := range strings.FieldsFunc(strings.ToLower(template), func(r rune) bool {
		return r == '+' || r == '/' || r == ',' || r == ';' || r == '|' || unicode.IsSpace(r)
	}) {
		switch token {
		case "arh_shell":
			found["ARH"] = true
		}
	}
	return missileGuidanceOrderedTypes(found)
}

// A plain Rocket with the AAM trigger is non-ARH evidence only. Both IR and
// SARH remain possible, so the type stays Unknown and the candidates are
// recorded explicitly.
func missilePlainRocketAAM(tr *track) bool {
	if tr == nil || tr.projectileSource == nil {
		return false
	}
	if strings.ToLower(strings.TrimSpace(tr.projectileSource.Template)) != "rocket" {
		return false
	}
	return tr.projectileSource.WeaponRef>>16 == missileNameArmamentTriggerAAM
}

func missileGuidanceOrderedTypes(set map[string]bool) []string {
	result := make([]string, 0, len(set))
	for _, guidance := range []string{"ARH", "IR", "SARH"} {
		if set[guidance] {
			result = append(result, guidance)
		}
	}
	return result
}

func missileGuidanceCandidateList(values []string) string {
	set := map[string]bool{}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return strings.Join(missileGuidanceOrderedTypes(set), " / ")
}

func missileGuidanceFallbackName(tr *track) string {
	if tr == nil || tr.missileGuidance == nil {
		return ""
	}
	info := tr.missileGuidance
	if info.Type != "IR" && info.Type != "SARH" {
		return ""
	}
	switch info.Confidence {
	case "confirmed", "high", "inferred":
		return "AAM (" + info.Type + ")"
	default:
		return ""
	}
}

func missileGuidanceProperties(tr *track) []string {
	if tr == nil || tr.missileGuidance == nil {
		return nil
	}
	info := tr.missileGuidance
	props := []string{
		"WT_GuidanceType=" + info.Type,
		"WT_GuidanceSource=" + escapeACMIValue(info.Source),
		"WT_GuidanceConfidence=" + info.Confidence,
	}
	if info.Candidates != "" {
		props = append(props, "WT_GuidanceCandidates="+escapeACMIValue(info.Candidates))
	}
	return props
}

func missileGuidanceCSVValues(tr *track) []string {
	if tr == nil || tr.missileGuidance == nil {
		return []string{"", "", "", ""}
	}
	info := tr.missileGuidance
	return []string{info.Type, info.Source, info.Confidence, info.Candidates}
}

func tracksHaveMissileGuidance(tracks []*track) bool {
	for _, tr := range tracks {
		if tr != nil && tr.key.missile && tr.missileGuidance != nil {
			return true
		}
	}
	return false
}
