package packetecs2

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
)

type ecsAllocationListParser struct {
	name  string
	parse func(*danet.BitReader, *PacketECSParser) (any, error)
}

func ecsAllocationListParsers() []ecsAllocationListParser {
	return []ecsAllocationListParser{
		{name: "generic", parse: GenericListParser[uint8]},
		{name: "bool", parse: BoolListParser},
		{name: "part id", parse: DmPartIdListParser},
		{name: "string", parse: StringListParser},
		{name: "entity id", parse: EidListParser},
		{name: "array", parse: ArrayParser},
		{name: "object", parse: ObjectParser},
	}
}

func TestReadECSCompressedMaxUint64(t *testing.T) {
	want := ^uint64(0)
	encoded := binary.AppendUvarint(nil, want)
	got, err := readECSCompressed(danet.NewBitReader(encoded))
	if err != nil {
		t.Fatalf("readECSCompressed: %v", err)
	}
	if got != want {
		t.Fatalf("readECSCompressed=%d, want %d", got, want)
	}
}

func TestReadECSCompressedRejectsOverlongULEB(t *testing.T) {
	encoded := bytes.Repeat([]byte{0x80}, 11)
	_, err := readECSCompressed(danet.NewBitReader(encoded))
	if err == nil {
		t.Fatal("overlong ULEB128 value was accepted")
	}
	if !strings.Contains(err.Error(), "ten bytes") {
		t.Fatalf("error=%q, want overlong ULEB rejection", err)
	}
}

func TestECSListReportedCountsRejected(t *testing.T) {
	counts := []uint64{
		29_601_127_424,
		3_474_186_254_336,
	}
	for _, count := range counts {
		for _, tc := range ecsAllocationListParsers() {
			parser := NewPacketECSParser(ComponentHashMaps{})
			r := danet.NewBitReader(binary.AppendUvarint(nil, count))
			_, err := tc.parse(r, parser)
			if err == nil {
				t.Fatalf("%s count %d was accepted", tc.name, count)
			}
			if !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("%s count %d error=%q, want allocation-limit rejection", tc.name, count, err)
			}
		}
	}
}

func TestECSListInsufficientRemainingBits(t *testing.T) {
	for _, tc := range ecsAllocationListParsers() {
		t.Run(tc.name, func(t *testing.T) {
			parser := NewPacketECSParser(ComponentHashMaps{})
			r := danet.NewBitReader([]byte{1})
			_, err := tc.parse(r, parser)
			if err == nil {
				t.Fatal("one-item list with no item payload was accepted")
			}
			if !strings.Contains(err.Error(), "remain") {
				t.Fatalf("error=%q, want remaining-bits rejection", err)
			}
		})
	}
}

func TestECSPartIDRawCountLimitBeforeExpansion(t *testing.T) {
	rawCount := uint64(ecsMaxAllocationCount)/8 + 1
	r := danet.NewBitReader(binary.AppendUvarint(nil, rawCount))
	_, err := DmPartIdListParser(r, NewPacketECSParser(ComponentHashMaps{}))
	if err == nil {
		t.Fatalf("raw count %d was accepted", rawCount)
	}
	if !strings.Contains(err.Error(), "raw count") || !strings.Contains(err.Error(), "expanded allocation limit") {
		t.Fatalf("error=%q, want PartId raw-count overflow rejection", err)
	}
}

func TestECSComponentNestingLimit(t *testing.T) {
	parser := NewPacketECSParser(ComponentHashMaps{})
	parser.componentParseDepth = ecsMaxComponentNesting
	r := danet.NewBitReader([]byte{1, 0, 0, 0})
	_, err := deserialize_child_component(r, parser)
	if err == nil {
		t.Fatal("component nesting at the depth limit was accepted")
	}
	if !strings.Contains(err.Error(), "maximum depth") {
		t.Fatalf("error=%q, want nesting-depth rejection", err)
	}
}
