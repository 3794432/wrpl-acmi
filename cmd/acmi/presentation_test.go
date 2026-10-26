package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func TestRecordingMetadataAndEscaping(t *testing.T) {
	h := wrpl.WRPLHeader{StartTime: 1000}
	copy(h.Raw_BattleType[:], "air,combat\x00junk")
	old := *recordingAuthor
	*recordingAuthor = "Alice,Test\nName"
	t.Cleanup(func() { *recordingAuthor = old })
	props := strings.Join(acmiMetadata(h, "air_afghan", terrainLayoutGeographic, acmiWriterContext{recordingTime: time.Unix(2000, 0), estimateTelemetry: true}), "\n")
	for _, want := range []string{"Title=air_afghan / air\\,combat / 1970-01-01T00:16:40Z", "Author=Alice\\,Test\\nName", "RecordingTime=1970-01-01T00:33:20Z", "Category=War Thunder", "DataRecorder=wrpl-acmi", "-terrain-layout=geographic", "georeference commit=", "NOT game telemetry", "IAS=EAS approximation"} {
		if !strings.Contains(props, want) {
			t.Fatalf("missing %q in %s", want, props)
		}
	}
	if strings.Contains(props, "junk") {
		t.Fatal("header trailing bytes leaked")
	}
}

func TestBookmarksUseEvidenceAndEscapeSeparators(t *testing.T) {
	air := &track{key: trackKey{air: true, id: 1}, name: "Test,Plane|Pilot", samples: []acmiSample{{t: 1000}}, hasEnd: true, end: 2000}
	weapon := &track{key: trackKey{missile: true, id: 2}, name: "Test missile", samples: []acmiSample{{t: 1100}}, parent: air}
	tracks := []*track{air, weapon}
	ids := acmiObjectIDs(tracks)
	bookmarks := acmiBookmarks(tracks, []acmiEvent{{t: 1200, text: "Countermeasure|released,now\nnext"}, {t: 1200, text: "Countermeasure|released,now\nnext"}}, nil, ids, nil, nil)
	if len(bookmarks[1200]) != 1 || strings.Contains(bookmarks[1200][0], "released,now") || !strings.Contains(bookmarks[1200][0], "Countermeasure/released\\,now next") {
		t.Fatalf("bad escaping/dedup: %v", bookmarks)
	}
	if !strings.Contains(bookmarks[1100][0], "Missile first observed") || !strings.Contains(bookmarks[2000][0], "Destroyed") {
		t.Fatalf("missing evidence events: %v", bookmarks)
	}
}

func TestRetiredAircraftHasNoDestroyedBookmark(t *testing.T) {
	retired := &track{
		key: trackKey{air: true, id: 5}, samples: []acmiSample{{t: 1000}},
		hasEnd: true, end: 2000, retired: true,
	}
	replacement := &track{
		key: trackKey{air: true, id: 5, sortie: 1}, samples: []acmiSample{{t: 2500}},
	}
	dead := &track{
		key: trackKey{air: true, id: 6}, samples: []acmiSample{{t: 1000}},
		hasEnd: true, end: 3000,
	}
	tracks := []*track{retired, replacement, dead}
	ids := acmiObjectIDs(tracks)
	bookmarks := acmiBookmarks(tracks, nil, nil, ids, nil, nil)
	if len(bookmarks[2000]) != 0 {
		t.Fatalf("retired sortie fabricated a destruction bookmark: %v", bookmarks[2000])
	}
	line := strings.Join(bookmarks[3000], "\n")
	if !strings.Contains(line, ids[dead.key]) || !strings.Contains(line, "Destroyed") {
		t.Fatalf("confirmed death lost its destruction bookmark: %v", bookmarks[3000])
	}
}

