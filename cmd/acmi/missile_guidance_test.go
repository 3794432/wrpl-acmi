package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func guidanceTestTable(models map[string]missileGuidanceEntry) *missileGuidanceTable {
	return &missileGuidanceTable{Schema: 1, GeneratedFrom: []string{"unit-test"}, Models: models}
}

func guidanceTestTrack(id uint64, template, internal, modelConfidence string) *track {
	tr := missileNameTestTrack(id, missileNameTestSource(77, 2000, 0, 8, template), true)
	if internal != "" || modelConfidence != "" {
		tr.missileModel = &missileModelInfo{
			Model: "Test model", InternalName: internal,
			Source: "KillReportMatched", Confidence: modelConfidence,
		}
	}
	return tr
}

func TestMissileGuidanceTemplateARHUsesMissedMissileConstruction(t *testing.T) {
	tr := guidanceTestTrack(1, "rocket+arh_shell", "", "")
	tr.name = "AAM (ARH)"
	if got := applyMissileGuidance([]*track{tr}, guidanceTestTable(nil)); got != 1 {
		t.Fatalf("resolved missiles = %d, want 1", got)
	}
	info := tr.missileGuidance
	if info == nil || info.Type != "ARH" || info.Source != "TemplateARHShell" ||
		info.Confidence != "high" || info.Candidates != "" {
		t.Fatalf("ARH template guidance = %+v", info)
	}
}

func TestMissileGuidanceExactInternalNames(t *testing.T) {
	table := guidanceTestTable(map[string]missileGuidanceEntry{
		"jp_aam3":   {Guidance: "IR", Source: "datamine/jp_aam3", Confidence: "confirmed"},
		"su_r27er":  {Guidance: "SARH", Source: "datamine/su_r27er", Confidence: "high"},
		"cn_pl12":   {Guidance: "ARH", Source: "kill/cn_pl12", Confidence: "confirmed"},
		"us_aim_9m": {Guidance: "IR", Source: "datamine/us_aim_9m", Confidence: "high"},
	})
	tests := []struct {
		name           string
		template       string
		internal       string
		wantType       string
		wantSource     string
		wantConf       string
		wantCandidates string
	}{
		{name: "IR exact", template: "rocket", internal: "jp_aam3", wantType: "IR", wantSource: "datamine/jp_aam3", wantConf: "confirmed"},
		{name: "SARH exact", template: "rocket", internal: "su_r27er", wantType: "SARH", wantSource: "datamine/su_r27er", wantConf: "high"},
		{name: "ARH exact", template: "rocket+arh_shell", internal: "cn_pl12", wantType: "ARH", wantSource: "kill/cn_pl12", wantConf: "confirmed"},
		{name: "case sensitive", template: "rocket", internal: "JP_AAM3", wantType: "Unknown", wantSource: "TemplateNonARH", wantConf: "unknown", wantCandidates: "IR / SARH"},
		{name: "no suffix match", template: "rocket", internal: "jp_aam3_extra", wantType: "Unknown", wantSource: "TemplateNonARH", wantConf: "unknown", wantCandidates: "IR / SARH"},
		{name: "no prefix match", template: "rocket", internal: "jp", wantType: "Unknown", wantSource: "TemplateNonARH", wantConf: "unknown", wantCandidates: "IR / SARH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := guidanceTestTrack(1, tt.template, tt.internal, "Matched")
			applyMissileGuidance([]*track{tr}, table)
			info := tr.missileGuidance
			if info == nil || info.Type != tt.wantType || info.Source != tt.wantSource ||
				info.Confidence != tt.wantConf || info.Candidates != tt.wantCandidates {
				t.Fatalf("guidance = %+v, want type=%s source=%s confidence=%s", info, tt.wantType, tt.wantSource, tt.wantConf)
			}
		})
	}
}

