package packetfm

import (
	"fmt"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
)

// A malformed or misaligned FM record must fail before a count-driven
// allocation. Every entry consumes at least two one-byte varints.
func checkFMCount(r *danet.BitReader, count int32, minBits int, label string) error {
	const maxFMCount = 4096
	if count < 0 || count > maxFMCount {
		return fmt.Errorf("FM %s count %d exceeds allocation limit %d", label, count, maxFMCount)
	}
	if r == nil || r.BitOffset < 0 || r.BitOffset > len(r.Data)*8 {
		return fmt.Errorf("FM %s has invalid reader offset", label)
	}
	remaining := len(r.Data)*8 - r.BitOffset
	if minBits <= 0 || int64(count) > int64(remaining/minBits) {
		return fmt.Errorf("FM %s count %d exceeds %d remaining bits", label, count, remaining)
	}
	return nil
}
