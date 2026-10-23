package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

func TestAppendMissileTracksPreservesFullGenerationIDsAndExactParent(t *testing.T) {
	aircraft, _ := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	const sameIndex = uint64(42)
	const nextGeneration = sameIndex + (uint64(1) << 32)

	tracks := appendMissileTracks([]*track{aircraft}, []missileTrack{
		{
			ID:        sameIndex,
			Name:      "R-73E",
			ParentUID: 77,
			HasParent: true,
			Samples: []acmiSample{
				{t: 2600},
				{t: 2000},
				{t: 3000},
			},
			EndTime: 2700,
			HasEnd:  true,
		},
		{
			ID:        nextGeneration,
			Name:      "R-27ER",
			ParentUID: 999,
			HasParent: true,
			Samples:   []acmiSample{{t: 2200}},
		},
		{ID: 123, Samples: nil},
	})

	if len(tracks) != 3 {
		t.Fatalf("appendMissileTracks returned %d tracks, want aircraft plus two missiles", len(tracks))
	}
	first, second := tracks[1], tracks[2]
	if first.key != (trackKey{missile: true, id: sameIndex}) {
		t.Fatalf("first missile key = %+v, want full ID %d", first.key, sameIndex)
	}
	if second.key != (trackKey{missile: true, id: nextGeneration}) {
		t.Fatalf("second missile key = %+v, want full ID %d", second.key, nextGeneration)
	}
	if first.parent != aircraft {
		t.Fatal("missile with the actual owner UID was not linked to that aircraft")
	}
	if second.parent != nil {
		t.Fatal("missile was linked without a matching aircraft owner UID")
	}
	if len(first.samples) != 2 || first.samples[0].t != 2000 || first.samples[1].t != 2600 {
		t.Fatalf("unexpected samples after end filtering: %+v", first.samples)
	}
	if first.hasEnd != true || first.end != 2700 {
		t.Fatalf("explicit missile end was not retained: end=%d hasEnd=%v", first.end, first.hasEnd)
	}
	if second.hasEnd {
		t.Fatal("missile without explicit end was marked as having one")
	}
	if first.samples[len(first.samples)-1].t != 2600 || second.samples[0].t != 2200 {
		t.Fatal("real sample timestamps were changed")
	}
}

func TestACMIMissileLifecycleCreatesAtFirstSampleAndDeletesAtEnd(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	tracks := appendMissileTracks([]*track{aircraft}, []missileTrack{{
		ID:        0x10000002A,
		Name:      "R-73M",
		ParentUID: 77,
		HasParent: true,
		Samples: []acmiSample{
			{t: 2200, x: 30, y: 40, z: 50, yaw: 15},
			{t: 2000, x: 10, y: 20, z: 30, yaw: 5},
			{t: 2100, x: 20, y: 30, z: 40, yaw: 10},
			{t: 2600, x: 60, y: 70, z: 80, yaw: 30},
		},
		EndTime: 2200,
		HasEnd:  true,
	}})

	data := renderTestACMI(t, tracks, slot, nil)
	frames := parseTestACMI(data)
	missileID := "2"
	beforeSpawn := data[:strings.Index(data, "#2.000\n")]
	if testHasLinePrefix(beforeSpawn, missileID+",") || testHasLinePrefix(beforeSpawn, "-"+missileID) {
		t.Fatal("missile object existed before its first recorded sample")
	}
	create := frameFindingLine(frames, 2.000, missileID+",")
	wantPrefix := "2,Name=R-73M,CallSign=R-73M,Parent=1,Type=Weapon+Missile,Coalition=Blue,Color=Blue,Group=War Thunder Blue,T="
	if !strings.HasPrefix(create, wantPrefix) {
		t.Fatalf("missile creation line = %q, want prefix %q", create, wantPrefix)
	}
	if got := countObjectLines(frames, missileID+","); got != 3 {
		t.Fatalf("missile emitted %d real sample lines, want 3", got)
	}
	if got := frameFindingLine(frames, 2.200, "-"+missileID); got != "-2" {
		t.Fatalf("missile deletion line = %q, want -2", got)
	}
	if frameHasObjectPrefixAfter(frames, 2.200, missileID+",") {
		t.Fatal("missile emitted a T sample after deletion")
	}
}

