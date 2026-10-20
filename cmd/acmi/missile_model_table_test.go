package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const missileModelTestSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func missileModelTestTable(entries map[string]missileModelEntry) *missileModelTable {
	return &missileModelTable{
		Schema: 1, GeneratedFrom: []string{"unit-test"},
		Replays: map[string]missileModelReplay{
			missileModelTestSHA: {WeaponIDs: entries},
		},
	}
}

func missileModelTestEntry(model string, weaponID uint32, confidence string, candidates ...string) map[string]missileModelEntry {
	internal := strings.ToLower(strings.ReplaceAll(model, " ", "_"))
	return map[string]missileModelEntry{
		strconv.FormatUint(uint64(weaponID), 10): {
			Model: model, InternalName: internal, Confidence: confidence,
			Source: "unit-test model row", Candidates: append([]string{}, candidates...),
		},
	}
}

func TestMissileModelsRequireExactReplaySHA(t *testing.T) {
	table := missileModelTestTable(missileModelTestEntry("PL-5E II", 60, "high"))
	correct := missileNameTestTrack(1, missileNameTestSource(77, 1000, 189, 8, "rocket"), true)
	applyMissileModelNames([]*track{correct}, nil)
	if adopted := applyMissileModelsForReplay([]*track{correct}, table, missileModelTestSHA); adopted != 1 {
		t.Fatalf("correct-SHA adopted = %d, want 1", adopted)
	}
	if got := correct.missileModel; got == nil || got.Model != "PL-5E II" ||
		got.Source != "ReplayWeaponIDFromModelTable" || got.Confidence != "high" {
		t.Fatalf("correct-SHA model = %+v", got)
	}
	if correct.name != "PL-5E II" {
		t.Fatalf("correct-SHA name = %q", correct.name)
	}
	props := strings.Join(missileModelProperties(correct), ",")
	for _, want := range []string{"WT_WeaponID=60", "WT_LeadingULEB=189", "WT_MissileModel=PL-5E II"} {
		if !strings.Contains(props, want) {
			t.Fatalf("correct-SHA properties %q missing %q", props, want)
		}
	}

	for _, wrong := range []string{"", strings.Repeat("b", 64), "not-a-sha"} {
		t.Run("wrong "+wrong, func(t *testing.T) {
			tr := missileNameTestTrack(2, missileNameTestSource(77, 1000, 189, 8, "rocket"), true)
			applyMissileModelNames([]*track{tr}, nil)
			if adopted := applyMissileModelsForReplay([]*track{tr}, table, wrong); adopted != 0 {
				t.Fatalf("wrong-SHA adopted = %d", adopted)
			}
			if tr.missileModel.Model != "" || tr.name != "AAM (unknown)" || tr.missileModel.ModelConflict != "" {
				t.Fatalf("wrong-SHA changed baseline: model=%+v name=%q", tr.missileModel, tr.name)
			}
		})
	}
}

func TestMissileModelsLowAndMediumConfidenceNeverName(t *testing.T) {
	for _, confidence := range []string{"medium", "low"} {
		t.Run(confidence, func(t *testing.T) {
			table := missileModelTestTable(missileModelTestEntry("PL-5E II", 53, confidence, "PL-5E II", "AAM-3"))
			tr := missileNameTestTrack(1, missileNameTestSource(77, 1000, 182, 8, "rocket"), true)
			applyMissileModelNames([]*track{tr}, nil)
			applyMissileModelsForReplay([]*track{tr}, table, missileModelTestSHA)
			info := tr.missileModel
			if info.Model != "" || info.InternalName != "" || tr.name != "AAM (unknown)" {
				t.Fatalf("candidate evidence became a name: model=%+v name=%q", info, tr.name)
			}
			if info.Confidence != confidence || strings.Join(info.ModelCandidates, " / ") != "PL-5E II / AAM-3" {
				t.Fatalf("candidate metadata = %+v", info)
			}
			props := strings.Join(missileModelProperties(tr), ",")
			if !strings.Contains(props, "WT_MissileModelCandidates=PL-5E II / AAM-3") ||
				strings.Contains(props, "WT_MissileModel=PL-5E II") {
				t.Fatalf("candidate properties = %q", props)
			}
		})
	}
}

