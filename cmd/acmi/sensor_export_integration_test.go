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

func TestWriterSeekerNativeStateAndSuppression(t *testing.T) {
	block := make([]byte, (607+7)/8)
	block[1] = 0xe0 // Radar seeker bits 8..10 = 111: track.
	motion := &projectiles.RawMotion{Complete: true, HasTail: true,
		HasSeekerBlock: true, SeekerBitLength: 607, SeekerRawBits: block}
	search := *motion
	search.SeekerRawBits = append([]byte(nil), block...)
	search.SeekerRawBits[1] = 0x20 // Radar seeker bits 8..10 = 001: search.
	missile := &track{key: trackKey{missile: true, id: 2}, name: "Test Missile",
		projectileSource: &projectiles.Projectile{ID: 2, Template: "rocket+arh_shell", HasEnd: true, EndTime: 1500},
		hasEnd:           true, end: 1500,
		samples: []acmiSample{
			{t: 1000, y: 1000, projectile: &projectiles.Sample{FromSync: true, RawMotion: &search}},
			{t: 1100, x: 50, y: 1000, projectile: &projectiles.Sample{FromSync: true, RawMotion: motion}},
			{t: 1250, x: 100, y: 1000},
		}}
	render := func(suppressed bool) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "seeker.acmi")
		ctx := acmiWriterContext{recordingTime: time.Unix(0, 0), suppressSeekerProperties: suppressed}
		if err := writeACMIWithContext(path, wrpl.WRPLHeader{}, []*track{missile}, nil, nil, nil, "", acmiSourceContext{}, ctx); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	on := render(false)
	off := render(true)
	for _, want := range []string{"WT_SeekerState=Search", "WT_SeekerState=Track", "LockedTargetMode=1", "RadarMode=1", "LockedTargetMode=0", "RadarMode=0", " s SRC", " s TRK", " s N/A"} {
		if !strings.Contains(on, want) {
			t.Fatalf("missing native state or subsequent clear %q", want)
		}
	}
	if strings.Contains(off, "WT_SeekerState=") || strings.Contains(off, "LockedTargetMode=") || strings.Contains(off, "RadarMode=") {
		t.Fatal("suppression left seeker properties in the file")
	}
	positions := func(data string) string {
		var lines []string
		for _, line := range strings.Split(data, "\n") {
			if i := strings.Index(line, ",T="); i >= 0 {
				lines = append(lines, strings.SplitN(line[i+3:], ",", 2)[0])
			}
		}
		return strings.Join(lines, "\n")
	}
	if positions(on) != positions(off) {
		t.Fatal("seeker export changed positions")
	}
}