func TestCountermeasureMessagesRemainWithoutHighlightedBookmarks(t *testing.T) {
	old := *timelineBookmarks
	t.Cleanup(func() { *timelineBookmarks = old })
	air := &track{key: trackKey{air: true, id: 1}, samples: []acmiSample{{t: 1000}}}
	events := []acmiEvent{{t: 1000, parentID: 1, hasParent: true, text: "Chaff released"}}
	*timelineBookmarks = false
	plain := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, []*track{air}, nil, nil, events)
	if strings.Contains(plain, "Event=Bookmark|") || strings.Count(plain, "Chaff released") != 1 || !strings.Contains(plain, "Event=Message|1|Chaff released") {
		t.Fatalf("plain release log lost or highlighted: %s", plain)
	}
	*timelineBookmarks = true
	highlighted := renderTestACMIWithEvents(t, wrpl.WRPLHeader{}, []*track{air}, nil, nil, events)
	if !strings.Contains(highlighted, "Event=Bookmark|1|Chaff released") || !strings.Contains(highlighted, "Event=Message|1|Chaff released") {
		t.Fatalf("explicit bookmark opt-in failed: %s", highlighted)
	}
}

func TestAircraftTelemetryIsOptInAndDoesNotAlterPositions(t *testing.T) {
	air := &track{key: trackKey{air: true, id: 1}, samples: []acmiSample{{t: 1000, y: 1000}, {t: 2000, y: 1000, z: 200}, {t: 3000, y: 1000, z: 400}}}
	path := filepath.Join(t.TempDir(), "sample.acmi")
	ctx := acmiWriterContext{recordingTime: time.Unix(2000, 0)}
	render := func() string {
		if err := writeACMIWithContext(path, wrpl.WRPLHeader{StartTime: 1000}, []*track{air}, nil, nil, nil, "", acmiSourceContext{}, ctx); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	off := render()
	ctx.estimateTelemetry = true
	on := render()
	if strings.Contains(off, ",TAS=") || !strings.Contains(on, ",TAS=") || !strings.Contains(on, ",VerticalGForce=") {
		t.Fatal("telemetry gating failed")
	}
	positions := func(s string) []string {
		var result []string
		for _, line := range strings.Split(s, "\n") {
			if i := strings.Index(line, ",T="); i >= 0 {
				position := strings.SplitN(line[i+3:], ",", 2)[0]
				result = append(result, position)
			}
		}
		return result
	}
	if strings.Join(positions(off), "\n") != strings.Join(positions(on), "\n") {
		t.Fatal("telemetry changed geometry")
	}
}

func TestFormatVersionValidatedBeforeOutput(t *testing.T) {
	old := *acmiFormatVersion
	t.Cleanup(func() { *acmiFormatVersion = old })
	path := filepath.Join(t.TempDir(), "out.acmi")
	*acmiFormatVersion = "9.9"
	if err := writeACMI(path, wrpl.WRPLHeader{}, nil, nil, nil); err == nil {
		t.Fatal("invalid format accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid version touched destination")
	}
	*acmiFormatVersion = "2.2"
	if err := writeACMI(path, wrpl.WRPLHeader{}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "FileVersion=2.2\n") {
		t.Fatal("2.2 not selected")
	}
}

func TestBookmarkReleaseBeforeWeaponBirthOnlyReferencesParent(t *testing.T) {
	air := &track{key: trackKey{air: true, id: 1}, samples: []acmiSample{{t: 1000}}}
	weapon := &track{key: trackKey{missile: true, id: 2}, samples: []acmiSample{{t: 2000}}, parent: air, projectileSource: &projectiles.Projectile{SpawnSample: projectiles.Sample{Time: 1500}}}
	ids := acmiObjectIDs([]*track{air, weapon})
	bookmarks := acmiBookmarks([]*track{air, weapon}, nil, nil, ids, nil, nil)
	if line := bookmarks[1500][0]; strings.Contains(line, "|"+ids[weapon.key]+"|") || !strings.Contains(line, "|"+ids[air.key]+"|") {
		t.Fatalf("event references unborn weapon or loses parent: %s", line)
	}
}

func TestBookmarksCanBeDisabledWithoutDroppingDestruction(t *testing.T) {
	old := *timelineBookmarks
	*timelineBookmarks = false
	t.Cleanup(func() { *timelineBookmarks = old })
	air := &track{key: trackKey{air: true, id: 1}, samples: []acmiSample{{t: 1000}}, hasEnd: true, end: 2000}
	data := renderTestACMI(t, []*track{air}, nil, nil)
	if strings.Contains(data, "Event=Bookmark") || !strings.Contains(data, "Event=Destroyed|1") {
		t.Fatalf("bookmark switch dropped original events: %s", data)
	}
}