func TestMissileGuidanceBareRocketIsUnknown(t *testing.T) {
	// ULEB 223 is an IR row in the sample replay's weapon-ID table. Guidance
	// must remain unknown because the Rocket construction itself is generic.
	tr := missileNameTestTrack(1, missileNameTestSource(77, 2000, 223, 8, "rocket"), true)
	tr.name = "AAM (unknown)"
	applyMissileGuidance([]*track{tr}, guidanceTestTable(nil))
	info := tr.missileGuidance
	if info == nil || info.Type != "Unknown" || info.Source != "TemplateNonARH" ||
		info.Confidence != "unknown" || info.Candidates != "IR / SARH" {
		t.Fatalf("bare rocket guidance = %+v", info)
	}
	if name := trackName(tr, nil, nil); name != "AAM (unknown)" {
		t.Fatalf("bare rocket fallback name = %q", name)
	}
}

func TestMissileGuidanceTemplateModelConflictIsNotConfirmed(t *testing.T) {
	table := guidanceTestTable(map[string]missileGuidanceEntry{
		"jp_aam3": {Guidance: "IR", Source: "datamine/jp_aam3", Confidence: "confirmed"},
		"cn_pl12": {Guidance: "ARH", Source: "kill/cn_pl12", Confidence: "confirmed"},
	})
	conflict := guidanceTestTrack(1, "rocket+arh_shell", "jp_aam3", "Matched")
	agree := guidanceTestTrack(2, "rocket+arh_shell", "cn_pl12", "Matched")
	applyMissileGuidance([]*track{conflict, agree}, table)
	if info := conflict.missileGuidance; info == nil || info.Type != "Unknown" ||
		info.Source != "TemplateModelConflict" || info.Confidence != "conflict" ||
		info.Candidates != "ARH / IR" {
		t.Fatalf("conflicting guidance = %+v", info)
	}
	if info := agree.missileGuidance; info == nil || info.Type != "ARH" || info.Confidence != "confirmed" {
		t.Fatalf("agreeing guidance = %+v", info)
	}
}

func TestMissileGuidancePlainRocketAAMListsCandidates(t *testing.T) {
	tr := missileNameTestTrack(1, missileNameTestSource(77, 2000, 0, 8, "rocket"), true)
	applyMissileGuidance([]*track{tr}, guidanceTestTable(nil))
	info := tr.missileGuidance
	if info == nil || info.Type != "Unknown" || info.Source != "TemplateNonARH" ||
		info.Confidence != "unknown" || info.Candidates != "IR / SARH" {
		t.Fatalf("ambiguous template guidance = %+v", info)
	}
	props := strings.Join(missileGuidanceProperties(tr), ",")
	if !strings.Contains(props, "WT_GuidanceType=Unknown") ||
		!strings.Contains(props, "WT_GuidanceCandidates=IR / SARH") {
		t.Fatalf("ambiguous properties = %q", props)
	}

	agm := missileNameTestTrack(2, missileNameTestSource(77, 2000, 0, 7, "rocket"), true)
	applyMissileGuidance([]*track{agm}, guidanceTestTable(nil))
	if info := agm.missileGuidance; info == nil || info.Type != "Unknown" ||
		info.Source != "None" || info.Confidence != "unknown" || info.Candidates != "" {
		t.Fatalf("plain AGM guidance = %+v", info)
	}
}

func TestMissileGuidanceInheritsInferredModelConfidence(t *testing.T) {
	table := guidanceTestTable(map[string]missileGuidanceEntry{
		"cn_pl12":   {Guidance: "ARH", Source: "kill/cn_pl12", Confidence: "confirmed"},
		"jp_aam3":   {Guidance: "IR", Source: "datamine/jp_aam3", Confidence: "confirmed"},
		"us_aim_9m": {Guidance: "IR", Source: "datamine/us_aim_9m", Confidence: "high"},
	})
	inferred := guidanceTestTrack(1, "rocket+arh_shell", "cn_pl12", "Inferred")
	matchedHigh := guidanceTestTrack(2, "rocket", "us_aim_9m", "Matched")
	applyMissileGuidance([]*track{inferred, matchedHigh}, table)
	if info := inferred.missileGuidance; info == nil || info.Confidence != "inferred" || info.Type != "ARH" {
		t.Fatalf("inferred guidance = %+v", info)
	}
	if info := matchedHigh.missileGuidance; info == nil || info.Confidence != "high" {
		t.Fatalf("matched high guidance = %+v", info)
	}

	fallback := guidanceTestTrack(3, "rocket", "jp_aam3", "Inferred")
	fallback.name = "AAM (unknown)"
	applyMissileGuidance([]*track{fallback}, table)
	if name := trackName(fallback, nil, nil); name != "AAM (IR)" {
		t.Fatalf("inferred IR fallback name = %q, want AAM (IR)", name)
	}
}

