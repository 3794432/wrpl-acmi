package projectiles

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	ecsp "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/pierrec/lz4/v4"
)

const (
	testUIDData       ecsp.DataComponentHash = 0x05060708
	testUIDType       ecsp.ComponentHash     = 0x95e97e5e
	testClassNameData ecsp.DataComponentHash = 0x01020304
	testClassNameType ecsp.ComponentHash     = 0x0a0b0c0d
	testRocketData    ecsp.DataComponentHash = 0xfe2545cd
	testRocketType    ecsp.ComponentHash     = 0x2c8f7a75
	testBombData      ecsp.DataComponentHash = 0xcd1063e9
	testBombType      ecsp.ComponentHash     = 0x3178acb1
	testVelocityData  ecsp.DataComponentHash = 0x33333333
	testVelocityType  ecsp.ComponentHash     = 0x44444444
	testUnknownData   ecsp.DataComponentHash = 0x11111111
	testUnknownType   ecsp.ComponentHash     = 0x22222222
)

type spawnWireComponent struct {
	id       ecsp.ComponentIdx
	dataHash ecsp.DataComponentHash
	typeHash ecsp.ComponentHash
}

type spawnWireValue struct {
	local int
	data  []byte
}

func TestMetadataParserProjectileOwnerAndExactDestruction(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), true)
	ownerEID := encodeSpawnEID(0x123, 2)
	projectileEID := encodeSpawnEID(0x2345, 3)
	aliasEID := encodeSpawnEID(0x2345, 4)
	weaponRef := uint32(0xa1b2c3d4)

	owner := spawnTemplateBlock(1, "aircraft", []spawnWireComponent{
		{id: 100, dataHash: testClassNameData, typeHash: testClassNameType},
		{id: 101, dataHash: testUIDData, typeHash: testUIDType},
	}, true, []spawnWireValue{
		{local: 0, data: append([]byte("f_16c_block_50"), 0)},
		{local: 1, data: spawnU32(uint32(423))},
	})
	unsupported := spawnTemplateBlock(2, "unsupported", []spawnWireComponent{
		{id: 102, dataHash: testUnknownData, typeHash: testUnknownType},
	}, true, []spawnWireValue{{local: 0, data: []byte{1, 2, 3}}})
	rocket := spawnTemplateBlock(3, "rocket", []spawnWireComponent{
		{id: 103, dataHash: testRocketData, typeHash: testRocketType},
	}, true, []spawnWireValue{{local: 0, data: spawnRocketValue(ownerEID, encodeSpawnEID(0x999, 1), weaponRef, 11.5, -22.25, 33.125)}})

	packetData := spawnConstructionPacket(
		spawnFrame(ownerEID, owner),
		spawnFrame(encodeSpawnEID(0x3456, 1), unsupported),
		spawnFrame(projectileEID, rocket),
	)
	if _, err := p.Parse(&packet.Packet{CurrentTime: 1000, PacketPayload: packetData}); err != nil {
		t.Fatalf("Parse construction: %v", err)
	}
	if len(p.Tracks) != 1 {
		t.Fatalf("tracks=%d, want 1", len(p.Tracks))
	}
	projectile := p.Tracks[projectileEID]
	if projectile == nil {
		t.Fatalf("projectile %#x was not tracked", projectileEID)
	}
	if projectile.OwnerEID != ownerEID || !projectile.HasParent || projectile.ParentUID != 423 {
		t.Fatalf("owner=%#x parent=%d present=%v", projectile.OwnerEID, projectile.ParentUID, projectile.HasParent)
	}
	if projectile.Name != "Missile" {
		t.Fatalf("name=%q, want Missile", projectile.Name)
	}
	if projectile.Kind != "" {
		t.Fatalf("rocket kind=%q, want empty", projectile.Kind)
	}
	if projectile.Template != "rocket" || projectile.WeaponRef != weaponRef || projectile.LeadingULEB != 0x2a {
		t.Fatalf("template=%q leading=%#x weaponRef=%#x", projectile.Template, projectile.LeadingULEB, projectile.WeaponRef)
	}
	if projectile.SpawnSample != (Sample{Time: 1000, X: 11.5, Y: -22.25, Z: 33.125}) {
		t.Fatalf("spawn sample=%+v", projectile.SpawnSample)
	}
	if projectile.SpawnSample.FromSync {
		t.Fatal("constructor sample was marked as sync")
	}

	if _, err := p.Parse(&packet.Packet{CurrentTime: 1100, PacketPayload: spawnDestructionPacket(aliasEID)}); err != nil {
		t.Fatalf("Parse alias destruction: %v", err)
	}
	if projectile.HasEnd {
		t.Fatal("destruction for a different generation was applied by entity index")
	}
	if _, err := p.Parse(&packet.Packet{CurrentTime: 1500, PacketPayload: spawnDestructionPacket(projectileEID)}); err != nil {
		t.Fatalf("Parse destruction: %v", err)
	}
	if !projectile.HasEnd || projectile.EndTime != 1500 {
		t.Fatalf("end=%v time=%d, want true/1500", projectile.HasEnd, projectile.EndTime)
	}
}

