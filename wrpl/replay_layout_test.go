package wrpl

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestReplayLayoutVersionBoundary(t *testing.T) {
	for _, tc := range []struct {
		version int32
		modern  bool
	}{
		{0x00018C1B, false},
		{0x00018C1C, true},
		{0x00018C28, true},
		{0x00018C29, true},
	} {
		if got := (WRPLHeader{Version: tc.version}).UsesModernLayout(); got != tc.modern {
			t.Errorf("version %#x: modern=%v, want %v", tc.version, got, tc.modern)
		}
	}
}

func TestOpenReplayModernRevisions(t *testing.T) {
	for _, version := range []int32{0x00018C1C, 0x00018C28, 0x00018C29} {
		t.Run(fmt.Sprintf("%x", version), func(t *testing.T) {
			settings := []byte("settings start at byte 1234, not byte 1226")
			packets := encodeTestPackets(t, []testPacketSpec{{packetType: replayPacketEndMarker}})
			results := []byte("results payload")
			path := writeTestReplayPart(t, t.TempDir(), testReplayConfig{
				version: version, settings: settings, packetStream: packets, results: results,
			})
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			r, err := OpenReplay(f, true, true, true)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if !bytes.Equal(r.Settings, settings) || !bytes.Equal(r.Results, results) {
				t.Fatalf("settings=%q results=%q", r.Settings, r.Results)
			}
			got, err := io.ReadAll(r.PacketStream)
			if err != nil || !bytes.Equal(got, packets) {
				t.Fatalf("packet stream=%x error=%v, want %x", got, err, packets)
			}
		})
	}
}
