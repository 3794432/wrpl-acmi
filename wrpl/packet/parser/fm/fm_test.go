package packetfm

import (
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
)

func TestTargetListDistinguishesExplicitEmptyFromUnavailable(t *testing.T) {
	empty, err := readTargets(danet.NewBitReader([]byte{0}))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("explicit empty designation list = %v/%v; want non-nil empty", empty, err)
	}
	for _, wire := range [][]byte{nil, {0x10}, {0x90}} {
		partial, err := readTargets(danet.NewBitReader(wire))
		if err == nil || partial != nil {
			t.Fatalf("unavailable/truncated designation list %x = %v/%v; want nil/error", wire, partial, err)
		}
	}
}
