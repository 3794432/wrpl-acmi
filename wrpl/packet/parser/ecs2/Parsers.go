package packetecs2

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
)

func EidParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	var n uint64
	n, err = packet.ReadEID(r)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func GenericParser[T any](r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	var n T
	if binary.Size(n) < 0 {
		return nil, fmt.Errorf("ECS generic parser type %T has unknown binary size at bit offset %d (data length %d bytes)", n, r.BitOffset, len(r.Data))
	}
	err = binary.Read(r, binary.LittleEndian, &n)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func GenericListParser[T any](r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	count, err := readECSCompressed(r)
	if err != nil {
		return nil, err
	}
	var sample T
	size := binary.Size(sample)
	if size < 0 {
		return nil, fmt.Errorf("ECS generic list type %T has unknown binary size at bit offset %d (data length %d bytes)", sample, r.BitOffset, len(r.Data))
	}
	if uint64(size) > ^uint64(0)/8 {
		return nil, fmt.Errorf("ECS generic list type %T element size %d is too large at bit offset %d (data length %d bytes)", sample, size, r.BitOffset, len(r.Data))
	}
	countInt, err := ecsCheckListCount(r, "generic list", count, uint64(size)*8)
	if err != nil {
		return nil, err
	}
	n := make([]T, countInt)
	for i := 0; i < countInt; i++ {
		peid, err := GenericParser[T](r, ctx)
		if err != nil {
			return nil, err
		}
		n[i] = peid.(T)
	}
	return n, nil
}

func StorageParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	var n UnitStorage
	blockSize, err := readECSCompressed(r)
	if err != nil {
		return nil, err
	}
	blockBits, err := ecsCheckListCount(r, "storage bit block", blockSize, 1)
	if err != nil {
		return nil, err
	}
	n.Raw, err = r.ReadBits(blockBits)
	if err != nil {
		return nil, err
	}
	return n, nil // TODO, implement IdFieldSerializer255
}

func read_string(r *danet.BitReader, ctx *PacketECSParser) (ret string, err error) {
	var n []byte
	for {
		temp, readErr := r.ReadByte()
		if readErr != nil {
			return "", readErr
		}
		if temp == 0 {
			return string(n), nil
		}
		if len(n) >= ecsMaxAllocationCount {
			return "", fmt.Errorf("ECS string length exceeds hard allocation limit %d at bit offset %d (data length %d bytes)", ecsMaxAllocationCount, r.BitOffset, len(r.Data))
		}
		n = append(n, temp)
	}
}

func StringParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	n, err := read_string(r, ctx)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func ReadBool(r *danet.BitReader, ctx *PacketECSParser) (ret bool, err error) { // TODO, move this inside bitReader, not having a read bool is CRIMINAL I say
	out, err := r.ReadBits(1)
	if err != nil {
		return false, err
	}
	return out[0] == 1, nil
}

func BoolParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	out, err := ReadBool(r, ctx)
	return out, nil
}

func BoolListParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	count, err := readECSCompressed(r)
	if err != nil {
		return nil, err
	}
	countInt, err := ecsCheckListCount(r, "bool list", count, 1)
	if err != nil {
		return nil, err
	}
	n := make([]bool, countInt)
	for i := 0; i < countInt; i++ {
		peid, err := ReadBool(r, ctx)
		if err != nil {
			return nil, err
		}
		n[i] = peid
	}
	return n, nil
}

func readPartId(r *danet.BitReader) (PartId, error) {
	rb := [1]byte{}
	_, err := r.ReadBitsInto(6, rb[:])
	if err != nil {
		return 0, err
	}
	return PartId(rb[0]), nil
}

func DmPartIdParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	return readPartId(r)
}

func DmPartIdListParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	count, err := readECSCompressed(r)
	if err != nil {
		return nil, err
	}
	countInt, err := ecsCheckPartIDListCount(r, count)
	if err != nil {
		return nil, err
	}
	n := make([]PartId, countInt)
	for i := 0; i < countInt; i++ {
		n[i], err = readPartId(r)
		if err != nil {
			return nil, err
		}
	}

	return n, nil
}

func StringListParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	count, err := readECSCompressed(r)
	if err != nil {
		return nil, err
	}
	countInt, err := ecsCheckListCount(r, "string list", count, 8)
	if err != nil {
		return nil, err
	}
	n := make([]string, countInt)
	for i := 0; i < countInt; i++ {
		pstr, err := StringParser(r, ctx)
		if err != nil {
			return nil, err
		}
		tstr := pstr.(string)
		n[i] = tstr
	}
	return n, nil
}

func EidListParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	count, err := readECSCompressed(r)
	if err != nil {
		return nil, err
	}
	countInt, err := ecsCheckListCount(r, "EID list", count, 16)
	if err != nil {
		return nil, err
	}
	n := make([]uint64, countInt)
	for i := 0; i < countInt; i++ {
		peid, err := EidParser(r, ctx)
		if err != nil {
			return nil, err
		}
		n[i] = peid.(uint64)
	}
	return n, nil
}

func ArrayParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	count, err := readECSCompressed(r)
	if err != nil {
		return nil, err
	}
	countInt, err := ecsCheckListCount(r, "array component", count, 32)
	if err != nil {
		return nil, err
	}
	n := make([]Component, countInt)
	for i := 0; i < countInt; i++ {
		pcomp, err := deserialize_child_component(r, ctx)
		if err != nil {
			return nil, err
		}
		if pcomp == nil {
			n[i] = Component{}
			continue
		}
		n[i] = *pcomp
	}
	return n, nil
}

const OBJECT_KEY_BITS = 10

func read_istring(r *danet.BitReader, ctx *PacketECSParser) (ret string, err error) {
	rawString, err := ReadBool(r, ctx)
	if err != nil {
		return "", err
	}
	if rawString {
		return read_string(r, ctx)
	}
	str_index_bytes := [2]byte{}
	_, err = r.ReadBitsInto(OBJECT_KEY_BITS, str_index_bytes[:])
	if err != nil {
		return "", err
	}
	strIndex := binary.LittleEndian.Uint16(str_index_bytes[:])
	str, exists := ctx.InternedStrings[strIndex]
	if !exists {
		str, err := read_string(r, ctx)
		if err != nil {
			return "", err
		}
		ctx.InternedStrings[strIndex] = str
		return str, nil
	}
	return str, nil
}

func IStringParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	str, err := read_istring(r, ctx)
	if err != nil {
		return nil, err
	}
	return str, nil
}

func ObjectParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	var n Object
	count, err := readECSCompressed(r)
	if err != nil {
		return nil, err
	}
	countInt, err := ecsCheckListCount(r, "object component", count, 41)
	if err != nil {
		return nil, err
	}
	for i := 0; i < countInt; i++ {
		name, err := read_istring(r, ctx)
		if err != nil {
			return nil, err
		}
		comp, err := deserialize_child_component(r, ctx)
		if err != nil {
			return nil, err
		}
		n.AddComponent(comp, name)
	}
	return n, nil
}

func TransformParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	if r == nil {
		return nil, fmt.Errorf("transform parser received a nil bit reader")
	}
	isOrthoUni, err := ReadBool(r, ctx)
	if err != nil {
		return nil, err
	}
	if isOrthoUni {
		// The quaternion remains opaque until its quantized rotation is decoded.
		// Read it rather than advancing the offset so truncated input is rejected.
		var quantizedQuat [8]byte
		if _, err := r.ReadBitsInto(62, quantizedQuat[:]); err != nil {
			return nil, fmt.Errorf("reading transform quaternion at bit offset %d: %w", r.BitOffset, err)
		}
		hasScale, err := ReadBool(r, ctx)
		if err != nil {
			return nil, err
		}
		if hasScale {
			var rawScale [4]byte
			if _, err := r.ReadBytesInto(len(rawScale), rawScale[:]); err != nil {
				return nil, fmt.Errorf("reading transform scale at bit offset %d: %w", r.BitOffset, err)
			}
			scale := math.Float32frombits(binary.LittleEndian.Uint32(rawScale[:]))
			if math.IsNaN(float64(scale)) || math.IsInf(float64(scale), 0) {
				return nil, fmt.Errorf("transform scale is not finite at bit offset %d", r.BitOffset-32)
			}
		}
	} else {
		var basis [9 * 4]byte
		if _, err := r.ReadBytesInto(len(basis), basis[:]); err != nil {
			return nil, fmt.Errorf("reading transform basis at bit offset %d: %w", r.BitOffset, err)
		}
		for i := 0; i < 9; i++ {
			value := math.Float32frombits(binary.LittleEndian.Uint32(basis[i*4 : i*4+4]))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, fmt.Errorf("transform basis element %d is not finite at bit offset %d", i, r.BitOffset-len(basis)*8+i*32)
			}
		}
	}
	var rawPosition [3 * 4]byte
	if _, err := r.ReadBytesInto(len(rawPosition), rawPosition[:]); err != nil {
		return nil, fmt.Errorf("reading transform position at bit offset %d: %w", r.BitOffset, err)
	}
	var n TMatrix
	n.V4 = Point3{
		X: math.Float32frombits(binary.LittleEndian.Uint32(rawPosition[0:4])),
		Y: math.Float32frombits(binary.LittleEndian.Uint32(rawPosition[4:8])),
		Z: math.Float32frombits(binary.LittleEndian.Uint32(rawPosition[8:12])),
	}
	if math.IsNaN(float64(n.V4.X)) || math.IsInf(float64(n.V4.X), 0) ||
		math.IsNaN(float64(n.V4.Y)) || math.IsInf(float64(n.V4.Y), 0) ||
		math.IsNaN(float64(n.V4.Z)) || math.IsInf(float64(n.V4.Z), 0) {
		return nil, fmt.Errorf("transform position is not finite at bit offset %d", r.BitOffset-len(rawPosition)*8)
	}
	// V1..V3 remain zero because the rotation is deliberately undecoded.
	// V4 is the serializer's unquantized float32 translation.
	return n, nil
}

func RendInstDescParser(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	var n RendInstDesc
	temp, err := r.ReadCompressed()
	if err != nil {
		return nil, err
	}
	n.V1 = uint32(temp)
	temp, err = r.ReadCompressed()
	if err != nil {
		return nil, err
	}
	n.V2 = uint32(temp)
	if n.V2 != 0 {
		temp, err = r.ReadCompressed()
		if err != nil {
			return nil, err
		}
		n.V3 = uint32(temp)
	}
	return n, nil
}

const ri_type_bits = 12
const ri_type_full_bits = 16
const ri_inst_base_bits = 11
const ri_inst_other_bits = 12
const ri_inst_total_bits = ri_inst_base_bits + ri_inst_other_bits
const short_bits = 1 + ri_inst_base_bits + ri_type_bits
const long_bits = 1 + ri_inst_total_bits + ri_type_full_bits

const ri_instance_type_shift = 32

func make_handle(ri_type uint32, ri_inst uint32) riex_handle_t {
	return riex_handle_t(uint64(ri_type)<<ri_instance_type_shift | uint64(ri_inst))
}

func RendInstSerializer(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	var handle riex_handle_t
	var riType uint32
	var riInst uint32
	word24, err := r.ReadU32LE()
	if err != nil {
		return nil, err
	}
	if word24&(1<<ri_inst_total_bits) > 0 {
		riInst = word24 & ((1 << ri_inst_total_bits) - 1)
		riType, err = r.ReadU32LE()
		if err != nil {
			return nil, err
		}
	} else {
		riType = word24 >> ri_inst_base_bits
		riInst = word24 & ((1 << ri_inst_base_bits) - 1)
	}
	if riInst == 0 && riType == 0 {
		handle = 18446744073709551615 // -1 maybe?????? I dunno microsoft gave this to me
	} else {
		handle = make_handle(riType, riInst) - 1
	}
	return handle, nil
}

