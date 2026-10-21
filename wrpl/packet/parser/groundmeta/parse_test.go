package groundmeta

import (
	"encoding/binary"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	ecs "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

func TestPacketParserBuildsModelsAndReportsTruncation(t *testing.T) {
	maps := ecs.ComponentHashMaps{
		ComponentNames: map[uint32]string{
			0x11111111: "float",
		},
		DataComponents: map[uint32]ecs.DataComponentDef{
			0x5cc7e6cd: {Name: "uid", ComponentHash: 0x95e97e5e},
			0xa2fd7d0c: {Name: "team", ComponentHash: 0x95e97e5e},
			0x1e9a352c: {Name: "unit__className", ComponentHash: 0x33e97a7b},
			0xe9b2598b: {Name: "unit_storage__tank", ComponentHash: 0x3964d2ed},
		},
		DataComponentParsers: map[ecs.DataComponentHash]ecs.ComponentParser{
			0x5cc7e6cd: ecs.ComponentParserFunc(ecs.GenericParser[int32]),
			0xa2fd7d0c: ecs.ComponentParserFunc(ecs.GenericParser[int32]),
			0x1e9a352c: ecs.ComponentParserFunc(ecs.StringParser),
			0xe9b2598b: ecs.ComponentParserFunc(ecs.StorageParser),
		},
	}
	p := NewParser(maps, true)
	p.ecs.TemplateDefs[1] = &ecs.Template{ID: 1, Name: "tank", Components: []ecs.ComponentIdx{100, 101, 102, 103}}
	p.ecs.ComponentDefs[100] = &ecs.HashedComponent{Name: 0x5cc7e6cd, Type: 0x95e97e5e}
	p.ecs.ComponentDefs[101] = &ecs.HashedComponent{Name: 0xa2fd7d0c, Type: 0x95e97e5e}
	p.ecs.ComponentDefs[102] = &ecs.HashedComponent{Name: 0x1e9a352c, Type: 0x33e97a7b}
	p.ecs.ComponentDefs[103] = &ecs.HashedComponent{Name: 0xe9b2598b, Type: 0x3964d2ed}

	raw := fixture(t, "tank-storage.hex")
	block := []byte{1, 0, 4, 0}
	block = binary.LittleEndian.AppendUint32(block, 37)
	block = append(block, 0)
	block = binary.LittleEndian.AppendUint32(block, 2)
	block = append(block, 0)
	block = append(block, []byte("tankModels/synthetic_model")...)
	block = append(block, 0)
	block = append(block, 0)
	block = appendULEB(block, uint64(len(raw))*8)
	block = append(block, raw...)

	pk := &packet.Packet{Seq: 1, PacketType: 6, PacketPayload: constructPayload(0x701, block)}
	if _, err := p.Parse(pk); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	info, ok := p.Lookup(37)
	if !ok {
		t.Fatalf("models=%+v", p.Models)
	}
	if info.RawModel != "tankModels/synthetic_model" || info.StorageModel == "" || info.Team == nil || *info.Team != 2 {
		t.Fatalf("info=%+v", info)
	}
	if info.Source != "ecs:unit__className+ecs:unit_storage__tank#0x6" {
		t.Fatalf("source=%q", info.Source)
	}
	if p.ParsedPackets != 1 || p.ParseErrors != 0 {
		t.Fatalf("packets=%d errors=%d", p.ParsedPackets, p.ParseErrors)
	}

	truncated := block[:len(block)-40]
	pk = &packet.Packet{Seq: 2, PacketType: 6, PacketPayload: constructPayload(0x702, truncated)}
	if _, err := p.Parse(pk); err == nil {
		t.Fatal("truncated construction was accepted")
	}
	if p.ParseErrors != 1 || p.LastError == nil {
		t.Fatalf("errors=%d last=%v", p.ParseErrors, p.LastError)
	}
	if got := p.Models[37]; got.RawModel != "tankModels/synthetic_model" {
		t.Fatalf("previous model changed: %+v", got)
	}
}

func constructPayload(eid uint64, block []byte) []byte {
	buf := []byte{0x24, 0}
	first := uint16(eid<<2 | 1)
	buf = binary.LittleEndian.AppendUint16(buf, first)
	buf = appendULEB(buf, uint64(len(block)))
	buf = append(buf, block...)
	return buf
}

func appendULEB(dst []byte, value uint64) []byte {
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			b |= 0x80
		}
		dst = append(dst, b)
		if value == 0 {
			return dst
		}
	}
}
