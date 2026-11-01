package main

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

// Identity must survive an unknown float inserted between className and uid,
// both in the old construction layout and in newer replay revisions.
func TestReplayAircraftIdentityWithNewPODComponent(t *testing.T) {
	for _, version := range []int32{0x00018C1B, 0x00018C1C, 0x00018C28, 0x00018C29} {
		t.Run(fmt.Sprintf("%x", version), func(t *testing.T) {
			maps, err := readHashes("embedded")
			if err != nil {
				t.Fatal(err)
			}
			const unknown = packetecs2.DataComponentHash(0x80223126)
			delete(maps.DataComponents, uint32(unknown))
			delete(maps.DataComponentParsers, unknown)
			p := newReplayECSParser(wrpl.WRPLHeader{Version: version}, *maps)
			p.TemplateDefs[1] = &packetecs2.Template{ID: 1, Name: "aircraft", Components: []packetecs2.ComponentIdx{100, 101, 102}}
			p.ComponentDefs[100] = &packetecs2.HashedComponent{Name: 0x1e9a352c, Type: 0x33e97a7b}
			p.ComponentDefs[101] = &packetecs2.HashedComponent{Name: unknown, Type: 0xa6c45d85}
			p.ComponentDefs[102] = &packetecs2.HashedComponent{Name: 0x5cc7e6cd, Type: 0x95e97e5e}
			block := []byte{1} // template
			if version >= 0x00018C1C {
				block = append(block, 0) // reserved
			}
			block = append(block, 3, 0) // three components, first local index zero
			block = append(block, []byte("f_16c_block_50")...)
			block = append(block, 0, 0)                                 // string end, next local index
			block = binary.LittleEndian.AppendUint32(block, 0x3fc00000) // opaque float 1.5
			block = append(block, 0)
			block = binary.LittleEndian.AppendUint32(block, 423)
			wire := binary.LittleEndian.AppendUint16(nil, 0x123<<2|1)
			wire = binary.AppendUvarint(wire, uint64(len(block)))
			wire = append(wire, block...)
			if _, err := p.ParseECSConstructMessage(danet.NewBitReader(wire)); err != nil {
				t.Fatal(err)
			}
			entity := p.Mgr.Uid_lookup[423]
			if entity == nil {
				t.Fatal("aircraft UID missing after unknown POD")
			}
			if model, ok := packetecs2.GetObjectData[string](&entity.Data, "unit__className"); !ok || model != "f_16c_block_50" {
				t.Fatalf("model=%q present=%v", model, ok)
			}
		})
	}
}
