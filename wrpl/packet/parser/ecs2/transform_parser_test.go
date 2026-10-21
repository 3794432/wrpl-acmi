package packetecs2

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
)

func TestTransformParserPreservesOrthonormalTranslation(t *testing.T) {
	data := make([]byte, 20)
	setStreamBit(data, 0, true) // isOrthoUni; bit 63 stays clear, so there is no scale.
	writeFloat32Bits(data, 64, 100)
	writeFloat32Bits(data, 96, -8.5)
	writeFloat32Bits(data, 128, 34.25)

	got, err := parsedTransform(t, data)
	if err != nil {
		t.Fatalf("TransformParser: %v", err)
	}
	want := Point3{X: 100, Y: -8.5, Z: 34.25}
	if got.V4 != want {
		t.Fatalf("translation = %+v, want %+v", got.V4, want)
	}
	if got.V1 != (Point3{}) || got.V2 != (Point3{}) || got.V3 != (Point3{}) {
		t.Fatalf("rotation was synthesized: %+v", got)
	}
}

func TestTransformParserPreservesScaledTranslation(t *testing.T) {
	data := make([]byte, 24)
	setStreamBit(data, 0, true)
	setStreamBit(data, 63, true) // hasScale follows the 62-bit quaternion.
	writeFloat32Bits(data, 64, 2.5)
	writeFloat32Bits(data, 96, -40)
	writeFloat32Bits(data, 128, 7)
	writeFloat32Bits(data, 160, 180)

	got, err := parsedTransform(t, data)
	if err != nil {
		t.Fatalf("TransformParser: %v", err)
	}
	want := Point3{X: -40, Y: 7, Z: 180}
	if got.V4 != want {
		t.Fatalf("translation = %+v, want %+v", got.V4, want)
	}
}

func TestTransformParserPreservesNonOrthogonalTranslation(t *testing.T) {
	data := make([]byte, 49)
	// The first stream bit is clear, selecting the 9-float basis layout. Every
	// basis value is finite zero; the translation starts at bit 289.
	positionBit := 1 + 9*32
	writeFloat32Bits(data, positionBit, 12)
	writeFloat32Bits(data, positionBit+32, -3)
	writeFloat32Bits(data, positionBit+64, 0.125)

	got, err := parsedTransform(t, data)
	if err != nil {
		t.Fatalf("TransformParser: %v", err)
	}
	want := Point3{X: 12, Y: -3, Z: 0.125}
	if got.V4 != want {
		t.Fatalf("translation = %+v, want %+v", got.V4, want)
	}
}

func TestTransformParserRejectsTruncatedPosition(t *testing.T) {
	data := make([]byte, 19)
	setStreamBit(data, 0, true)
	writeFloat32Bits(data, 64, 1)
	writeFloat32Bits(data, 96, 2)

	_, err := parsedTransform(t, data)
	if err == nil || !strings.Contains(err.Error(), "position") {
		t.Fatalf("error = %v, want truncated position error", err)
	}
}

func TestTransformParserRejectsNonFiniteValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		build  func([]byte)
		length int
	}{
		{
			name: "position NaN",
			build: func(data []byte) {
				setStreamBit(data, 0, true)
				writeFloat32Bits(data, 64, float32(math.NaN()))
			},
			length: 20,
		},
		{
			name: "scale infinity",
			build: func(data []byte) {
				setStreamBit(data, 0, true)
				setStreamBit(data, 63, true)
				writeFloat32Bits(data, 64, float32(math.Inf(1)))
			},
			length: 24,
		},
		{
			name: "basis NaN",
			build: func(data []byte) {
				writeFloat32Bits(data, 1+8*32, float32(math.NaN()))
			},
			length: 49,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.length)
			tc.build(data)
			if _, err := parsedTransform(t, data); err == nil {
				t.Fatal("non-finite transform was accepted")
			}
		})
	}
}

func parsedTransform(t *testing.T, data []byte) (TMatrix, error) {
	t.Helper()
	got, err := TransformParser(danet.NewBitReader(data), NewPacketECSParser(ComponentHashMaps{}))
	if err != nil {
		return TMatrix{}, err
	}
	matrix, ok := got.(TMatrix)
	if !ok {
		t.Fatalf("TransformParser returned %T, want TMatrix", got)
	}
	return matrix, nil
}

func writeFloat32Bits(data []byte, bitOffset int, value float32) {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], math.Float32bits(value))
	for i, b := range raw {
		for bit := 7; bit >= 0; bit-- {
			setStreamBit(data, bitOffset+i*8+(7-bit), b&(1<<bit) != 0)
		}
	}
}

func setStreamBit(data []byte, position int, set bool) {
	mask := byte(1 << (7 - (position & 7)))
	if set {
		data[position>>3] |= mask
	} else {
		data[position>>3] &^= mask
	}
}
