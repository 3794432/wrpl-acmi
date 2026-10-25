package packetfm

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
)

func malformedFMCountPacket(count uint64, inner bool) *packet.Packet {
	// UID 1 by increment, present entry, flags clear, uint32 clear,
	// two final flags clear, then the first list's zig-zag count.
	fields := []int{0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 32, 0, 1, 0, 1}
	if inner {
		fields = append(fields, 2, 8, 0, 8) // outer count 1; first Temp = 0.
	}
	for _, b := range binary.AppendUvarint(nil, count<<1) {
		fields = append(fields, int(b), 8)
	}
	return &packet.Packet{PacketType: 2, PacketPayload: sensorTestBits(fields...)}
}

func TestFMMalformedCountsFailBeforeAllocation(t *testing.T) {
	for _, inner := range []bool{false, true} {
		for _, count := range []uint64{1, 4_000_000} {
			parser := &PacketFlightModelParser{}
			result, err := parser.Parse2(malformedFMCountPacket(count, inner))
			if err == nil || (!strings.Contains(err.Error(), "allocation limit") && !strings.Contains(err.Error(), "remaining bits")) {
				t.Fatalf("inner=%t count=%d: error=%v", inner, count, err)
			}
			if result == nil || len(result.Entries) != 1 || result.Entries[0].Data == nil {
				t.Fatal("test did not reach the FM list")
			}
			if !inner && result.Entries[0].Data.Unk11 != nil {
				t.Fatal("malformed count allocated the list before rejection")
			}
		}
	}
}