func TestMissileGuidanceFallbackNameOnlyForUnnamedAAM(t *testing.T) {
	table := guidanceTestTable(map[string]missileGuidanceEntry{
		"jp_aam3": {Guidance: "IR", Source: "datamine/jp_aam3", Confidence: "confirmed"},
	})
	unnamed := guidanceTestTrack(1, "rocket", "jp_aam3", "Matched")
	unnamed.name = "AAM (unknown)"
	identified := guidanceTestTrack(2, "rocket", "jp_aam3", "Matched")
	identified.name = "AAM-3"
	agm := guidanceTestTrack(3, "rocket", "jp_aam3", "Matched")
	agm.name = "AGM (unknown)"
	applyMissileGuidance([]*track{unnamed, identified, agm}, table)
	if got := trackName(unnamed, nil, nil); got != "AAM (IR)" {
		t.Fatalf("unnamed AAM = %q", got)
	}
	if got := trackName(identified, nil, nil); got != "AAM-3" {
		t.Fatalf("identified AAM was renamed to %q", got)
	}
	if got := trackName(agm, nil, nil); got != "AGM (unknown)" {
		t.Fatalf("AGM was renamed to %q", got)
	}
}

func TestMissileGuidanceManualNameAndINSRemainUnchanged(t *testing.T) {
	first := missileINSTestSample(0.3125, true, true)
	first.t, first.x, first.y, first.z = 1000, 1, 2, 3
	tr := &track{
		key: trackKey{missile: true, id: 42}, name: "AAM (unknown)",
		projectileSource: &projectiles.Projectile{Template: "rocket+arh_shell"},
		samples:          []acmiSample{first, {t: 1100, x: 11, y: 2, z: 3}},
	}
	applyMissileGuidance([]*track{tr}, guidanceTestTable(nil))
	data := renderTestACMI(t, []*track{tr}, nil, map[string]string{"missile:42": "Custom missile"})
	for _, want := range []string{
		"Name=Custom missile,CallSign=Custom missile",
		"WT_GuidanceType=ARH", "WT_GuidanceSource=TemplateARHShell", "WT_GuidanceConfidence=high",
		"WT_InertialTime=0.312500", "INS 0.31 s",
	} {
		if !strings.Contains(data, want) {
			t.Fatalf("ACMI missing %q:\n%s", want, data)
		}
	}
	if got := strings.Count(data, "WT_GuidanceType="); got != 1 {
		t.Fatalf("guidance fields written %d times, want once at creation", got)
	}
	lastFrame := data[strings.LastIndex(data, "#0.100"):]
	if strings.Contains(lastFrame, "WT_Guidance") {
		t.Fatalf("guidance repeated on a later frame:\n%s", lastFrame)
	}
}

func TestMissileGuidanceOffIsNoOp(t *testing.T) {
	if table, err := loadMissileGuidance("off"); err != nil || table != nil {
		t.Fatalf("off load = %+v/%v, want nil/nil", table, err)
	}
	tr := &track{
		key: trackKey{missile: true, id: 7}, name: "AAM (unknown)",
		projectileSource: &projectiles.Projectile{Template: "rocket+arh_shell"},
		samples:          []acmiSample{{t: 1000, x: 1}, {t: 1100, x: 2}},
	}
	tracks := []*track{tr}
	before := renderTestACMI(t, tracks, nil, nil)
	if got := applyMissileGuidance(tracks, nil); got != 0 {
		t.Fatalf("off resolved %d missiles", got)
	}
	after := renderTestACMI(t, tracks, nil, nil)
	if stripReferenceTime(radarTestStableRecording(before)) != stripReferenceTime(radarTestStableRecording(after)) {
		t.Fatal("off changed ACMI output")
	}
	if strings.Contains(after, "WT_Guidance") {
		t.Fatalf("off wrote guidance fields:\n%s", after)
	}
	if !strings.Contains(after, "Name=AAM (unknown)") || !strings.Contains(after, "INS -- s") {
		t.Fatalf("off changed existing name or INS label:\n%s", after)
	}
}

