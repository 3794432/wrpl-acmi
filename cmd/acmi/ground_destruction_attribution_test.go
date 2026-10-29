package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
)

func TestGroundDestroyedEventContainsWeaponAndTemporalPlayerWithoutLiveKiller(t *testing.T) {
	roster := newOptionalEventParser(map[string]bool{"kill": true}, nil)
	damageTestRosterAt(roster, 23, 0, 11, "Original player")
	damageTestRosterAt(roster, 23, 2500, 12, "Replacement")
	victim := killAttributionGround(groundUnitUIDNamespace|7, 2000, "2S38")
	dead := damageTestAircraft(77, "f_16a_block_15_adf_killstreak", 23, 1000)
	dead.hasEnd, dead.end = true, 1500
	report := killAttributionEntry(2000, 0x7ff, 7, "us_b61_30kt")
	report.KillerPid, report.HasKillerPID = 23, true
	report.PlayerVehicle = "f_16a_block_15_adf_killstreak"
	tracks := []*track{dead, victim}
	details := buildGroundDestructionAttributions(tracks, []packetkill.KillEntry{report}, roster, nil)
	entry, ok := details[victim.key]
	if !ok || entry.text != "2S38 destroyed by ☢F-16A ADF (Original player) with ☢B61" || entry.identitySource != "ReportPIDRosterAtTime" {
		t.Fatalf("missing temporal ground attribution: %+v", entry)
	}
	path := filepath.Join(t.TempDir(), "ground.acmi")
	if err := writeACMIWithContext(path, wrpl.WRPLHeader{}, tracks, nil, nil, nil, "", acmiSourceContext{}, acmiWriterContext{groundDestructions: details}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	frames := parseTestACMI(string(raw))
	ids := acmiObjectIDs(tracks)
	line := "0,Event=Destroyed|" + ids[victim.key] + "|" + entry.text
	if frameFindingLine(frames, 1.0, line) != line || frameFindingLine(frames, 1.0, "-"+ids[victim.key]) == "" {
		t.Fatal("native destruction attribution/deletion missing")
	}
	if frameFindingLine(frames, 1.0, ids[dead.key]+",") != "" || strings.Contains(string(raw), "Replacement") {
		t.Fatal("dead killer revived or later roster identity borrowed")
	}
	if got := killsWithoutGroundDestructionEvents(tracks, []packetkill.KillEntry{report}, details); len(got) != 0 {
		t.Fatal("ground attribution also generated a duplicate Message event")
	}
}

func TestGroundDestroyedEventAppliesToNonNuclearWeaponsAndPreservesAIIdentity(t *testing.T) {
	roster := newOptionalEventParser(map[string]bool{"kill": true}, nil)
	damageTestRosterAt(roster, 0, 0, 100, "Unrelated roster zero")
	groundKiller := killAttributionGround(groundUnitUIDNamespace|9, 3000, "2S6")
	victim := killAttributionGround(groundUnitUIDNamespace|7, 2000, "Tank")
	report := killAttributionEntry(2000, 9, 7, "152mm_zur_9m311_1m")
	report.KillerPid, report.HasKillerPID = 0, true
	report.PlayerVehicle = "ussr_zprk_2s6"
	details := buildGroundDestructionAttributions([]*track{groundKiller, victim}, []packetkill.KillEntry{report}, roster, nil)
	entry := details[victim.key]
	if entry.killerPlayer != "" || !strings.Contains(entry.text, "with 9M311-1M") || strings.Contains(entry.text, "Unrelated") {
		t.Fatalf("AI ground killer inherited a player: %+v", entry)
	}
	for _, weapon := range []string{"us_gbu_39", "su_kh_29td", "unknown_new_weapon", "cannon"} {
		report.PlayerWeapon = weapon
		got := buildGroundDestructionAttributions([]*track{groundKiller, victim}, []packetkill.KillEntry{report}, roster, nil)
		if len(got) != 1 || got[victim.key].weapon != weapon {
			t.Fatalf("non-nuclear weapon %q omitted", weapon)
		}
	}
}

func TestGroundDestroyedAttributionRejectsAmbiguityMissingWeaponAndWrongLifetime(t *testing.T) {
	victim := killAttributionGround(groundUnitUIDNamespace|7, 2000, "Tank")
	report := killAttributionEntry(2000, 0x7ff, 7, "us_gbu_39")
	for _, variant := range []string{"missing weapon", "wrong time", "ambiguous victim", "conflicting reports"} {
		t.Run(variant, func(t *testing.T) {
			tracks, reports := []*track{victim}, []packetkill.KillEntry{report}
			switch variant {
			case "missing weapon":
				reports[0].PlayerWeapon = ""
			case "wrong time":
				reports[0].CurrentTime = 1999
			case "ambiguous victim":
				tracks = append(tracks, killAttributionGround(groundUnitUIDNamespace|7, 2000, "Other"))
			case "conflicting reports":
				other := report
				other.PlayerWeapon = "us_gbu_53"
				reports = append(reports, other)
			}
			if got := buildGroundDestructionAttributions(tracks, reports, nil, nil); len(got) != 0 {
				t.Fatalf("unsafe attribution: %+v", got)
			}
		})
	}
	lines := groundDestroyedEventLines(victim, "1", nil)
	if len(lines) != 1 || lines[0] != "0,Event=Destroyed|1" {
		t.Fatal("missing attribution changed generic destruction")
	}
}

func TestGroundKillPIDMissingCannotDefaultToRosterZero(t *testing.T) {
	roster := newOptionalEventParser(map[string]bool{"kill": true}, nil)
	damageTestRosterAt(roster, 0, 0, 100, "Player zero")
	victim := killAttributionGround(groundUnitUIDNamespace|7, 2000, "Tank")
	report := killAttributionEntry(2000, 0x7ff, 7, "us_gbu_39")
	entry := buildGroundDestructionAttributions([]*track{victim}, []packetkill.KillEntry{report}, roster, nil)[victim.key]
	if entry.killerPlayer != "" || entry.identitySource != "WeaponOnly" {
		t.Fatalf("missing PID became player zero: %+v", entry)
	}
	if got := roster.ParsesMatching()[4]; len(got) == 0 {
		t.Fatal("kill-only category did not retain roster history")
	}
}

func TestGroundDelayedKillCannotBorrowReusedPlayerSlot(t *testing.T) {
	roster := newOptionalEventParser(map[string]bool{"kill": true}, nil)
	damageTestRosterAt(roster, 23, 0, 11, "Original")
	damageTestRosterAt(roster, 23, 1500, 12, "Replacement")
	victim := killAttributionGround(groundUnitUIDNamespace|7, 2000, "Tank")
	report := killAttributionEntry(2000, 0x7ff, 7, "us_b61_30kt")
	report.KillerPid, report.HasKillerPID = 23, true
	report.PlayerVehicle = "f_16a_block_15_adf_killstreak"
	entry := buildGroundDestructionAttributions([]*track{victim}, []packetkill.KillEntry{report}, roster, nil)[victim.key]
	if entry.killerPlayer != "" || strings.Contains(entry.text, "Replacement") || entry.weaponName != "☢B61" {
		t.Fatalf("delayed weapon borrowed replacement player: %+v", entry)
	}
}

func TestGroundDelayedKillUsesUniqueSortieAfterEarlierRosterReplacement(t *testing.T) {
	roster := newOptionalEventParser(map[string]bool{"kill": true}, nil)
	damageTestRosterAt(roster, 23, 0, 11, "Earlier occupant")
	damageTestRosterAt(roster, 23, 1500, 12, "Actual attacker")
	killer := damageTestAircraft(77, "f_16a_block_15_adf_killstreak", 23, 1600)
	killer.hasEnd, killer.end = true, 1900
	victim := killAttributionGround(groundUnitUIDNamespace|7, 2000, "Tank")
	report := killAttributionEntry(2000, 77, 7, "us_b61_30kt")
	report.KillerPid, report.HasKillerPID = 23, true
	report.PlayerVehicle = "f_16a_block_15_adf_killstreak"
	entry := buildGroundDestructionAttributions([]*track{killer, victim}, []packetkill.KillEntry{report}, roster, nil)[victim.key]
	if entry.killerPlayer != "Actual attacker" || entry.identitySource != "ReportPIDHistoricalUID" {
		t.Fatalf("historical exact sortie lost: %+v", entry)
	}
	killer.samples[0].t = 1000 // The old sortie now spans the roster replacement.
	entry = buildGroundDestructionAttributions([]*track{killer, victim}, []packetkill.KillEntry{report}, roster, nil)[victim.key]
	if entry.killerPlayer != "" {
		t.Fatal("roster replacement was assigned to the earlier sortie")
	}
}