func TestMissileModelsDoNotOverwriteExistingKillEvidence(t *testing.T) {
	tr := missileNameTestTrack(1, missileNameTestSource(77, 1020, 161, 8, "rocket+arh_shell"), true)
	applyMissileModelNames([]*track{tr}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1020, KillerUID: 77, Weapon: "cn_pl12"},
	}})
	if tr.missileModel.Model != "PL-12" || tr.missileModel.Confidence != "Matched" {
		t.Fatalf("kill evidence setup = %+v", tr.missileModel)
	}
	table := missileModelTestTable(missileModelTestEntry("AIM-120A", 32, "high"))
	applyMissileModelsForReplay([]*track{tr}, table, missileModelTestSHA)
	info := tr.missileModel
	if info.Model != "PL-12" || info.InternalName != "cn_pl12" || info.Source != "KillReportMatched" ||
		info.Confidence != "Matched" || tr.name != "PL-12" {
		t.Fatalf("kill evidence was overwritten: %+v name=%q", info, tr.name)
	}
	if !strings.Contains(info.ModelConflict, "existing=PL-12") ||
		!strings.Contains(info.ModelConflict, "table=AIM-120A") ||
		strings.Join(info.ModelCandidates, " / ") != "PL-12 / AIM-120A" {
		t.Fatalf("conflict metadata = %+v", info)
	}
}

func TestMissileModelNamesRemainHighestPriority(t *testing.T) {
	tr := missileNameTestTrack(42, missileNameTestSource(77, 1000, 189, 8, "rocket"), true)
	applyMissileModelNames([]*track{tr}, nil)
	applyMissileModelsForReplay([]*track{tr}, missileModelTestTable(missileModelTestEntry("PL-5E II", 60, "high")), missileModelTestSHA)
	names := map[string]string{"missile:42": "Manual missile"}
	if got := trackName(tr, nil, names); got != "Manual missile" {
		t.Fatalf("manual name = %q, want Manual missile", got)
	}
}

func TestMissileModelsOffRestoresACMIAndCSV(t *testing.T) {
	if table, err := loadMissileModels("off"); err != nil || table != nil {
		t.Fatalf("off load = %+v/%v", table, err)
	}
	tr := missileNameTestTrack(42, missileNameTestSource(77, 1000, 189, 8, "rocket"), true)
	applyMissileModelNames([]*track{tr}, nil)
	tracks := []*track{tr}
	beforeACMI := stripReferenceTime(renderTestACMI(t, tracks, nil, nil))
	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.csv")
	if err := writeTrackList(beforePath, tracks, nil, nil); err != nil {
		t.Fatal(err)
	}
	beforeCSV, err := os.ReadFile(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := applyMissileModelsForReplay(tracks, nil, ""); got != 0 {
		t.Fatalf("off adopted %d models", got)
	}
	afterACMI := stripReferenceTime(renderTestACMI(t, tracks, nil, nil))
	if beforeACMI != afterACMI {
		t.Fatal("off changed ACMI output")
	}
	afterPath := filepath.Join(dir, "after.csv")
	if err := writeTrackList(afterPath, tracks, nil, nil); err != nil {
		t.Fatal(err)
	}
	afterCSV, err := os.ReadFile(afterPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeCSV, afterCSV) {
		t.Fatalf("off changed CSV output:\nbefore=%s\nafter=%s", beforeCSV, afterCSV)
	}
}

func TestMissileGuidanceAcceptsHighModelTableEvidence(t *testing.T) {
	tr := missileNameTestTrack(1, missileNameTestSource(77, 1000, 189, 8, "rocket"), true)
	applyMissileModelNames([]*track{tr}, nil)
	applyMissileModelsForReplay([]*track{tr}, missileModelTestTable(missileModelTestEntry("AAM-3", 60, "high")), missileModelTestSHA)
	guidance := guidanceTestTable(map[string]missileGuidanceEntry{
		"aam-3": {Guidance: "IR", Source: "datamine/aam-3", Confidence: "confirmed"},
	})
	applyMissileGuidance([]*track{tr}, guidance)
	info := tr.missileGuidance
	if info == nil || info.Type != "IR" || info.Source != "datamine/aam-3" || info.Confidence != "high" {
		t.Fatalf("high model guidance = %+v", info)
	}
}

func TestMissileModelTableAliasesExtendKillCorrelationOnlyWhenEnabled(t *testing.T) {
	table := missileModelTestTable(nil)
	table.Aliases = map[string]missileModelAliasEntry{
		"us_aim_9m": {Model: "AIM-9M", InternalName: "us_aim9m_sidewinder", Confidence: "high", Source: "loadout/datamine"},
	}
	kills := &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1020, KillerUID: 77, Weapon: "US_AIM_9M_DEFAULT"},
	}}
	direct := missileNameTestTrack(1, missileNameTestSource(77, 1020, 161, 8, "rocket"), true)
	propagated := missileNameTestTrack(2, missileNameTestSource(88, 3000, 161, 8, "rocket"), true)
	applyMissileModelNamesWithAliases([]*track{direct, propagated}, kills, table.killAliases())
	if direct.missileModel.Model != "AIM-9M" || direct.missileModel.Confidence != "Matched" {
		t.Fatalf("alias direct evidence = %+v", direct.missileModel)
	}
	if propagated.missileModel.Model != "AIM-9M" || propagated.missileModel.Confidence != "Inferred" {
		t.Fatalf("alias propagation = %+v", propagated.missileModel)
	}

	offDirect := missileNameTestTrack(3, missileNameTestSource(77, 1020, 161, 8, "rocket"), true)
	offPropagated := missileNameTestTrack(4, missileNameTestSource(88, 3000, 161, 8, "rocket"), true)
	applyMissileModelNames([]*track{offDirect, offPropagated}, kills)
	if offDirect.missileModel.Model != "" || offPropagated.missileModel.Model != "" {
		t.Fatalf("off alias leaked into kill correlation: %+v / %+v", offDirect.missileModel, offPropagated.missileModel)
	}
}

