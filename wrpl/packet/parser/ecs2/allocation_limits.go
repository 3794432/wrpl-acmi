package packetecs2

import (
	"fmt"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
)

const (
	ecsMaxAllocationCount  = 1_000_000
	ecsMaxComponentNesting = 64
)

func ecsRemainingBits(r *danet.BitReader) (uint64, error) {
	if r == nil {
		return 0, fmt.Errorf("ECS parser received a nil bit reader")
	}
	if r.BitOffset < 0 {
		return 0, fmt.Errorf("ECS parser invalid bit reader state: bit offset %d is negative (data length %d bytes)", r.BitOffset, len(r.Data))
	}
	if len(r.Data) > int(^uint(0)>>1)/8 {
		return 0, fmt.Errorf("ECS parser bit reader data length %d bytes is too large", len(r.Data))
	}
	dataBits := uint64(len(r.Data)) * 8
	if uint64(r.BitOffset) > dataBits {
		return 0, fmt.Errorf("ECS parser invalid bit reader state: bit offset %d is beyond data length %d bytes", r.BitOffset, len(r.Data))
	}
	return dataBits - uint64(r.BitOffset), nil
}

func ecsCheckListCount(r *danet.BitReader, label string, count, minBitsPerItem uint64) (int, error) {
	remaining, err := ecsRemainingBits(r)
	if err != nil {
		return 0, err
	}
	if count > uint64(ecsMaxAllocationCount) {
		return 0, fmt.Errorf("ECS %s count %d exceeds hard allocation limit %d at bit offset %d (data length %d bytes)", label, count, ecsMaxAllocationCount, r.BitOffset, len(r.Data))
	}
	if count > 0 && minBitsPerItem > 0 && count > remaining/minBitsPerItem {
		return 0, fmt.Errorf("ECS %s count %d requires at least %d bits per item, but only %d bits remain at bit offset %d (data length %d bytes)", label, count, minBitsPerItem, remaining, r.BitOffset, len(r.Data))
	}
	return int(count), nil
}

func ecsCheckPartIDListCount(r *danet.BitReader, rawCount uint64) (int, error) {
	if _, err := ecsRemainingBits(r); err != nil {
		return 0, err
	}
	const rawCountLimit = uint64(ecsMaxAllocationCount) / 8
	if rawCount > rawCountLimit {
		return 0, fmt.Errorf("ECS dm::PartIdList raw count %d exceeds expanded allocation limit %d at bit offset %d (data length %d bytes)", rawCount, rawCountLimit, r.BitOffset, len(r.Data))
	}
	return ecsCheckListCount(r, "dm::PartIdList", rawCount*8, 6)
}

func ecsCheckByteBlock(r *danet.BitReader, label string, size uint64) (int, error) {
	remaining, err := ecsRemainingBits(r)
	if err != nil {
		return 0, err
	}
	if size > uint64(ecsMaxAllocationCount) {
		return 0, fmt.Errorf("ECS %s size %d bytes exceeds hard allocation limit %d at bit offset %d (data length %d bytes)", label, size, ecsMaxAllocationCount, r.BitOffset, len(r.Data))
	}
	if size > remaining/8 {
		return 0, fmt.Errorf("ECS %s size %d bytes requires %d bits, but only %d bits remain at bit offset %d (data length %d bytes)", label, size, size*8, remaining, r.BitOffset, len(r.Data))
	}
	return int(size), nil
}

func ecsLimitedLZ4BufferSize(compressedBytes int) (int, error) {
	if compressedBytes < 0 {
		return 0, fmt.Errorf("ECS lz4 compressed size %d is negative", compressedBytes)
	}
	if uint64(compressedBytes) > uint64(ecsMaxAllocationCount)/8 {
		return ecsMaxAllocationCount, nil
	}
	return compressedBytes * 8, nil
}

// readECSCompressed accepts the same uint64 ULEB128 values as BitReader.ReadCompressed,
// but rejects encodings that exceed ten bytes or overflow uint64.
func readECSCompressed(r *danet.BitReader) (uint64, error) {
	if _, err := ecsRemainingBits(r); err != nil {
		return 0, err
	}
	startOffset := r.BitOffset
	var value uint64
	for i := 0; i < 10; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("ECS compressed value starting at bit offset %d (data length %d bytes): %w", startOffset, len(r.Data), err)
		}
		payload := uint64(b & 0x7f)
		shift := uint(i * 7)
		if i == 9 && payload > 1 {
			return 0, fmt.Errorf("ECS compressed value starting at bit offset %d overflows uint64 (data length %d bytes)", startOffset, len(r.Data))
		}
		value |= payload << shift
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, fmt.Errorf("ECS compressed value starting at bit offset %d exceeds ten bytes (data length %d bytes)", startOffset, len(r.Data))
}
