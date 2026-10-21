package packetecs2

// OpaqueDataComponent preserves a fixed-width component whose data-component
// hash is absent from the supplied ecshashes table. It is only produced when
// PacketECSParser.AllowMissingPODDataComponents is enabled.
type OpaqueDataComponent struct {
	Type ComponentHash
	Raw  []byte
}

func fixedPODComponentBits(maps ComponentHashMaps, typ ComponentHash) (int, bool) {
	name, ok := maps.ComponentNames[uint32(typ)]
	if !ok {
		return 0, false
	}
	switch name {
	case "bool":
		return 1, true
	case "uint8_t":
		return 8, true
	case "uint16_t":
		return 16, true
	case "float", "int", "uint32_t", "E3DCOLOR":
		return 32, true
	case "Point2", "IPoint2":
		return 64, true
	case "Point3", "IPoint3":
		return 96, true
	case "Point4", "IPoint4":
		return 128, true
	default:
		return 0, false
	}
}