func TestACMIMissileOneSampleWritesCreationBeforeDeletion(t *testing.T) {
	tracks := appendMissileTracks(nil, []missileTrack{{
		ID:      9,
		Name:    "AIM-9M",
		Samples: []acmiSample{{t: 1500, x: 1, y: 2, z: 3}},
		EndTime: 1500,
		HasEnd:  true,
	}})
	frames := renderAndParseACMI(t, tracks, nil, nil)
	if len(frames) != 1 {
		t.Fatalf("one-sample missile produced %d frames, want 1", len(frames))
	}
	if len(frames[0].lines) != 2 {
		t.Fatalf("one-sample frame lines = %v, want creation then deletion", frames[0].lines)
	}
	if !strings.HasPrefix(frames[0].lines[0], "1,Name=AIM-9M,") || frames[0].lines[1] != "-1" {
		t.Fatalf("one-sample frame order = %v, want creation then -1", frames[0].lines)
	}
}

func TestACMIMissileWithoutExplicitEndUsesFinalSampleAsCutoff(t *testing.T) {
	tracks := appendMissileTracks(nil, []missileTrack{{
		ID:      10,
		Name:    "R-60M",
		Samples: []acmiSample{{t: 5000}, {t: 5500}},
	}})
	frames := renderAndParseACMI(t, tracks, nil, nil)
	if got := frameFindingLine(frames, 0.500, "-1"); got != "-1" {
		t.Fatalf("final-sample cutoff deletion = %q, want -1 at #0.500", got)
	}
	if countObjectLines(frames, "1,") != 2 {
		t.Fatalf("missile samples = %v, want exactly the two recorded samples", frames)
	}
}

func TestMissileKeysAndManualNamesDoNotCollideWithOtherKinds(t *testing.T) {
	aircraft, _ := newTestTrack(true, 42, "f-16c_block_50", "", 0)
	ground := &track{key: trackKey{id: 42}, samples: []acmiSample{{t: 0}}}
	fullID := uint64(42) + (uint64(1) << 32)
	tracks := appendMissileTracks([]*track{ground, aircraft}, []missileTrack{
		{ID: 42, Name: "R-73", Samples: []acmiSample{{t: 1000}}},
		{ID: fullID, Name: "R-27ER", Samples: []acmiSample{{t: 1000}}},
	})

	if len(tracks) != 4 {
		t.Fatalf("got %d tracks, want ground, air, and two generation-distinct missiles", len(tracks))
	}
	seen := map[trackKey]bool{}
	for _, tr := range tracks {
		if seen[tr.key] {
			t.Fatalf("duplicate track key %+v", tr.key)
		}
		seen[tr.key] = true
	}
	names := map[string]string{
		"42":                 "wrong generic name",
		"2A":                 "wrong hex name",
		"missile:42":         "R-73 override",
		"missile:4294967338": "R-27 override",
	}
	if got := trackName(tracks[1], nil, names); got != "wrong generic name" {
		t.Fatalf("existing aircraft generic-name behavior changed to %q", got)
	}
	if got := trackName(tracks[0], nil, names); got != "wrong generic name" {
		t.Fatalf("existing ground generic-name behavior changed to %q", got)
	}
	if got := trackName(tracks[2], nil, names); got != "R-73 override" {
		t.Fatalf("missile 42 override = %q", got)
	}
	if got := trackName(tracks[3], nil, names); got != "R-27 override" {
		t.Fatalf("same-index later-generation override = %q", got)
	}
	if tracks[2].key.id == tracks[3].key.id {
		t.Fatal("same-index different-generation EIDs were collapsed")
	}
	ids := declaredACMIIDs(renderTestACMI(t, tracks, nil, names))
	if len(ids) != 4 {
		t.Fatalf("ACMI declared IDs %v, want one unique ID for each of four kinds/objects", ids)
	}
}