func stripReferenceTime(data string) string {
	lines := strings.Split(data, "\n")
	out := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(line, "0,ReferenceTime=") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestParseMissileGuidanceRejectsInvalidTables(t *testing.T) {
	valid := `{"schema":1,"generated_from":["test"],"models":{"jp_aam3":{"guidance":"IR","source":"datamine","confidence":"high"}}}`
	if _, err := parseMissileGuidanceJSON([]byte(valid)); err != nil {
		t.Fatalf("valid table rejected: %v", err)
	}
	if _, err := parseMissileGuidanceJSON([]byte(`{"schema":1,"generated_from":[],"models":{}}`)); err != nil {
		t.Fatalf("empty valid table rejected: %v", err)
	}

	cases := map[string]string{
		"missing schema":       `{"generated_from":["test"],"models":{}}`,
		"wrong schema":         `{"schema":2,"generated_from":["test"],"models":{}}`,
		"missing generated":    `{"schema":1,"models":{}}`,
		"null generated":       `{"schema":1,"generated_from":null,"models":{}}`,
		"missing models":       `{"schema":1,"generated_from":["test"]}`,
		"top level unknown":    `{"schema":1,"generated_from":["test"],"models":{},"extra":1}`,
		"entry unknown":        `{"schema":1,"generated_from":["test"],"models":{"x":{"guidance":"IR","source":"s","confidence":"high","extra":1}}}`,
		"bad guidance":         `{"schema":1,"generated_from":["test"],"models":{"x":{"guidance":"RADAR","source":"s","confidence":"high"}}}`,
		"bad confidence":       `{"schema":1,"generated_from":["test"],"models":{"x":{"guidance":"IR","source":"s","confidence":"medium"}}}`,
		"empty source":         `{"schema":1,"generated_from":["test"],"models":{"x":{"guidance":"IR","source":"","confidence":"high"}}}`,
		"space in internal":    `{"schema":1,"generated_from":["test"],"models":{"x y":{"guidance":"IR","source":"s","confidence":"high"}}}`,
		"trailing JSON":        valid + `{}`,
		"tolerated bad syntax": `{"schema":1,`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMissileGuidanceJSON([]byte(data)); err == nil {
				t.Fatalf("invalid table accepted: %s", data)
			}
		})
	}

	oversized := append([]byte(valid), bytes.Repeat([]byte(" "), missileGuidanceMaxFileBytes)...)
	if _, err := parseMissileGuidanceJSON(oversized); err == nil {
		t.Fatal("oversized table accepted")
	}
}

