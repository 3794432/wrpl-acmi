package packetfm

import (
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
)

func sensorTestBits(values ...int) []byte {
	bits := make([]bool, 0, 64)
	add := func(value, count int) {
		for bit := count - 1; bit >= 0; bit-- {
			bits = append(bits, value&(1<<bit) != 0)
		}
	}
	if len(values)%2 != 0 {
		panic("sensorTestBits expects value/count pairs")
	}
	for i := 0; i < len(values); i += 2 {
		add(values[i], values[i+1])
	}
	out := make([]byte, (len(bits)+7)/8)
	for i, bit := range bits {
		if bit {
			out[i/8] |= 1 << (7 - i%8)
		}
	}
	return out
}

func TestSensorPreservesExistingKindOneWireLayout(t *testing.T) {
	// The existing runtime layout still reads the kind-1 has-state bit after
	// the leading state and kind/slot byte. The newer documentation does not
	// justify changing that alignment for older replays.
	r := danet.NewBitReader(sensorTestBits(0, 1, 0x12, 8, 0, 1, 0, 7))
	sensor, err := readSensor(r)
	if err != nil {
		t.Fatal(err)
	}
	if sensor.FirstBool || sensor.SensorKind() != 1 || sensor.SensorSlot() != 2 || sensor.Data1 == nil || sensor.Data1.HasState() {
		t.Fatalf("off sensor = %+v", sensor)
	}
	if r.BitOffset != 10 {
		t.Fatalf("off record consumed %d bits, want preserved 10-bit layout", r.BitOffset)
	}
}

func TestSensorKindOneNamedFields(t *testing.T) {
	packed := uint16(5 | (42 << 4) | (9 << 10))
	transceiver, pattern, signal := RadarModeIndices(packed)
	if transceiver != 5 || pattern != 42 || signal != 9 {
		t.Fatalf("packed indices = %d/%d/%d", transceiver, pattern, signal)
	}
	data := &FMSensorType1Data{Unk0: true, Unk1: packed, Unk2: 12.5, Unk3: 999, Unk4: 123, Unk5: 456}
	if !data.HasState() || data.PackedIndices() != packed || data.ModeChangeTime() != 12.5 || data.AzimuthRaw() != 123 || data.ElevationRaw() != 456 {
		t.Fatalf("kind-1 accessors = %+v", data)
	}
	sensor := FMSensorData{FirstBool: true, SensorType: 0x2a}
	if sensor.SensorKind() != 2 || sensor.SensorSlot() != 10 {
		t.Fatalf("sensor kind/slot = %d/%d", sensor.SensorKind(), sensor.SensorSlot())
	}
}

func TestTargetListDistinguishesExplicitEmptyFromUnavailable(t *testing.T) {
	empty, err := readTargets(danet.NewBitReader([]byte{0}))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("explicit empty designation list = %v/%v; want non-nil empty", empty, err)
	}
	for _, wire := range [][]byte{nil, {0x10}, {0x90}} {
		partial, err := readTargets(danet.NewBitReader(wire))
		if err == nil || partial != nil {
			t.Fatalf("unavailable/truncated designation list %x = %v/%v; want nil/error", wire, partial, err)
		}
	}
}