func TestMissileWithoutMatchingOwnerHasNoParentOrGuessedCoalition(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	tracks := appendMissileTracks([]*track{aircraft}, []missileTrack{{
		ID:        5,
		Name:      "AIM-7M",
		ParentUID: 78,
		HasParent: true,
		Samples:   []acmiSample{{t: 1000}},
	}})
	frames := renderAndParseACMI(t, tracks, slot, nil)
	create := frameFindingLine(frames, 1.000, "2,")
	if strings.Contains(create, "Parent=") {
		t.Fatalf("missile guessed a parent from nearby aircraft: %q", create)
	}
	if !strings.Contains(create, "Coalition=Neutral,Color=White") {
		t.Fatalf("missile without an actual launcher did not remain neutral: %q", create)
	}
}

func TestMissileTrackListUsesActualNameAndOptionalParentSource(t *testing.T) {
	aircraft, slot := newTestTrack(true, 77, "F-16C Block 50", "Alpha", 1)
	tracks := appendMissileTracks([]*track{aircraft}, []missileTrack{{
		ID:        0x10000002A,
		Name:      "R-73M",
		ParentUID: 77,
		HasParent: true,
		Samples:   []acmiSample{{t: 2000}, {t: 2500}},
		EndTime:   3300,
		HasEnd:    true,
	}})
	path := filepath.Join(t.TempDir(), "tracks.csv")
	if err := writeTrackList(path, tracks, slot, nil); err != nil {
		t.Fatalf("writing track list: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("CSV rows = %d, want header plus aircraft and missile", len(rows))
	}
	missile := rows[2]
	want := []string{"missile:4294967338", "missile", "", "Blue", "R-73M", "2.000", "3.300", "2", "", "air:77"}
	if strings.Join(missile, "|") != strings.Join(want, "|") {
		t.Fatalf("missile CSV row = %v, want %v", missile, want)
	}
}

type missileTestFrame struct {
	label string
	lines []string
}

func renderAndParseACMI(t *testing.T, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string) []missileTestFrame {
	t.Helper()
	return parseTestACMI(renderTestACMI(t, tracks, slot, names))
}

func renderTestACMI(t *testing.T, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "missile.acmi")
	if err := writeACMI(path, wrpl.WRPLHeader{}, tracks, slot, names); err != nil {
		t.Fatalf("writing ACMI: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func parseTestACMI(data string) []missileTestFrame {
	var frames []missileTestFrame
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "#") {
			frames = append(frames, missileTestFrame{label: strings.TrimPrefix(line, "#")})
			continue
		}
		if len(frames) > 0 && line != "" {
			frames[len(frames)-1].lines = append(frames[len(frames)-1].lines, line)
		}
	}
	return frames
}

func testHasLinePrefix(data, prefix string) bool {
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func declaredACMIIDs(data string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, line := range strings.Split(data, "\n") {
		if !strings.Contains(line, ",Type=") {
			continue
		}
		id, _, ok := strings.Cut(line, ",")
		if ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func frameFindingLine(frames []missileTestFrame, second float64, prefix string) string {
	label := formatTestFrameTime(second)
	for _, frame := range frames {
		if frame.label != label {
			continue
		}
		for _, line := range frame.lines {
			if strings.HasPrefix(line, prefix) {
				return line
			}
		}
	}
	return ""
}

func frameHasObjectPrefixAfter(frames []missileTestFrame, second float64, prefix string) bool {
	label := formatTestFrameTime(second)
	started := false
	for _, frame := range frames {
		if frame.label == label {
			started = true
			continue
		}
		if !started {
			continue
		}
		for _, line := range frame.lines {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
}

func countObjectLines(frames []missileTestFrame, prefix string) int {
	count := 0
	for _, frame := range frames {
		for _, line := range frame.lines {
			if strings.HasPrefix(line, prefix) {
				count++
			}
		}
	}
	return count
}

func formatTestFrameTime(second float64) string {
	return strconv.FormatFloat(second, 'f', 3, 64)
}