func RocketSerializer(r *danet.BitReader, ctx *PacketECSParser) (ret any, err error) {
	_, err = r.ReadCompressed()
	if err != nil {
		return nil, err
	}
	_, err = packet.ReadEID(r)
	if err != nil {
		return nil, err
	}
	_, err = packet.ReadEID(r)
	if err != nil {
		return nil, err
	}
	_, err = r.ReadByte()
	if err != nil {
		return nil, err
	}
	r.IgnoreBits(792)
	// _, err = r.ReadBits(792)
	// if err != nil {
	// 	return nil, err
	// }

	temp1, err := r.ReadBytes(2)
	if err != nil {
		return nil, err
	}
	n1 := (*uint16)(unsafe.Pointer(&temp1[0]))
	x1 := (int(*n1) + 7) & 0xfffffff8
	_, err = r.ReadBits(x1)
	if err != nil {
		return nil, err
	}

	temp2, err := r.ReadBytes(2)
	if err != nil {
		return nil, err
	}
	n2 := (*uint16)(unsafe.Pointer(&temp2[0]))
	x2 := (int(*n2) + 7) & 0xfffffff8
	_, err = r.ReadBits(x2)
	if err != nil {
		return nil, err
	}

	temp3, err := r.ReadBytes(2)
	if err != nil {
		return nil, err
	}
	n3 := (*uint16)(unsafe.Pointer(&temp3[0]))
	x3 := (int(*n3) + 7) & 0xfffffff8
	_, err = r.ReadBits(x3)
	if err != nil {
		return nil, err
	}

	r.IgnoreBits(112)
	// _, err = r.ReadBits(112)
	// if err != nil {
	// 	return nil, err
	// }

	var rocket Rocket
	return rocket, nil
}

// ai gave me these :)
type ComponentParserFunc func(r *danet.BitReader, ctx *PacketECSParser) (any, error)

func (f ComponentParserFunc) Parse(r *danet.BitReader, ctx *PacketECSParser) (any, error) {
	return f(r, ctx)
}