func TestMetadataParserTracksBombWithSharedRocketLayout(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), false)
	ownerEID := encodeSpawnEID(0x421, 2)
	bombEID := encodeSpawnEID(0x522, 3)

	owner := spawnTemplateBlock(40, "aircraft", []spawnWireComponent{
		{id: 600, dataHash: testUIDData, typeHash: testUIDType},
	}, false, []spawnWireValue{{local: 0, data: spawnU32(777)}})
	bomb := spawnTemplateBlock(41, "bomb", []spawnWireComponent{
		{id: 601, dataHash: testBombData, typeHash: testBombType},
	}, false, []spawnWireValue{{local: 0, data: spawnRocketValue(ownerEID, 0, 0x00080021, 101.5, -202.25, 303.125)}})

	data := spawnConstructionPacket(
		spawnFrame(ownerEID, owner),
		spawnFrame(bombEID, bomb),
	)
	if _, err := p.Parse(&packet.Packet{CurrentTime: 6000, PacketPayload: data}); err != nil {
		t.Fatalf("Parse bomb construction: %v", err)
	}
	projectile := p.Tracks[bombEID]
	if projectile == nil {
		t.Fatalf("bomb %#x was not tracked", bombEID)
	}
	if projectile.Kind != "bomb" || projectile.Name != "Bomb" {
		t.Fatalf("kind=%q name=%q, want bomb/Bomb", projectile.Kind, projectile.Name)
	}
	if projectile.Template != "bomb" || projectile.OwnerEID != ownerEID || projectile.WeaponRef != 0x00080021 {
		t.Fatalf("template=%q owner=%#x weaponRef=%#x", projectile.Template, projectile.OwnerEID, projectile.WeaponRef)
	}
	if !projectile.HasParent || projectile.ParentUID != 777 {
		t.Fatalf("parent uid=%d present=%v", projectile.ParentUID, projectile.HasParent)
	}
	if projectile.SpawnSample != (Sample{Time: 6000, X: 101.5, Y: -202.25, Z: 303.125}) {
		t.Fatalf("bomb spawn sample=%+v", projectile.SpawnSample)
	}
	if !projectile.HasTemplateComponent("bomb_component") || projectile.HasTemplateComponent("rocket_component") {
		t.Fatalf("bomb template components=%#v", projectile.TemplateComponents)
	}

	if _, err := p.Parse(&packet.Packet{CurrentTime: 6500, PacketPayload: spawnDestructionPacket(bombEID)}); err != nil {
		t.Fatalf("Parse bomb destruction: %v", err)
	}
	if !projectile.HasEnd || projectile.EndTime != 6500 {
		t.Fatalf("bomb end=%v time=%d", projectile.HasEnd, projectile.EndTime)
	}
}

