package packetecs2

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

const (
	testKnownData = DataComponentHash(0x11111111)
	testKnownType = ComponentHash(0x22222222)
	testMissData  = DataComponentHash(0x80223126)
	testFloatType = ComponentHash(0xa6c45d85)
	testIntType   = ComponentHash(0x95e97e5e)
)

func missingComponentParser() *PacketECSParser {
	p := newPacketECSParser(ComponentHashMaps{
		ComponentNames: map[uint32]string{
			uint32(testKnownType): "uint32_t",
			uint32(testFloatType): "float",
			uint32(testIntType):   "int",
		},
		DataComponents: map[uint32]DataComponentDef{
			uint32(testKnownData): {Name: "known", ComponentHash: uint32(testKnownType)},
			uint32(testUIDHash):   {Name: "uid", ComponentHash: uint32(testIntType)},
		},
		DataComponentParsers: map[DataComponentHash]ComponentParser{
			testKnownData: ComponentParserFunc(GenericParser[uint32]),
			testUIDHash:   ComponentParserFunc(GenericParser[int32]),
		},
	})
	p.ConstructionReservedByte = true
	p.TemplateDefs[1] = &Template{ID: 1, Name: "server_unit", Components: []ComponentIdx{100, 101, 102}}
	p.ComponentDefs[100] = &HashedComponent{Name: testKnownData, Type: testKnownType}
	p.ComponentDefs[101] = &HashedComponent{Name: testMissData, Type: testFloatType}
	p.ComponentDefs[102] = &HashedComponent{Name: testUIDHash, Type: testIntType}
	return p
}

func opaqueBlock() []byte {
	block := []byte{1, 0, 3, 0}
	block = binary.LittleEndian.AppendUint32(block, 42)
	block = append(block, 0)
	block = binary.LittleEndian.AppendUint32(block, math.Float32bits(1.5))
	block = append(block, 0)
	block = binary.LittleEndian.AppendUint32(block, 7)
	return block
}

func TestMissingPODDataComponentRequiresOptIn(t *testing.T) {
	p := missingComponentParser()
	_, err := p.ParseECSConstructMessage(messageReader(0x501, opaqueBlock()))
	if err == nil || !strings.Contains(err.Error(), "serializer not found") {
		t.Fatalf("default parser error=%v, want missing serializer rejection", err)
	}
	if len(p.Mgr.Entities) != 0 || len(p.Mgr.Uid_lookup) != 0 {
		t.Fatalf("failed construction mutated entities=%d uid=%d", len(p.Mgr.Entities), len(p.Mgr.Uid_lookup))
	}

	p = missingComponentParser()
	p.AllowMissingPODDataComponents = true
	msg, err := p.ParseECSConstructMessage(messageReader(0x501, opaqueBlock()))
	if err != nil {
		t.Fatalf("opt-in parse: %v", err)
	}
	if len(msg.Parsed) != 1 {
		t.Fatalf("parsed=%d", len(msg.Parsed))
	}
	entity := msg.Parsed[0].(*Entity)
	if got, ok := GetObjectData[uint32](&entity.Data, "known"); !ok || got != 42 {
		t.Fatalf("known=%d,%v", got, ok)
	}
	opaque, ok := GetObjectData[OpaqueDataComponent](&entity.Data, "missing_data_0x80223126")
	if !ok {
		t.Fatalf("opaque component absent: %+v", entity.Data.Components)
	}
	if opaque.Type != testFloatType || len(opaque.Raw) != 4 || math.Float32frombits(binary.LittleEndian.Uint32(opaque.Raw)) != 1.5 {
		t.Fatalf("opaque=%+v", opaque)
	}
	if got, ok := GetObjectData[int32](&entity.Data, "uid"); !ok || got != 7 {
		t.Fatalf("uid=%d,%v", got, ok)
	}
	if p.Mgr.Uid_lookup[7] != entity {
		t.Fatal("UID 7 not registered")
	}
	if _, ok := p.Mgr.Uid_lookup[7]; !ok {
		t.Fatal("missing UID lookup")
	}

	// A later construction on the same template still aligns: component index
	// two is uid=8 after the opaque float was skipped.
	next := []byte{1, 0, 1, 2}
	next = binary.LittleEndian.AppendUint32(next, 8)
	msg, err = p.ParseECSConstructMessage(messageReader(0x502, next))
	if err != nil {
		t.Fatalf("second construction: %v", err)
	}
	second := msg.Parsed[0].(*Entity)
	if got, ok := GetObjectData[int32](&second.Data, "uid"); !ok || got != 8 {
		t.Fatalf("second uid=%d,%v", got, ok)
	}
}

func TestOptInDoesNotChangeKnownConstruction(t *testing.T) {
	block := []byte{1, 0, 2, 0}
	block = binary.LittleEndian.AppendUint32(block, 99)
	block = append(block, 1)
	block = binary.LittleEndian.AppendUint32(block, 12)
	var parsed []*Entity
	for _, opt := range []bool{false, true} {
		p := missingComponentParser()
		p.AllowMissingPODDataComponents = opt
		msg, err := p.ParseECSConstructMessage(messageReader(0x601, block))
		if err != nil {
			t.Fatalf("opt=%v: %v", opt, err)
		}
		parsed = append(parsed, msg.Parsed[0].(*Entity))
	}
	a, b := parsed[0], parsed[1]
	if len(a.Data.Components) != 2 || len(b.Data.Components) != 2 {
		t.Fatalf("components=%d/%d", len(a.Data.Components), len(b.Data.Components))
	}
	for i := range a.Data.Components {
		if a.Data.Components[i].Name != b.Data.Components[i].Name {
			t.Fatalf("component %d name %q != %q", i, a.Data.Components[i].Name, b.Data.Components[i].Name)
		}
	}
	if va, _ := GetObjectData[uint32](&a.Data, "known"); va != 99 {
		t.Fatalf("known=%d", va)
	}
	if vb, _ := GetObjectData[int32](&b.Data, "uid"); vb != 12 {
		t.Fatalf("uid=%d", vb)
	}
}

func TestKnownDataComponentStillRequiresSerializer(t *testing.T) {
	p := missingComponentParser()
	p.Maps.DataComponents[uint32(testMissData)] = DataComponentDef{
		Name:          "server_only",
		ComponentHash: uint32(testFloatType),
	}
	p.AllowMissingPODDataComponents = true
	if _, err := p.ParseECSConstructMessage(messageReader(0x701, opaqueBlock())); err == nil || !strings.Contains(err.Error(), "serializer not found") {
		t.Fatalf("known data component without serializer error=%v", err)
	}
}