var (
	default_comp_map = map[string]ComponentParser{
		"FlightModelWrapStorageComponent":    ComponentParserFunc(StorageParser),
		"HeavyVehicleModelStorageComponent":  ComponentParserFunc(StorageParser),
		"WarShipModelStorageComponent":       ComponentParserFunc(StorageParser),
		"InfantryTroopStorageComponent":      ComponentParserFunc(StorageParser),
		"HumanStorageComponent":              ComponentParserFunc(StorageParser),
		"WalkerVehicleStorageComponent":      ComponentParserFunc(StorageParser),
		"FortificationModelStorageComponent": ComponentParserFunc(StorageParser),
		"LightVehicleModelStorageComponent":  ComponentParserFunc(StorageParser),
		"BarrageBalloonStorageComponent":     ComponentParserFunc(StorageParser),
		"bool":                               ComponentParserFunc(BoolParser),
		"float":                              ComponentParserFunc(GenericParser[float32]),
		"ecs::EntityId":                      ComponentParserFunc(EidParser),
		"TMatrix":                            ComponentParserFunc(GenericParser[TMatrix]),
		"E3DCOLOR":                           ComponentParserFunc(GenericParser[E3DColor]),
		"int":                                ComponentParserFunc(GenericParser[int32]),
		"uint32_t":                           ComponentParserFunc(GenericParser[uint32]),
		"Point2":                             ComponentParserFunc(GenericParser[Point2]),
		"Point3":                             ComponentParserFunc(GenericParser[Point3]),
		"Point4":                             ComponentParserFunc(GenericParser[Point4]),
		"IPoint2":                            ComponentParserFunc(GenericParser[IPoint2]),
		"IPoint3":                            ComponentParserFunc(GenericParser[IPoint3]),
		"IPoint4":                            ComponentParserFunc(GenericParser[IPoint4]),
		"ecs::string":                        ComponentParserFunc(StringParser),
		"ecs::Object":                        ComponentParserFunc(ObjectParser),
		"ecs::Array":                         ComponentParserFunc(ArrayParser),
		"ecs::UInt8List":                     ComponentParserFunc(GenericListParser[uint8]),
		"ecs::UInt16List":                    ComponentParserFunc(GenericListParser[uint16]),
		"ecs::UInt32List":                    ComponentParserFunc(GenericListParser[uint32]),
		"ecs::UInt64List":                    ComponentParserFunc(GenericListParser[uint64]),
		"ecs::StringList":                    ComponentParserFunc(StringListParser),
		"ecs::EidList":                       ComponentParserFunc(EidListParser),
		"ecs::FloatList":                     ComponentParserFunc(GenericListParser[float32]),
		"ecs::Point2List":                    ComponentParserFunc(GenericListParser[Point2]),
		"ecs::Point3List":                    ComponentParserFunc(GenericListParser[Point3]),
		"ecs::Point4List":                    ComponentParserFunc(GenericListParser[Point4]),
		"ecs::IPoint2List":                   ComponentParserFunc(GenericListParser[IPoint2]),
		"ecs::IPoint3List":                   ComponentParserFunc(GenericListParser[IPoint3]),
		"ecs::IPoint4List":                   ComponentParserFunc(GenericListParser[IPoint4]),
		"ecs::BoolList":                      ComponentParserFunc(BoolListParser),
		"ecs::TMatrixList":                   ComponentParserFunc(GenericListParser[TMatrix]),
		"ecs::ColorList":                     ComponentParserFunc(GenericListParser[E3DColor]),
		"ecs::Int8List":                      ComponentParserFunc(GenericListParser[int8]),
		"ecs::Int16List":                     ComponentParserFunc(GenericListParser[int16]),
		"ecs::IntList":                       ComponentParserFunc(GenericListParser[int32]),
		"ecs::Int64List":                     ComponentParserFunc(GenericListParser[int64]),
		"dm::PartIdList":                     ComponentParserFunc(DmPartIdListParser),
		"uint8_t":                            ComponentParserFunc(GenericParser[uint8]),
		"uint16_t":                           ComponentParserFunc(GenericParser[uint16]),
		"dm::PartId":                         ComponentParserFunc(DmPartIdParser),
		"Payload":                            ComponentParserFunc(RocketSerializer),
		"Bomb":                               ComponentParserFunc(RocketSerializer),
		"Rocket":                             ComponentParserFunc(RocketSerializer),
		"Jettisoned":                         ComponentParserFunc(RocketSerializer),
		"Torpedo":                            ComponentParserFunc(RocketSerializer),
		"BufferedHudData":                    ComponentParserFunc(GenericListParser[uint8]),
	}

	default_datacomp_map = map[string]ComponentParser{
		"ri_extra__riSyncDesc": ComponentParserFunc(RendInstDescParser),
		"transform":            ComponentParserFunc(TransformParser),
		"ri_extra__handle":     ComponentParserFunc(RendInstSerializer),
	}
)

func (chm *ComponentHashMaps) InitializeParsers() error {
	chm.ComponentParsers = make(map[ComponentHash]ComponentParser)
	chm.DataComponentParsers = make(map[DataComponentHash]ComponentParser)
	for key, value := range chm.ComponentNames {
		f, exists := default_comp_map[value]
		if exists { // exists
			chm.ComponentParsers[ComponentHash(key)] = f
		}
	}
	for key, value := range chm.DataComponents {
		if value.CustomLoader {
			f, exists := default_datacomp_map[value.Name]
			if exists {
				chm.DataComponentParsers[DataComponentHash(key)] = f
			}
		} else { // if the datacomp doesnt define a custom one, steal it from the component
			f, exists := chm.ComponentParsers[ComponentHash(value.ComponentHash)]
			if exists {
				chm.DataComponentParsers[DataComponentHash(key)] = f
			}
		}
	}
	return nil
}
