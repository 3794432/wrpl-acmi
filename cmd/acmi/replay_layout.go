package main

import (
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

func newReplayECSParser(header wrpl.WRPLHeader, maps packetecs2.ComponentHashMaps) *packetecs2.PacketECSParser {
	p := packetecs2.NewPacketECSParser(maps)
	p.ConstructionReservedByte = header.UsesModernLayout()
	// Preserve identities when a newer replay adds an unnamed fixed-width
	// POD component. Unknown variable-width types and known fields with missing
	// serializers still fail; no semantics are guessed for the opaque bytes.
	p.AllowMissingPODDataComponents = true
	return p
}