func TestMetadataParserFindsBombAfterParsedPrefixComponent(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), false)
	eid := encodeSpawnEID(0x523, 1)
	bomb := spawnTemplateBlock(42, "bomb", []spawnWireComponent{
		{id: 602, dataHash: testUIDData, typeHash: testUIDType},
		{id: 603, dataHash: testBombData, typeHash: testBombType},
	}, false, []spawnWireValue{
		{local: 0, data: spawnU32(1234)},
		{local: 1, data: spawnRocketValue(1, 2, 3, 7, 8, 9)},
	})

	if _, err := p.Parse(&packet.Packet{CurrentTime: 6600, PacketPayload: spawnConstructionPacket(spawnFrame(eid, bomb))}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	projectile := p.Tracks[eid]
	if projectile == nil || projectile.Kind != "bomb" || projectile.Name != "Bomb" {
		t.Fatalf("bomb after uid prefix was not decoded: %+v", projectile)
	}
	if projectile.SpawnSample.X != 7 || projectile.SpawnSample.Y != 8 || projectile.SpawnSample.Z != 9 {
		t.Fatalf("bomb position=%+v", projectile.SpawnSample)
	}
}

func TestMetadataParserRecordsTemplateSchemaWithoutGuessingFlightValues(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), false)
	eid := encodeSpawnEID(0x55a, 1)
	rocket := spawnTemplateBlock(50, "rocket", []spawnWireComponent{
		{id: 500, dataHash: testRocketData, typeHash: testRocketType},
		{id: 501, dataHash: testVelocityData, typeHash: testVelocityType},
	}, false, []spawnWireValue{{local: 0, data: spawnRocketValue(10, 11, 12, 1, 2, 3)}})

	if _, err := p.Parse(&packet.Packet{CurrentTime: 5000, PacketPayload: spawnConstructionPacket(spawnFrame(eid, rocket))}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	projectile := p.Tracks[eid]
	if projectile == nil {
		t.Fatal("projectile was not tracked")
	}
	want := []string{"rocket_component", "proj__velocity"}
	if len(projectile.TemplateComponents) != len(want) {
		t.Fatalf("template components = %#v, want %#v", projectile.TemplateComponents, want)
	}
	for i := range want {
		if projectile.TemplateComponents[i] != want[i] {
			t.Fatalf("template components = %#v, want %#v", projectile.TemplateComponents, want)
		}
	}
	if !projectile.HasTemplateComponent("proj__velocity") || projectile.HasTemplateComponent("proj__omega") {
		t.Fatalf("template component lookup = %#v", projectile.TemplateComponents)
	}
	if projectile.Flight != nil {
		t.Fatalf("schema-only component produced evidence: %+v", projectile.Flight)
	}
}

func TestFlightEvidenceRequiresFiniteDecodedPoint3(t *testing.T) {
	var evidence FlightEvidence
	if _, ok := evidence.Point3(FlightVelocity); ok {
		t.Fatal("absent velocity reported present")
	}
	if evidence.SetPoint3(FlightVelocity, ecsp.Point3{X: float32(math.NaN()), Y: 1, Z: 2}) {
		t.Fatal("non-finite Point3 was accepted")
	}
	if evidence.SetPoint3(FlightOmega, ecsp.Point3{X: float32(math.Inf(-1)), Y: 1, Z: 2}) {
		t.Fatal("infinite Point3 was accepted")
	}
	if evidence.SetPoint3(FlightField(99), ecsp.Point3{X: 1, Y: 2, Z: 3}) {
		t.Fatal("unknown field was accepted")
	}
	if _, ok := evidence.Point3(FlightVelocity); ok {
		t.Fatal("rejected value changed presence")
	}

	if !evidence.SetPoint3(FlightPrevOmega, ecsp.Point3{X: 4, Y: 5, Z: 6}) {
		t.Fatal("finite Point3 was rejected")
	}
	got, ok := evidence.Point3(FlightPrevOmega)
	if !ok || !got.Present || got.X != 4 || got.Y != 5 || got.Z != 6 {
		t.Fatalf("prev omega = %+v present=%v", got, ok)
	}
	if _, ok := evidence.Point3(FlightOmega); ok {
		t.Fatal("unset omega reported present")
	}
}