func TestLoadMissileGuidanceCustomPathAndSizeLimit(t *testing.T) {
	valid := `{"schema":1,"generated_from":["test"],"models":{"jp_aam3":{"guidance":"IR","source":"datamine","confidence":"high"}}}`
	dir := t.TempDir()
	path := filepath.Join(dir, "guidance.json")
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	table, err := loadMissileGuidance(path)
	if err != nil || table == nil || table.Models["jp_aam3"].Guidance != "IR" {
		t.Fatalf("custom table = %+v/%v", table, err)
	}

	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, []byte(valid+strings.Repeat(" ", missileGuidanceMaxFileBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMissileGuidance(big); err == nil {
		t.Fatal("oversized custom table accepted")
	}
	if _, err := loadMissileGuidance(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing custom table accepted")
	}
}

func TestEmbeddedMissileGuidanceTableIsValid(t *testing.T) {
	table, err := parseMissileGuidanceJSON(embeddedMissileGuidanceJSON)
	if err != nil {
		t.Fatalf("embedded table is invalid: %v", err)
	}
	if table.Schema != 1 || table.Models == nil {
		t.Fatalf("embedded table = %+v", table)
	}
}

func TestMissileGuidanceReplayScopedWeaponIDRequiresExactSHA(t *testing.T) {
	dir := t.TempDir()
	replayPath := filepath.Join(dir, "sample.wrpl")
	content := []byte("synthetic replay bytes for guidance SHA")
	if err := os.WriteFile(replayPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	sha, err := hashMissileGuidanceReplay(replayPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	if want := hex.EncodeToString(sum[:]); sha != want {
		t.Fatalf("replay SHA = %q, want %q", sha, want)
	}
	table := guidanceTestTable(nil)
	table.Replays = map[string]missileGuidanceReplay{
		sha: {WeaponIDs: map[string]missileGuidanceEntry{
			"60": {Guidance: "IR", Source: "scoped replay weapon 60", Confidence: "high"},
		}},
	}

	correct := missileNameTestTrack(1, missileNameTestSource(77, 2000, 189, 8, "rocket"), true)
	correct.name = "AAM (unknown)"
	applyMissileGuidanceForReplay([]*track{correct}, table, sha)
	if info := correct.missileGuidance; info == nil || info.Type != "IR" ||
		info.Source != "scoped replay weapon 60" || info.Confidence != "high" || info.Candidates != "" {
		t.Fatalf("correct-SHA guidance = %+v", info)
	}
	if name := trackName(correct, nil, nil); name != "AAM (IR)" {
		t.Fatalf("correct-SHA fallback name = %q", name)
	}

	for _, wrong := range []string{"", strings.Repeat("0", 64), strings.Repeat("f", 64)} {
		t.Run("wrong SHA "+wrong, func(t *testing.T) {
			tr := missileNameTestTrack(2, missileNameTestSource(77, 2000, 189, 8, "rocket"), true)
			tr.name = "AAM (unknown)"
			applyMissileGuidanceForReplay([]*track{tr}, table, wrong)
			info := tr.missileGuidance
			if info == nil || info.Type != "Unknown" || info.Source != "TemplateNonARH" ||
				info.Confidence != "unknown" || info.Candidates != "IR / SARH" {
				t.Fatalf("wrong-SHA guidance = %+v", info)
			}
			if name := trackName(tr, nil, nil); name != "AAM (unknown)" {
				t.Fatalf("wrong-SHA fallback name = %q", name)
			}
		})
	}
}

func TestMissileGuidanceReplayScopeMergesWithModelAndTemplate(t *testing.T) {
	sha := strings.Repeat("a", 64)
	table := guidanceTestTable(map[string]missileGuidanceEntry{
		"cn_pl12": {Guidance: "ARH", Source: "model cn_pl12", Confidence: "confirmed"},
		"jp_aam3": {Guidance: "IR", Source: "model jp_aam3", Confidence: "confirmed"},
	})
	table.Replays = map[string]missileGuidanceReplay{
		sha: {WeaponIDs: map[string]missileGuidanceEntry{
			"60": {Guidance: "IR", Source: "scoped 60", Confidence: "high"},
		}},
	}

	conflict := guidanceTestTrack(1, "", "cn_pl12", "Matched")
	conflict.projectileSource.LeadingULEB = 189
	agree := guidanceTestTrack(2, "", "jp_aam3", "Matched")
	agree.projectileSource.LeadingULEB = 189
	templateConflict := guidanceTestTrack(3, "rocket+arh_shell", "", "")
	templateConflict.projectileSource.LeadingULEB = 189
	applyMissileGuidanceForReplay([]*track{conflict, agree, templateConflict}, table, sha)

	if info := conflict.missileGuidance; info == nil || info.Type != "Unknown" ||
		info.Source != "scoped 60" || info.Confidence != "conflict" || info.Candidates != "ARH / IR" {
		t.Fatalf("scoped/model conflict = %+v", info)
	}
	if info := agree.missileGuidance; info == nil || info.Type != "IR" ||
		info.Source != "scoped 60" || info.Confidence != "high" {
		t.Fatalf("scoped/model agreement = %+v", info)
	}
	if info := templateConflict.missileGuidance; info == nil || info.Type != "Unknown" ||
		info.Source != "scoped 60" || info.Confidence != "conflict" || info.Candidates != "ARH / IR" {
		t.Fatalf("scoped/template conflict = %+v", info)
	}
}

func TestMissileGuidanceReplayScopeValidation(t *testing.T) {
	sha := strings.Repeat("a", 64)
	valid := `{"schema":1,"generated_from":["test"],"models":{},"replays":{"` + sha + `":{"weapon_ids":{"60":{"guidance":"IR","source":"asset","confidence":"high"}}}}}`
	table, err := parseMissileGuidanceJSON([]byte(valid))
	if err != nil || table.Replays[sha].WeaponIDs["60"].Guidance != "IR" {
		t.Fatalf("valid replay scope = %+v/%v", table, err)
	}
	upper := `{"schema":1,"generated_from":["test"],"models":{},"replays":{"` + strings.ToUpper(sha) + `":{"weapon_ids":{"60":{"guidance":"IR","source":"asset","confidence":"high"}}}}}`
	upperTable, err := parseMissileGuidanceJSON([]byte(upper))
	if err != nil {
		t.Fatalf("uppercase SHA should normalize: %v", err)
	}
	if got := upperTable.Replays[sha].WeaponIDs["60"].Guidance; got != "IR" {
		t.Fatalf("normalized uppercase SHA lookup = %q", got)
	}

	cases := map[string]string{
		"short SHA":       `{"schema":1,"generated_from":["test"],"models":{},"replays":{"abc":{"weapon_ids":{}}}}`,
		"non-hex SHA":     `{"schema":1,"generated_from":["test"],"models":{},"replays":{"` + strings.Repeat("z", 64) + `":{"weapon_ids":{}}}}`,
		"missing ids":     `{"schema":1,"generated_from":["test"],"models":{},"replays":{"` + sha + `":{}}}`,
		"leading zero id": `{"schema":1,"generated_from":["test"],"models":{},"replays":{"` + sha + `":{"weapon_ids":{"060":{"guidance":"IR","source":"s","confidence":"high"}}}}}`,
		"negative id":     `{"schema":1,"generated_from":["test"],"models":{},"replays":{"` + sha + `":{"weapon_ids":{"-1":{"guidance":"IR","source":"s","confidence":"high"}}}}}`,
		"overflow id":     `{"schema":1,"generated_from":["test"],"models":{},"replays":{"` + sha + `":{"weapon_ids":{"4294967296":{"guidance":"IR","source":"s","confidence":"high"}}}}}`,
		"bad scoped type": `{"schema":1,"generated_from":["test"],"models":{},"replays":{"` + sha + `":{"weapon_ids":{"60":{"guidance":"RADAR","source":"s","confidence":"high"}}}}}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMissileGuidanceJSON([]byte(data)); err == nil {
				t.Fatalf("invalid replay scope accepted: %s", data)
			}
		})
	}
}

func TestMissileGuidanceCSVColumnsAppendOnlyWhenEnabled(t *testing.T) {
	tracks := []*track{guidanceTestTrack(42, "rocket+arh_shell", "", "")}
	applyMissileGuidance(tracks, guidanceTestTable(nil))
	dir := t.TempDir()
	listPath := filepath.Join(dir, "list.csv")
	if err := writeTrackList(listPath, tracks, nil, nil); err != nil {
		t.Fatal(err)
	}
	listRows := readTestCSV(t, listPath)
	if got := strings.Join(listRows[0][len(listRows[0])-4:], ","); got != "guidance_type,guidance_source,guidance_confidence,guidance_candidates" {
		t.Fatalf("list guidance header = %q", got)
	}
	if got := strings.Join(listRows[1][len(listRows[1])-4:], "|"); got != "ARH|TemplateARHShell|high|" {
		t.Fatalf("list guidance row = %q", got)
	}

	flightPath := filepath.Join(dir, "flight.csv")
	if err := writeMissileFlightLog(flightPath, tracks); err != nil {
		t.Fatal(err)
	}
	flightRows := readTestCSV(t, flightPath)
	if got := strings.Join(flightRows[0][len(flightRows[0])-4:], ","); got != "guidance_type,guidance_source,guidance_confidence,guidance_candidates" {
		t.Fatalf("flight guidance header = %q", got)
	}
	if got := strings.Join(flightRows[1][len(flightRows[1])-4:], "|"); got != "ARH|TemplateARHShell|high|" {
		t.Fatalf("flight guidance row = %q", got)
	}

	off := []*track{guidanceTestTrack(43, "rocket+arh_shell", "", "")}
	offList := filepath.Join(dir, "off-list.csv")
	if err := writeTrackList(offList, off, nil, nil); err != nil {
		t.Fatal(err)
	}
	offRows := readTestCSV(t, offList)
	if len(offRows[0]) != 10 {
		t.Fatalf("off list columns = %d, want 10", len(offRows[0]))
	}
}

func readTestCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
