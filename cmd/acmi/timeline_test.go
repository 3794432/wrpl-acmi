package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
)

func TestTimelineSelection(t *testing.T) {
	all, err := parseTimelineEvents(defaultTimelineEvents)
	if err != nil || len(all) != 5 || all["award"] {
		t.Fatalf("defaults: %v %v", all, err)
	}
	withAward, err := parseTimelineEvents("all")
	if err != nil || len(withAward) != 6 || !withAward["award"] {
		t.Fatalf("all: %v %v", withAward, err)
	}
	for _, spec := range []string{"none", "off", ""} {
		selected, err := parseTimelineEvents(spec)
		if err != nil || len(selected) != 0 {
			t.Fatalf("off %q: %v %v", spec, selected, err)
		}
	}
	if _, err := parseTimelineEvents("kill,typo"); err == nil {
		t.Fatal("unknown category accepted")
	}
	selected, err := parseTimelineEvents("kill, radar,kill")
	if err != nil || len(selected) != 2 {
		t.Fatalf("selection %v %v", selected, err)
	}
}

func TestTimelineDoesNotReviveObjectsOrInjectFields(t *testing.T) {
	tr := &track{key: trackKey{air: true, id: 0}, samples: []acmiSample{{t: 100}}, hasEnd: true, end: 200}
	events := []timelineEvent{
		{t: 99, keys: []trackKey{tr.key}, text: "unborn"},
		{t: 201, keys: []trackKey{tr.key}, text: "deleted"},
		{t: 200, keys: []trackKey{tr.key}, text: "Killed victim with weapon"},
		{t: 200, keys: []trackKey{tr.key}, text: "Killed victim with weapon"},
		{t: 50, text: "Chat: |hello,world\n-1"},
	}
	lines := timelineLines(events, []*track{tr}, map[trackKey]string{tr.key: "a"})
	if len(lines) != 2 || len(lines[200]) != 1 || !strings.Contains(lines[200][0], "Message|a|Killed") {
		t.Fatalf("lifetime/dedup: %v", lines)
	}
	if strings.Contains(lines[50][0], "\n") || strings.Count(lines[50][0], "|") != 1 || !strings.Contains(lines[50][0], `hello\,world`) {
		t.Fatalf("injection: %q", lines[50])
	}
}

func TestGroundDestroyedBeforeDeleteAndNoSyntheticEnd(t *testing.T) {
	dead := &track{key: trackKey{groundEntity: true, id: 4}, samples: []acmiSample{{t: 100}, {t: 200, x: 10}}, end: 250, hasEnd: true}
	alive := &track{key: trackKey{groundEntity: true, id: 5}, samples: []acmiSample{{t: 100}, {t: 200, x: 10}}}
	path := filepath.Join(t.TempDir(), "ground.acmi")
	if err := writeACMI(path, wrpl.WRPLHeader{}, []*track{dead, alive}, nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	ids := acmiObjectIDs([]*track{dead, alive})
	event := "0,Event=Destroyed|" + ids[dead.key]
	if strings.Count(text, event) != 1 || strings.Index(text, event) > strings.Index(text, "\n-"+ids[dead.key]+"\n") || strings.Contains(text, "0,Event=Destroyed|"+ids[alive.key]) {
		t.Fatalf("ground destruction lifecycle: %s", text)
	}
}

func TestRadarMessagesIncludeClearBetweenFMFrames(t *testing.T) {
	source := radarTestTrack(1, radarTestSample(100, 0), radarTestSample(1000, 0))
	target := radarTestTrack(2, radarTestSample(100, 1000))
	target.end, target.hasEnd = 500, true
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{radarTestFrameTargets(100, 1, []packetfm.FMTargetData{radarTestLock(2)})}}
	x := radarTestPrepare(t, fm, []*track{source, target})
	events := radarTimeline(x, nil, nil)
	if len(events) != 2 || events[1].t != 500 || events[1].text != "Radar designation cleared" || !strings.Contains(events[0].text, "1000 m") {
		t.Fatalf("radar events %+v", events)
	}
	path := filepath.Join(t.TempDir(), "radar-messages.acmi")
	if err := writeACMIWithContext(path, wrpl.WRPLHeader{}, []*track{source, target}, nil, nil, nil, "", acmiSourceContext{}, acmiWriterContext{radarLocks: x, radarMessages: true, suppressRadarProperties: true}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if strings.Count(text, "Event=Message|") != 2 || strings.Contains(text, "LockedTarget=") {
		t.Fatalf("independent radar messages: %s", text)
	}
}