func TestMetadataParserResolvesOwnerAfterProjectile(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), false)
	ownerEID := encodeSpawnEID(0x777, 1)
	projectileEID := encodeSpawnEID(0x888, 1)
	rocket := spawnTemplateBlock(10, "rocket+arh_shell", []spawnWireComponent{
		{id: 200, dataHash: testRocketData, typeHash: testRocketType},
	}, false, []spawnWireValue{{local: 0, data: spawnRocketValue(ownerEID, 0, 7, 1, 2, 3)}})
	owner := spawnTemplateBlock(11, "aircraft", []spawnWireComponent{
		{id: 201, dataHash: testUIDData, typeHash: testUIDType},
	}, false, []spawnWireValue{{local: 0, data: spawnU32(9911)}})

	data := spawnConstructionPacket(
		spawnFrame(projectileEID, rocket),
		spawnFrame(ownerEID, owner),
	)
	if _, err := p.Parse(&packet.Packet{CurrentTime: 2000, PacketPayload: data}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	projectile := p.Tracks[projectileEID]
	if projectile == nil || !projectile.HasParent || projectile.ParentUID != 9911 {
		t.Fatalf("late owner was not linked: %+v", projectile)
	}
}

func TestMetadataParserRetainsReconstructedSameEID(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), false)
	eid := encodeSpawnEID(0x909, 1)
	first := spawnTemplateBlock(12, "rocket", []spawnWireComponent{
		{id: 202, dataHash: testRocketData, typeHash: testRocketType},
	}, false, []spawnWireValue{{local: 0, data: spawnRocketValue(1, 2, 3, 4, 5, 6)}})
	second := spawnTemplateBlock(13, "rocket+arh_shell", []spawnWireComponent{
		{id: 203, dataHash: testRocketData, typeHash: testRocketType},
	}, false, []spawnWireValue{{local: 0, data: spawnRocketValue(1, 2, 7, 8, 9, 10)}})

	if _, err := p.Parse(&packet.Packet{CurrentTime: 1000, PacketPayload: spawnConstructionPacket(spawnFrame(eid, first))}); err != nil {
		t.Fatalf("first construction: %v", err)
	}
	if _, err := p.Parse(&packet.Packet{CurrentTime: 1100, PacketPayload: spawnDestructionPacket(eid)}); err != nil {
		t.Fatalf("first destruction: %v", err)
	}
	if _, err := p.Parse(&packet.Packet{CurrentTime: 2000, PacketPayload: spawnConstructionPacket(spawnFrame(eid, second))}); err != nil {
		t.Fatalf("second construction: %v", err)
	}
	if got := len(p.History[eid]); got != 2 {
		t.Fatalf("history=%d, want 2", got)
	}
	if !p.History[eid][0].HasEnd || p.History[eid][0].EndTime != 1100 {
		t.Fatalf("first instance lost its lifetime: %+v", p.History[eid][0])
	}
	if p.Tracks[eid] != p.History[eid][1] || p.Tracks[eid].SpawnSample.Time != 2000 {
		t.Fatal("Tracks did not advance to the reconstructed instance")
	}

	if _, err := p.Parse(&packet.Packet{CurrentTime: 2100, PacketPayload: spawnDestructionPacket(eid)}); err != nil {
		t.Fatalf("second destruction: %v", err)
	}
	if p.History[eid][0].EndTime != 1100 || !p.History[eid][1].HasEnd || p.History[eid][1].EndTime != 2100 {
		t.Fatalf("destruction closed the wrong instance: %+v", p.History[eid])
	}
}

