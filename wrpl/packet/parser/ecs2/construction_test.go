package packetecs2

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
)

const (
	testClassNameHash = DataComponentHash(0x1e9a352c)
	testClassNameType = ComponentHash(0x33e97a7b)
	testUIDHash       = DataComponentHash(0x5cc7e6cd)
	testUIDType       = ComponentHash(0x95e97e5e)
)

func TestConstructionReservedByteLayouts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reserved bool
	}{
		{name: "legacy"},
		{name: "version gated", reserved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parser := newClassUIDParser()
			parser.ConstructionReservedByte = tc.reserved
			block := syntheticClassUIDBlock(tc.reserved)

			msg, err := parser.ParseECSConstructMessage(messageReader(0x123, block))
			if err != nil {
				t.Fatalf("ParseECSConstructMessage: %v", err)
			}
			if msg.Template != 1 || len(msg.Parsed) != 1 {
				t.Fatalf("template=%d parsed=%d, want 1/1", msg.Template, len(msg.Parsed))
			}
			entity, ok := msg.Parsed[0].(*Entity)
			if !ok {
				t.Fatalf("parsed value is %T, want *Entity", msg.Parsed[0])
			}
			if got, ok := GetObjectData[string](&entity.Data, "unit__className"); !ok || got != "f_16c_block_50" {
				t.Fatalf("className=%q present=%v", got, ok)
			}
			if got, ok := GetObjectData[int32](&entity.Data, "uid"); !ok || got != 423 {
				t.Fatalf("uid=%d present=%v", got, ok)
			}
			if parser.Mgr.Uid_lookup[423] != entity {
				t.Fatal("UID was not registered against the constructed entity")
			}
		})
	}
}

func TestKnownTemplateToolkitBlock(t *testing.T) {
	parser := newPacketECSParser(ComponentHashMaps{
		DataComponents: map[uint32]DataComponentDef{
			0: {Name: "toolkit_value"},
		},
		ComponentParsers: map[ComponentHash]ComponentParser{
			0x7777: ComponentParserFunc(GenericParser[uint16]),
		},
	})
	parser.ConstructionReservedByte = true
	parser.TemplateDefs[8] = &Template{ID: 8, Name: "toolkit_ecs", Components: []ComponentIdx{416, 417, 418, 420, 422, 423, 424, 425}}
	parser.ComponentDefs[423] = &HashedComponent{Type: 0x7777}

	// Captured from creation traffic for replay 0x00018C1C: template 8,
	// reserved zero, one component at local index 5, then uint16 0x027b.
	msg, err := parser.ParseECSConstructMessage(messageReader(0x456, []byte{0x08, 0x00, 0x01, 0x05, 0x7b, 0x02}))
	if err != nil {
		t.Fatalf("ParseECSConstructMessage: %v", err)
	}
	entity, ok := msg.Parsed[0].(*Entity)
	if !ok {
		t.Fatalf("parsed value is %T, want *Entity", msg.Parsed[0])
	}
	if len(entity.Data.Components) != 1 || entity.Data.Components[0].Name != "toolkit_value" {
		t.Fatalf("components=%+v", entity.Data.Components)
	}
	if got, ok := GetObjectData[uint16](&entity.Data, "toolkit_value"); !ok || got != 0x027b {
		t.Fatalf("toolkit value=%#x present=%v", got, ok)
	}
}

func TestConstructionReservedByteRejectsNonzero(t *testing.T) {
	parser := newClassUIDParser()
	parser.ConstructionReservedByte = true
	block := append([]byte{0x01, 0x01}, syntheticConstruction(false)...)
	_, err := parser.ParseECSConstructMessage(messageReader(0x789, block))
	if err == nil || !strings.Contains(err.Error(), "unsupported construction reserved byte: 0x01") {
		t.Fatalf("error=%v, want nonzero reserved byte rejection", err)
	}
}

func newClassUIDParser() *PacketECSParser {
	parser := newPacketECSParser(ComponentHashMaps{
		DataComponents: map[uint32]DataComponentDef{
			uint32(testClassNameHash): {Name: "unit__className", ComponentHash: uint32(testClassNameType)},
			uint32(testUIDHash):       {Name: "uid", ComponentHash: uint32(testUIDType)},
		},
		DataComponentParsers: map[DataComponentHash]ComponentParser{
			testClassNameHash: ComponentParserFunc(StringParser),
			testUIDHash:       ComponentParserFunc(GenericParser[int32]),
		},
	})
	parser.TemplateDefs[1] = &Template{ID: 1, Name: "aircraft", Components: []ComponentIdx{100, 101}}
	parser.ComponentDefs[100] = &HashedComponent{Name: testClassNameHash, Type: testClassNameType}
	parser.ComponentDefs[101] = &HashedComponent{Name: testUIDHash, Type: testUIDType}
	return parser
}

func newPacketECSParser(maps ComponentHashMaps) *PacketECSParser {
	parser := NewPacketECSParser(maps)
	parser.Maps = maps
	return parser
}

func syntheticClassUIDBlock(reserved bool) []byte {
	block := []byte{0x01}
	if reserved {
		block = append(block, 0)
	}
	return append(block, syntheticConstruction(false)...)
}

func syntheticConstruction(reserved bool) []byte {
	if reserved {
		return append([]byte{0}, syntheticConstruction(false)...)
	}
	data := []byte{2, 0}
	data = append(data, []byte("f_16c_block_50")...)
	data = append(data, 0, 0)
	uid := make([]byte, 4)
	binary.LittleEndian.PutUint32(uid, 423)
	return append(data, uid...)
}

func messageReader(eid uint64, block []byte) *danet.BitReader {
	buf := make([]byte, 0, 4+len(block))
	first := uint16(eid<<2 | 1)
	buf = binary.LittleEndian.AppendUint16(buf, first)
	for n := uint64(len(block)); ; n >>= 7 {
		b := byte(n & 0x7f)
		if n > 0x7f {
			b |= 0x80
		}
		buf = append(buf, b)
		if n <= 0x7f {
			break
		}
	}
	buf = append(buf, block...)
	return danet.NewBitReader(buf)
}