func TestMissileModelCSVColumnsOnlyWhenTableEnabled(t *testing.T) {
	tr := missileNameTestTrack(42, missileNameTestSource(77, 1000, 189, 8, "rocket"), true)
	applyMissileModelNames([]*track{tr}, nil)
	applyMissileModelsForReplay([]*track{tr}, missileModelTestTable(missileModelTestEntry("PL-5E II", 60, "high")), missileModelTestSHA)
	path := filepath.Join(t.TempDir(), "list.csv")
	if err := writeTrackList(path, []*track{tr}, nil, nil); err != nil {
		t.Fatal(err)
	}
	rows := readTestCSV(t, path)
	if got := strings.Join(rows[0][10:15], ","); got != "weapon_id,model,model_source,model_confidence,candidates" {
		t.Fatalf("model columns = %q", got)
	}
	if got := strings.Join(rows[1][10:15], "|"); got != "60|PL-5E II|ReplayWeaponIDFromModelTable|high|" {
		t.Fatalf("model row = %q", got)
	}
}

func TestParseMissileModelsRejectsInvalidTables(t *testing.T) {
	valid := `{"schema":1,"generated_from":["test"],"replays":{"` + missileModelTestSHA + `":{"weapon_ids":{"60":{"model":"AAM-3","internal_name":"jp_aam3","confidence":"high","source":"asset","candidates":[]}}}}}`
	if _, err := parseMissileModelsJSON([]byte(valid)); err != nil {
		t.Fatalf("valid table rejected: %v", err)
	}
	validAlias := `{"schema":1,"generated_from":["test"],"aliases":{"us_aim_9m":{"model":"AIM-9M","internal_name":"us_aim9m_sidewinder","confidence":"high","source":"asset"}},"replays":{}}`
	if _, err := parseMissileModelsJSON([]byte(validAlias)); err != nil {
		t.Fatalf("valid alias table rejected: %v", err)
	}

	cases := map[string]string{
		"missing schema":            `{"generated_from":["test"],"replays":{}}`,
		"wrong schema":              `{"schema":2,"generated_from":["test"],"replays":{}}`,
		"missing source":            `{"schema":1,"generated_from":["test"],"replays":{"` + missileModelTestSHA + `":{"weapon_ids":{"60":{"model":"x","internal_name":"x","confidence":"high","source":"","candidates":[]}}}}}`,
		"bad confidence":            `{"schema":1,"generated_from":["test"],"replays":{"` + missileModelTestSHA + `":{"weapon_ids":{"60":{"model":"x","internal_name":"x","confidence":"certain","source":"s","candidates":[]}}}}}`,
		"missing candidates":        `{"schema":1,"generated_from":["test"],"replays":{"` + missileModelTestSHA + `":{"weapon_ids":{"60":{"model":"x","internal_name":"x","confidence":"high","source":"s"}}}}}`,
		"medium without candidates": `{"schema":1,"generated_from":["test"],"replays":{"` + missileModelTestSHA + `":{"weapon_ids":{"60":{"model":"x","internal_name":"x","confidence":"medium","source":"s","candidates":[]}}}}}`,
		"bad ID":                    `{"schema":1,"generated_from":["test"],"replays":{"` + missileModelTestSHA + `":{"weapon_ids":{"060":{"model":"x","internal_name":"x","confidence":"high","source":"s","candidates":[]}}}}}`,
		"bad SHA":                   `{"schema":1,"generated_from":["test"],"replays":{"abc":{"weapon_ids":{}}}}`,
		"bad alias confidence":      `{"schema":1,"generated_from":["test"],"aliases":{"x":{"model":"x","internal_name":"x","confidence":"medium","source":"s"}},"replays":{}}`,
		"duplicate alias":           `{"schema":1,"generated_from":["test"],"aliases":{"X":{"model":"x","internal_name":"x","confidence":"high","source":"s"},"x":{"model":"x","internal_name":"x","confidence":"high","source":"s"}},"replays":{}}`,
		"alias unknown field":       `{"schema":1,"generated_from":["test"],"aliases":{"x":{"model":"x","internal_name":"x","confidence":"high","source":"s","candidates":[]}},"replays":{}}`,
		"trailing JSON":             valid + `{}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMissileModelsJSON([]byte(data)); err == nil {
				t.Fatalf("invalid table accepted: %s", data)
			}
		})
	}
	oversized := append([]byte(valid), bytes.Repeat([]byte(" "), missileModelTableMaxFileBytes)...)
	if _, err := parseMissileModelsJSON(oversized); err == nil {
		t.Fatal("oversized table accepted")
	}
}

func TestEmbeddedMissileModelsTableIsValid(t *testing.T) {
	table, err := parseMissileModelsJSON(embeddedMissileModelsJSON)
	if err != nil {
		t.Fatalf("embedded missile models table is invalid: %v", err)
	}
	if table.Schema != 1 || table.Replays == nil {
		t.Fatalf("embedded missile models table = %+v", table)
	}
}

func TestEmbeddedMissileModelsExpectedCoverageAndFallback(t *testing.T) {
	table, err := parseMissileModelsJSON(embeddedMissileModelsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Aliases) != 18 {
		t.Fatalf("embedded aliases = %d, want 18", len(table.Aliases))
	}
	replay := table.Replays["c0fcd26febf9f0ec75765337a841c1d55d089cd13b45740f0cbd41c190f418b2"]
	if len(replay.WeaponIDs) != 25 {
		t.Fatalf("embedded WeaponIDs = %d, want 25", len(replay.WeaponIDs))
	}
	wantHigh := map[string]string{"35": "PL-12", "60": "PL-5E II", "69": "AIM-120A", "157": "AAM-3"}
	for id, model := range wantHigh {
		entry := replay.WeaponIDs[id]
		if entry.Confidence != "high" || entry.Model != model {
			t.Fatalf("high row %s = %+v, want %s", id, entry, model)
		}
	}
	for _, id := range []string{"53", "99", "159"} {
		entry := replay.WeaponIDs[id]
		if entry.Confidence != "medium" || entry.Model != "" || len(entry.Candidates) == 0 {
			t.Fatalf("medium fallback row %s = %+v", id, entry)
		}
	}
	counts := map[string]int{}
	for _, entry := range replay.WeaponIDs {
		counts[entry.Confidence]++
		if (entry.Confidence == "medium" || entry.Confidence == "low") &&
			(entry.Model != "" || len(entry.Candidates) == 0) {
			t.Fatalf("non-specific row names a model or has no candidates: %+v", entry)
		}
	}
	if counts["confirmed"]+counts["high"] != 15 || counts["medium"] != 3 || counts["low"] != 7 {
		t.Fatalf("embedded confidence counts = %+v", counts)
	}
}