func TestMetadataParserCandidatesAndFiniteCoordinates(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), false)
	valid := spawnRocketValue(1, 2, 3, 4, 5, 6)
	// A template named "bomb" does not prove that its Rocket body is a Bomb
	// component; only the component definition does.
	nameOnlyBomb := spawnTemplateBlock(20, "bomb", []spawnWireComponent{
		{id: 300, dataHash: testRocketData, typeHash: testRocketType},
	}, false, []spawnWireValue{{local: 0, data: valid}})
	invalid := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(invalid[len(invalid)-12:], math.Float32bits(float32(math.NaN())))
	nanRocket := spawnTemplateBlock(21, "rocket", []spawnWireComponent{
		{id: 301, dataHash: testRocketData, typeHash: testRocketType},
	}, false, []spawnWireValue{{local: 0, data: invalid}})
	nanBomb := spawnTemplateBlock(22, "bomb", []spawnWireComponent{
		{id: 302, dataHash: testBombData, typeHash: testBombType},
	}, false, []spawnWireValue{{local: 0, data: invalid}})

	data := spawnConstructionPacket(
		spawnFrame(encodeSpawnEID(0x901, 1), nameOnlyBomb),
		spawnFrame(encodeSpawnEID(0x902, 1), nanRocket),
		spawnFrame(encodeSpawnEID(0x903, 1), nanBomb),
	)
	if _, err := p.Parse(&packet.Packet{CurrentTime: 3000, PacketPayload: data}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Tracks) != 0 {
		t.Fatalf("tracks=%d, want no guessed or non-finite construction", len(p.Tracks))
	}
}

func TestMetadataParserRejectsTruncatedBombConstruction(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), false)
	eid := encodeSpawnEID(0x904, 1)
	value := spawnRocketValue(1, 2, 3, 4, 5, 6)
	bomb := spawnTemplateBlock(23, "bomb", []spawnWireComponent{
		{id: 303, dataHash: testBombData, typeHash: testBombType},
	}, false, []spawnWireValue{{local: 0, data: value[:5]}})

	if _, err := p.Parse(&packet.Packet{CurrentTime: 3100, PacketPayload: spawnConstructionPacket(spawnFrame(eid, bomb))}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Tracks) != 0 || len(p.History) != 0 {
		t.Fatalf("truncated bomb was retained: tracks=%+v history=%+v", p.Tracks, p.History)
	}
}

func TestMetadataParserExpandsLZ4ForLargeBoundedBlock(t *testing.T) {
	p := NewMetadataParser(spawnTestMaps(), true)
	eid := encodeSpawnEID(0xabc, 1)
	block := spawnTemplateBlock(30, "rocket", []spawnWireComponent{
		{id: 400, dataHash: testRocketData, typeHash: testRocketType},
	}, true, []spawnWireValue{{local: 0, data: spawnRocketValue(44, 55, 66, 7, 8, 9)}})
	block = append(block, make([]byte, 64<<10)...)
	raw := spawnConstructionPacket(spawnFrame(eid, block))[1:]
	compressed := make([]byte, lz4.CompressBlockBound(len(raw)))
	n, err := lz4.CompressBlock(raw, compressed, nil)
	if err != nil {
		t.Fatalf("CompressBlock: %v", err)
	}
	if n == 0 {
		t.Fatal("CompressBlock returned an uncompressed block")
	}
	payload := append([]byte{0x25}, compressed[:n]...)
	blob, err := decompressLZ4(payload[1:])
	if err != nil {
		t.Fatalf("decompressLZ4: %v", err)
	}
	if len(blob) != len(raw) {
		t.Fatalf("decompressed bytes=%d, want %d (compressed=%d)", len(blob), len(raw), n)
	}
	if _, err := p.Parse(&packet.Packet{CurrentTime: 4000, PacketPayload: payload}); err != nil {
		t.Fatalf("Parse compressed construction: %v", err)
	}
	if p.Tracks[eid] == nil {
		t.Fatal("projectile in dynamically resized LZ4 block was not decoded")
	}
}

