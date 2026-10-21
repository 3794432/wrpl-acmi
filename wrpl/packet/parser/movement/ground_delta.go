package packetmovement

import "fmt"

const groundDeltaHistorySize = 32

type groundDeltaHistory struct {
	entries   [groundDeltaHistorySize][]byte
	packetNos [groundDeltaHistorySize]uint32
	valid     [groundDeltaHistorySize]bool
}

func newGroundDeltaHistory() *groundDeltaHistory { return &groundDeltaHistory{} }

func (h *groundDeltaHistory) apply(full bool, packetNoDelta, nextPacketNo uint32, compressed []byte, totalBytes *int) ([]byte, error) {
	if len(compressed) == 0 || len(compressed) > maxGroundDeltaCompressed {
		return nil, fmt.Errorf("invalid compressed delta size %d", len(compressed))
	}
	var result []byte
	if full {
		result = append([]byte(nil), compressed...)
	} else {
		if packetNoDelta >= groundDeltaHistorySize {
			return nil, fmt.Errorf("delta base distance %d exceeds history", packetNoDelta)
		}
		baseNo := nextPacketNo - packetNoDelta
		baseSlot := baseNo % groundDeltaHistorySize
		baseTag := baseNo & 0x1fff
		if !h.valid[baseSlot] || h.packetNos[baseSlot] != baseTag || len(h.entries[baseSlot]) == 0 {
			return nil, fmt.Errorf("delta base packet %d is unavailable (slot %d holds packet %d, valid=%t)", baseNo, baseSlot, h.packetNos[baseSlot], h.valid[baseSlot])
		}
		base := h.entries[baseSlot]
		delta, err := rle0kiDecompress(compressed)
		if err != nil {
			return nil, err
		}
		result, err = xorPatched(base, delta)
		if err != nil {
			return nil, err
		}
	}
	if len(result) > maxGroundDeltaRecord {
		return nil, fmt.Errorf("patched delta size %d exceeds record limit %d", len(result), maxGroundDeltaRecord)
	}
	slot := nextPacketNo % groundDeltaHistorySize
	if totalBytes == nil {
		return nil, fmt.Errorf("nil history budget")
	}
	nextTotal := *totalBytes - len(h.entries[slot]) + len(result)
	if nextTotal < 0 || nextTotal > maxGroundHistoryTotal {
		return nil, fmt.Errorf("ground delta history budget would reach %d bytes (limit %d)", nextTotal, maxGroundHistoryTotal)
	}
	h.entries[slot] = append([]byte(nil), result...)
	h.packetNos[slot] = nextPacketNo & 0x1fff
	h.valid[slot] = true
	*totalBytes = nextTotal
	return result, nil
}

func xorPatched(base, delta []byte) ([]byte, error) {
	n := len(base)
	if len(delta) > n {
		n = len(delta)
	}
	if n > maxGroundDeltaRecord {
		return nil, fmt.Errorf("patched delta size %d exceeds limit", n)
	}
	out := make([]byte, n)
	common := len(base)
	if len(delta) < common {
		common = len(delta)
	}
	for i := 0; i < common; i++ {
		out[i] = base[i] ^ delta[i]
	}
	if len(delta) > len(base) {
		copy(out[common:], delta[common:])
	} else {
		copy(out[common:], base[common:])
	}
	return out, nil
}

// rle0kiDecompress ports Dagor's two-bit RLE decoder. The compressed stream's
// final byte records whether a partial destination byte has two or one code.
func rle0kiDecompress(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("empty RLE payload")
	}
	limit := len(src) - 1
	parity := src[limit]
	if parity != 111 && parity != 222 {
		return nil, fmt.Errorf("invalid RLE parity byte 0x%02x", parity)
	}
	maxBytes := maxGroundDeltaRecord
	out := make([]byte, maxBytes)
	outBits := 0
	code := -1
	super := -1
	for i := 0; i < limit; i++ {
		klim := 2
		if i == limit-1 && parity == 111 {
			klim = 1
		}
		for k := 0; k < klim; k++ {
			val := [2]int{int((src[i] >> (k * 4)) & 3), int((src[i] >> (2 + k*4)) & 3)}
			num := 0
			if code != 4 {
				code = val[0]
			}
			switch code {
			case 0: // long run of zero 2-bit units
				num = val[1] + 3
				val[0], val[1] = 0, 0
			case 3:
				num = val[1] + 7
				val[0], val[1] = 0, 0
			case 1:
				code = 4
				super = val[1]
				num = 1
				val[0] = val[1]
			case 2:
				num = 1
				val[0] = val[1]
			case 4:
				code = -1
				num = 2
				if super == 0 && val[0] == 0 && val[1] == 0 {
					num = 39
				}
			}
			if outBits+num*2 > len(out)*8 {
				return nil, fmt.Errorf("RLE output exceeds %d bytes", len(out))
			}
			for n := 0; n < num; n++ {
				v := val[n&1]
				byteIdx := outBits / 8
				shift := outBits % 8
				out[byteIdx] |= byte(v) << shift
				outBits += 2
			}
		}
	}
	return out[:(outBits+7)/8], nil
}