func spawnTestMaps() ecsp.ComponentHashMaps {
	return ecsp.ComponentHashMaps{
		ComponentNames: map[uint32]string{
			uint32(testUIDType):       "Int32",
			uint32(testClassNameType): "String",
			uint32(testRocketType):    "Rocket",
			uint32(testBombType):      "Bomb",
			uint32(testVelocityType):  "Point3",
			uint32(testUnknownType):   "Unsupported",
		},
		DataComponents: map[uint32]ecsp.DataComponentDef{
			uint32(testUIDData):       {Name: "uid", ComponentHash: uint32(testUIDType)},
			uint32(testClassNameData): {Name: "unit__className", ComponentHash: uint32(testClassNameType)},
			uint32(testRocketData):    {Name: "rocket_component", ComponentHash: uint32(testRocketType)},
			uint32(testBombData):      {Name: "bomb_component", ComponentHash: uint32(testBombType)},
			uint32(testVelocityData):  {Name: "proj__velocity", ComponentHash: uint32(testVelocityType)},
			uint32(testUnknownData):   {Name: "unsupported_value", ComponentHash: uint32(testUnknownType)},
		},
		DataComponentParsers: map[ecsp.DataComponentHash]ecsp.ComponentParser{
			testUIDData:       ecsp.ComponentParserFunc(ecsp.GenericParser[int32]),
			testClassNameData: ecsp.ComponentParserFunc(ecsp.StringParser),
			testVelocityData:  ecsp.ComponentParserFunc(ecsp.GenericParser[ecsp.Point3]),
		},
	}
}

func spawnTemplateBlock(id uint16, name string, components []spawnWireComponent, reserved bool, values []spawnWireValue) []byte {
	block := spawnCompressed(nil, uint64(id))
	block = append(block, byte(len(name)))
	block = append(block, name...)
	block = binary.LittleEndian.AppendUint16(block, uint16(len(components)))
	for _, component := range components {
		block = spawnCompressed(block, uint64(component.id))
		block = spawnU32Raw(block, uint32(component.dataHash))
		block = spawnU32Raw(block, uint32(component.typeHash))
	}
	if reserved {
		block = append(block, 0)
	}
	block = append(block, byte(len(values)))
	previous := -1
	for _, value := range values {
		offset := value.local
		if previous >= 0 {
			offset = value.local - previous - 1
		}
		block = append(block, byte(offset))
		block = append(block, value.data...)
		previous = value.local
	}
	return block
}

func spawnRocketValue(ownerEID, targetEID uint64, weaponRef uint32, x, y, z float64) []byte {
	value := []byte{0x2a}
	value = append(value, spawnEID(ownerEID)...)
	value = append(value, spawnEID(targetEID)...)
	value = append(value, 0x7f)
	value = binary.LittleEndian.AppendUint32(value, 432)
	value = binary.LittleEndian.AppendUint32(value, weaponRef)
	for _, coordinate := range []float64{x, y, z} {
		value = binary.LittleEndian.AppendUint32(value, math.Float32bits(float32(coordinate)))
	}
	return value
}

func spawnConstructionPacket(frames ...[]byte) []byte {
	packetData := []byte{0x24, byte(len(frames) - 1)}
	for _, frame := range frames {
		packetData = append(packetData, frame...)
	}
	return packetData
}

func spawnFrame(eid uint64, block []byte) []byte {
	frame := spawnEID(eid)
	frame = spawnCompressed(frame, uint64(len(block)))
	return append(frame, block...)
}

func spawnDestructionPacket(eid uint64) []byte {
	return append([]byte{0x26, 0}, spawnEID(eid)...)
}

func spawnCompressed(dst []byte, value uint64) []byte {
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

func spawnEID(value uint64) []byte {
	index := value & 0x3fffff
	generation := (value >> 22) & 0xff
	if generation <= 1 {
		first := uint16(index<<2) | uint16(generation<<1) | 1
		return binary.LittleEndian.AppendUint16(nil, first)
	}
	if index <= 0x3fff {
		first := uint16(index<<2) | 2
		out := binary.LittleEndian.AppendUint16(nil, first)
		return append(out, byte(generation))
	}
	first := uint16(index&0x3fff) << 2
	second := uint16((index>>14)&0xff) | uint16(generation<<8)
	out := binary.LittleEndian.AppendUint16(nil, first)
	return binary.LittleEndian.AppendUint16(out, second)
}

func encodeSpawnEID(index, generation uint64) uint64 {
	return (index & 0x3fffff) | ((generation & 0xff) << 22)
}

func spawnU32(value uint32) []byte {
	return spawnU32Raw(nil, value)
}

func spawnU32Raw(dst []byte, value uint32) []byte {
	return binary.LittleEndian.AppendUint32(dst, value)
}
