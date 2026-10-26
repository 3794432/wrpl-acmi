package main

import (
	"math"
	"strconv"
	"strings"
	"testing"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
)

func radarModeTestCatalog() *radarModeCatalog {
	beamHorizontal, beamVertical := 20.0, 8.0
	return &radarModeCatalog{
		Models: map[string][]string{
			"test_radar_aircraft": {"rwr", "radar", "irst", "second_radar"},
		},
		Sensors: map[string]radarModeSensor{
			"rwr": {
				Name: "Test RWR", Type: "rwr",
			},
			"radar": {
				Name: "AN/Test Radar", Type: "radar",
				Transceivers: []radarModeTransceiver{
					{ID: "hprf", Active: true},
					{ID: "irst", VisibilityType: "infraRed", Active: true},
				},
				ScanPatterns: []radarModeScanPattern{
					{ID: "searchWide", Types: []string{"pyramide"}},
					{ID: "twsNarrow", Types: []string{"pyramide"}},
					{ID: "track", Types: []string{"no"}},
					{ID: "beamExplicit", Types: []string{"cone"}, HorizontalBeamwidthDeg: &beamHorizontal, VerticalBeamwidthDeg: &beamVertical},
				},
				Signals: []string{"search", "track"},
			},
			"irst": {
				Name: "Test IRST", Type: "radar",
				Transceivers: []radarModeTransceiver{{ID: "irst", VisibilityType: "infraRed", Active: true}},
				ScanPatterns: []radarModeScanPattern{{ID: "irstSearch", Types: []string{"pyramide"}}},
			},
			"second_radar": {
				Name: "AN/Second Radar", Type: "radar",
				Transceivers: []radarModeTransceiver{{ID: "mprf", Active: true}},
				ScanPatterns: []radarModeScanPattern{{ID: "secondaryScan", Types: []string{"cylinder"}}},
			},
			"esm": {
				Name: "Test ESM", Type: "radar",
				Transceivers: []radarModeTransceiver{{ID: "common", VisibilityType: "radarIntercept", Active: false}},
			},
		},
	}
}

func radarModeTestTrack(uid uint64, model string, times ...uint32) *track {
	tr := &track{key: trackKey{air: true, id: uid}}
	for _, t := range times {
		tr.samples = append(tr.samples, acmiSample{t: t})
	}
	if model != "" {
		tr.entity = &packetecs2.Entity{Template: "test"}
		tr.entity.Data.AddComponent(&packetecs2.Component{Value: model}, "unit__className")
	}
	return tr
}

func radarModeTestFrame(t uint32, uid uint64, sensors ...packetfm.FMSensorData) packetfm.FMUpdatePacket {
	return packetfm.FMUpdatePacket{
		CurrentTime: t,
		Entries: []*packetfm.FMEntry{{
			HasUID: true,
			UID:    uid,
			Data:   &packetfm.FMData{SensorsData: sensors},
		}},
	}
}

func radarModeTestPacked(transceiver, pattern, signal int) uint16 {
	return uint16(transceiver&0xf) | uint16(pattern&0x3f)<<4 | uint16(signal&0xf)<<10
}

func radarModeTestOn(slot, transceiver, pattern, signal int, azimuth, elevation uint16, changeTime float32) packetfm.FMSensorData {
	return packetfm.FMSensorData{
		FirstBool:  true,
		SensorType: byte(0x10 | slot),
		Data1: &packetfm.FMSensorType1Data{
			Unk0: true,
			Unk1: radarModeTestPacked(transceiver, pattern, signal),
			Unk2: changeTime,
			Unk3: 12345, // preceding unknown scalar must not become an angle.
			Unk4: azimuth,
			Unk5: elevation,
		},
	}
}

func radarModeTestOff(slot int) packetfm.FMSensorData {
	return packetfm.FMSensorData{
		FirstBool:  false,
		SensorType: byte(0x10 | slot),
		Data1:      &packetfm.FMSensorType1Data{},
	}
}

func radarModeTestNoState(slot int) packetfm.FMSensorData {
	return packetfm.FMSensorData{
		FirstBool:  true,
		SensorType: byte(0x10 | slot),
		Data1:      &packetfm.FMSensorType1Data{},
	}
}

func radarModeTestKind(slot, kind int) packetfm.FMSensorData {
	return packetfm.FMSensorData{FirstBool: true, SensorType: byte(kind<<4 | slot)}
}

func radarModeTestPrepare(t *testing.T, frames []packetfm.FMUpdatePacket, tracks ...*track) *radarModeExport {
	t.Helper()
	x := newRadarModeExport(
		&packetfm.PacketFlightModelParser{Results: frames},
		radarModeExportOptions{Table: radarModeTestCatalog()},
	)
	if x == nil {
		t.Fatal("newRadarModeExport returned nil")
	}
	ids := acmiObjectIDs(tracks)
	x.prepare(tracks, ids)
	return x
}

func radarModeTestProperties(x *radarModeExport, uid uint64, t uint32, tr *track) map[string]string {
	props := map[string]string{}
	for _, item := range x.lineProperties(uid, t, tr) {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			props[parts[0]] = parts[1]
		}
	}
	return props
}

func radarModeTestRequireFloat(t *testing.T, props map[string]string, name string, want float64) {
	t.Helper()
	raw, ok := props[name]
	if !ok {
		t.Fatalf("%s missing from %v", name, props)
	}
	got, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.Abs(got-want) > 0.006 {
		t.Fatalf("%s = %q, want %.3f (%v)", name, raw, want, props)
	}
}

func TestRadarModeExportUsesDatamineSlotAndExactModeText(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000)
	// The RWR is slot 0 and emits no record. The radar is slot 1.
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid,
			radarModeTestOn(1, 0, 1, 1, uint16(int16(8192)), uint16(int16(4096)), 987.25)),
	}, tr)
	props := radarModeTestProperties(x, uid, 1000, tr)
	want := map[string]string{
		"RadarMode":              "1",
		"WT_RadarMode":           "scan",
		"WT_RadarPattern":        "twsNarrow",
		"WT_RadarTransceiver":    "hprf",
		"WT_RadarSignal":         "track",
		"WT_RadarSensor":         "AN/Test Radar",
		"WT_RadarSlot":           "1",
		"WT_RadarStateSource":    "fm-kind1",
		"WT_RadarModeChangeTime": "987.250",
	}
	for name, value := range want {
		if props[name] != value {
			t.Errorf("%s = %q, want %q; all=%v", name, props[name], value, props)
		}
	}
	radarModeTestRequireFloat(t, props, "RadarAzimuth", radarModeAzimuthDegrees(uint16(int16(8192))))
	radarModeTestRequireFloat(t, props, "RadarElevation", radarModeElevationDegrees(uint16(int16(4096))))
	if props["Label"] != "TWS NAR" {
		t.Fatalf("native label did not apply the display mapping: %v", props)
	}
	for _, forbidden := range []string{"RadarRange", "RadarHorizontalBeamwidth", "RadarVerticalBeamwidth"} {
		if _, ok := props[forbidden]; ok {
			t.Fatalf("unknown %s was invented: %v", forbidden, props)
		}
	}
	if !strings.Contains(strings.Join(x.lineProperties(uid, 1000, tr), ","), "RadarMode=1") {
		t.Fatal("exact sample did not return the radar-mode transition")
	}
	if got := x.lineProperties(uid, 999, tr); len(got) != 0 {
		t.Fatalf("state leaked to an earlier time: %v", got)
	}
}

func TestRadarModePatternNoIsTrackAndOtherPatternsAreScan(t *testing.T) {
	const uid = 5
	for _, test := range []struct {
		pattern int
		name    string
		mode    string
	}{
		{1, "twsNarrow", "scan"},
		{2, "track", "track"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000)
			x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
				radarModeTestFrame(1000, uid, radarModeTestOn(1, 0, test.pattern, 0, 0, 0, 1)),
			}, tr)
			props := radarModeTestProperties(x, uid, 1000, tr)
			if props["RadarMode"] != "1" || props["WT_RadarMode"] != test.mode || props["WT_RadarPattern"] != test.name {
				t.Fatalf("mode/pattern = %v", props)
			}
		})
	}
}

func TestRadarModeBodyAngleDirectionIsTacviewRightPositive(t *testing.T) {
	left := uint16(0xC000)        // approximately -90 degrees in the observed radar layout.
	right := uint16(int16(16384)) // approximately +90 degrees as a signed int16.
	if got := radarModeAzimuthDegrees(left); math.Abs(got+90) > 0.01 {
		t.Fatalf("left scan azimuth = %.4f, want -90 Tacview degrees", got)
	}
	if got := radarModeAzimuthDegrees(right); math.Abs(got-90) > 0.01 {
		t.Fatalf("right scan azimuth = %.4f, want +90 Tacview degrees", got)
	}
	if got := radarModeElevationDegrees(right); math.Abs(got-90) > 0.01 {
		t.Fatalf("up elevation = %.4f, want +90 Tacview degrees", got)
	}
}

func TestCapturedRadarAnglesMatchTrackedTargetGeometry(t *testing.T) {
	// F-2A ADTW, replay #2026.09.17 13.50.59, raw FM t=206181 ms.
	// The independently computed target angles were +20.75/-8.07 degrees.
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000)
	sensor := radarModeTestOn(1, 0, 0, 0, 3792, 64067, 205.93582)
	sensor.Data1.Unk3 = 0
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{radarModeTestFrame(1000, uid, sensor)}, tr)
	props := radarModeTestProperties(x, uid, 1000, tr)
	if got, _ := strconv.ParseFloat(props["RadarAzimuth"], 64); math.Abs(got-20.75) > 0.2 {
		t.Fatalf("recorded azimuth %v is inconsistent with target geometry", got)
	}
	if got, _ := strconv.ParseFloat(props["RadarElevation"], 64); math.Abs(got+8.07) > 0.2 {
		t.Fatalf("recorded elevation %v is inconsistent with target geometry", got)
	}
}

func TestRadarModeAnglesAreAlreadyBodyRelative(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000)
	tr.samples[0].x, tr.samples[0].y, tr.samples[0].z = 100, 500, -250
	tr.samples[0].yaw, tr.samples[0].pitch, tr.samples[0].roll = 135, 20, -30
	rawAzimuth := uint16(int16(8192))
	rawElevation := uint16(int16(4096))
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, radarModeTestOn(1, 0, 0, 0, rawAzimuth, rawElevation, 1)),
	}, tr)
	props := radarModeTestProperties(x, uid, 1000, tr)
	radarModeTestRequireFloat(t, props, "RadarAzimuth", radarModeAzimuthDegrees(rawAzimuth))
	radarModeTestRequireFloat(t, props, "RadarElevation", radarModeElevationDegrees(rawElevation))
}

func TestRadarModeOffClearsNativeAndTextState(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 2000)
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, radarModeTestOn(1, 0, 1, 1, 1000, 500, 1)),
		radarModeTestFrame(2000, uid, radarModeTestOff(1)),
	}, tr)
	props := radarModeTestProperties(x, uid, 2000, tr)
	want := map[string]string{
		"RadarMode":           "0",
		"WT_RadarMode":        "off",
		"WT_RadarStateSource": "fm-kind1-off",
		"WT_RadarPattern":     "",
		"WT_RadarTransceiver": "",
		"RadarAzimuth":        "0.00",
		"RadarElevation":      "0.00",
	}
	for name, value := range want {
		if props[name] != value {
			t.Fatalf("%s = %q, want %q; all=%v", name, props[name], value, props)
		}
	}
}

func TestRadarModeUnknownAndIRTransitionsDoNotClaimOffOrSTT(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 2000, 3000, 4000, 5000)
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, radarModeTestOn(1, 0, 0, 0, 0, 0, 1)),
		radarModeTestFrame(2000, uid, radarModeTestNoState(1)),
		radarModeTestFrame(3000, uid, radarModeTestOn(1, 1, 0, 0, 0, 0, 2)), // IR transceiver in a mixed radar sensor
		radarModeTestFrame(4000, uid, radarModeTestKind(1, 2)),
		radarModeTestFrame(5000, uid),
	}, tr)
	for _, test := range []struct {
		t      uint32
		source string
	}{
		{2000, "fm-kind1-no-state"},
		{3000, "fm-kind1-non-radar-transceiver"},
		{4000, "fm-sensor-kind-2"},
		{5000, "fm-no-sensor-records"},
	} {
		props := radarModeTestProperties(x, uid, test.t, tr)
		if props["RadarMode"] != "0" || props["WT_RadarMode"] != "unknown" || props["WT_RadarStateSource"] != test.source {
			t.Errorf("t=%d state = %v, want explicit unknown/%s", test.t, props, test.source)
		}
		if props["WT_RadarPattern"] != "" {
			t.Errorf("t=%d retained stale pattern: %v", test.t, props)
		}
		for key := range props {
			if key == "WT_RadarMode" || key == "RadarMode" {
				continue
			}
			if strings.Contains(props[key], "STT") {
				t.Errorf("invented STT mode at t=%d: %v", test.t, props)
			}
		}
	}
}

func TestRadarModeUnknownInitialSensorDoesNotGuessOff(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000)
	// Slot 0 is RWR and slot 9 does not exist. Neither is evidence that a radar
	// sensor was switched off.
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, radarModeTestOff(0), radarModeTestOff(9)),
	}, tr)
	if got := x.lineProperties(uid, 1000, tr); len(got) != 0 {
		t.Fatalf("unknown/RWR off produced a radar claim: %v", got)
	}
}

func TestRadarModeSlotSwitchClearsOldStateAndUsesNewSensor(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 2000)
	// IRST is slot 2. It is not the transmitter currently selected by the new
	// radar slot, so its presence must not make the active radar disappear.
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, radarModeTestOn(1, 0, 1, 1, 100, 10, 1), radarModeTestKind(2, 2)),
		radarModeTestFrame(2000, uid, radarModeTestOn(3, 0, 0, 0, 20, 30, 2), radarModeTestKind(2, 2)),
	}, tr)
	first := radarModeTestProperties(x, uid, 1000, tr)
	if first["WT_RadarSlot"] != "1" || first["WT_RadarPattern"] != "twsNarrow" {
		t.Fatalf("initial radar slot/interchange = %v", first)
	}
	second := radarModeTestProperties(x, uid, 2000, tr)
	if second["WT_RadarSlot"] != "3" || second["WT_RadarSensor"] != "AN/Second Radar" || second["WT_RadarPattern"] != "secondaryScan" {
		t.Fatalf("new radar slot was not selected: %v", second)
	}
	if second["RadarMode"] != "1" || second["WT_RadarMode"] != "scan" {
		t.Fatalf("new radar slot has no native mode: %v", second)
	}
}

func TestRadarModeInvalidIndicesAreRejectedWithoutGuessing(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 2000, 3000)
	invalidTransceiver := radarModeTestOn(1, 15, 0, 0, 1000, 1000, 1)
	invalidPattern := radarModeTestOn(1, 0, 63, 0, 1000, 1000, 2)
	invalidSignal := radarModeTestOn(1, 0, 0, 15, 1000, 1000, 3)
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, invalidTransceiver),
		radarModeTestFrame(2000, uid, invalidPattern),
		radarModeTestFrame(3000, uid, invalidSignal),
	}, tr)
	for _, test := range []struct {
		t      uint32
		source string
	}{
		{1000, "fm-kind1-invalid-transceiver"},
		{2000, "fm-kind1-invalid-pattern"},
	} {
		props := radarModeTestProperties(x, uid, test.t, tr)
		if props["RadarMode"] != "0" || props["WT_RadarMode"] != "unknown" || props["WT_RadarStateSource"] != test.source {
			t.Errorf("t=%d = %v", test.t, props)
		}
	}
	valid := radarModeTestProperties(x, uid, 3000, tr)
	if valid["RadarMode"] != "1" || valid["WT_RadarPattern"] != "searchWide" || valid["WT_RadarSignal"] != "" {
		t.Fatalf("invalid signal index prevented a valid radar state: %v", valid)
	}
}

func TestRadarModeExplicitBeamwidthOnlyWhenConfigured(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 2000)
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, radarModeTestOn(1, 0, 3, 0, 0, 0, 1)),
		radarModeTestFrame(2000, uid, radarModeTestOff(1)),
	}, tr)
	props := radarModeTestProperties(x, uid, 1000, tr)
	if props["RadarHorizontalBeamwidth"] != "20.00" || props["RadarVerticalBeamwidth"] != "8.00" {
		t.Fatalf("explicit beamwidth was not exported: %v", props)
	}
	off := radarModeTestProperties(x, uid, 2000, tr)
	if off["RadarHorizontalBeamwidth"] != "0" || off["RadarVerticalBeamwidth"] != "0" {
		t.Fatalf("known beamwidth survived off: %v", off)
	}
}

func TestRadarModeExtraLineUsesOnlyAtOrBeforeTrackSamples(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 3000)
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(2000, uid, radarModeTestOn(1, 0, 1, 0, 0, 0, 5)),
	}, tr)
	if got := x.lineProperties(uid, 1000, tr); len(got) != 0 {
		t.Fatalf("future-only radar state was borrowed at t=1000: %v", got)
	}
	if got := x.lineProperties(uid, 2000, tr); len(got) != 0 {
		t.Fatalf("state without a track sample created a regular line property: %v", got)
	}
	extra := x.extraLines()
	if len(extra) != 1 || extra[0].t != 2000 || !strings.Contains(extra[0].line, "WT_RadarPattern=twsNarrow") {
		t.Fatalf("extra lines = %+v", extra)
	}
}

func TestRadarModeRespectsTrackDestructionAndUnknownModel(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 3000)
	tr.hasEnd, tr.end = true, 2500
	known := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(2000, uid, radarModeTestOn(1, 0, 1, 0, 0, 0, 1)),
		radarModeTestFrame(2600, uid, radarModeTestOff(1)),
	}, tr)
	if len(known.extraLines()) != 1 || known.extraLines()[0].t != 2000 {
		t.Fatalf("post-destruction state leaked: %+v", known.extraLines())
	}
	unknownTrack := radarModeTestTrack(uid, "not_in_datamine", 2000)
	unknown := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(2000, uid, radarModeTestOn(1, 0, 1, 0, 0, 0, 1)),
	}, unknownTrack)
	if got := unknown.lineProperties(uid, 2000, unknownTrack); len(got) != 0 {
		t.Fatalf("unresolved model guessed a sensor slot: %v", got)
	}
}

func TestRadarModeSourcesDoNotShareState(t *testing.T) {
	first := radarModeTestTrack(5, "test_radar_aircraft", 1000)
	second := radarModeTestTrack(6, "test_radar_aircraft", 1000)
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, 5, radarModeTestOn(1, 0, 1, 0, 0, 0, 1)),
		radarModeTestFrame(1000, 6, radarModeTestOff(1)),
	}, first, second)
	if props := radarModeTestProperties(x, 5, 1000, first); props["WT_RadarMode"] != "scan" {
		t.Fatalf("source 5 = %v", props)
	}
	if props := radarModeTestProperties(x, 6, 1000, second); props["WT_RadarMode"] != "off" {
		t.Fatalf("source 6 = %v", props)
	}
}

func TestRadarModeSameUIDSortiesStartWithFreshState(t *testing.T) {
	first := radarTestSortieTrack(5, 0, radarTestSample(1000, 0), radarTestSample(2000, 0))
	first.hasEnd, first.end = true, 2500
	second := radarTestSortieTrack(5, 1, radarTestSample(3000, 0), radarTestSample(4000, 0))
	for _, tr := range []*track{first, second} {
		tr.entity = radarModeTestTrack(5, "test_radar_aircraft").entity
	}
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, 5, radarModeTestOn(1, 0, 0, 0, 0, 0, 1)),
		radarModeTestFrame(2000, 5, radarModeTestOff(1)),
		radarModeTestFrame(3000, 5, radarModeTestOn(1, 0, 0, 0, 0, 0, 3)),
		radarModeTestFrame(4000, 5, radarModeTestOff(1)),
	}, first, second)

	firstOn := radarModeTestProperties(x, 5, 1000, first)
	if firstOn["WT_RadarMode"] != "scan" || firstOn["RadarMode"] != "1" {
		t.Fatalf("first sortie mode = %v", firstOn)
	}
	if off := radarModeTestProperties(x, 5, 2000, first); off["WT_RadarMode"] != "off" {
		t.Fatalf("first sortie off = %v", off)
	}
	secondOn := radarModeTestProperties(x, 5, 3000, second)
	if secondOn["WT_RadarMode"] != "scan" || secondOn["RadarMode"] != "1" || secondOn["WT_RadarModeChangeTime"] != "3.000" {
		t.Fatalf("second sortie did not restart from an empty mode state: %v", secondOn)
	}
	if off := radarModeTestProperties(x, 5, 4000, second); off["WT_RadarMode"] != "off" {
		t.Fatalf("second sortie off = %v", off)
	}
	if got := x.lineProperties(5, 3000, first); len(got) != 0 {
		t.Fatalf("second sortie's mode leaked onto the first object: %v", got)
	}
	if len(x.extraLines()) != 0 {
		t.Fatalf("exactly sampled sortie modes produced independent extra lines: %+v", x.extraLines())
	}
}

func TestRadarModeShortRecordDoesNotClearOrInventMode(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 2000)
	fm := &packetfm.PacketFlightModelParser{Results: []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, radarModeTestOn(1, 0, 0, 0, 0, 0, 1)),
		radarTestShortFrame(2000, uid),
	}}
	if frames := collectRadarModeFrames(fm); len(frames[uid]) != 1 || frames[uid][0].t != 1000 {
		t.Fatalf("short FM record entered sensor frames: %+v", frames[uid])
	}
	x := radarModeTestPrepare(t, fm.Results, tr)
	if got := x.lineProperties(uid, 2000, tr); len(got) != 0 {
		t.Fatalf("short FM record emitted a mode change: %v", got)
	}
	if got := x.lineProperties(uid, 1000, tr); !strings.Contains(strings.Join(got, ","), "WT_RadarMode=scan") {
		t.Fatalf("short record disturbed the previous mode: %v", got)
	}
	if len(x.extraLines()) != 0 {
		t.Fatalf("short record created an extra line: %+v", x.extraLines())
	}
}

func TestRadarModeRepeatedPrepareDoesNotLeakState(t *testing.T) {
	const uid = 5
	tr := radarModeTestTrack(uid, "test_radar_aircraft", 1000, 2000)
	x := radarModeTestPrepare(t, []packetfm.FMUpdatePacket{
		radarModeTestFrame(1000, uid, radarModeTestOn(1, 0, 1, 0, 0, 0, 1)),
		radarModeTestFrame(2000, uid, radarModeTestOff(1)),
	}, tr)
	first := strconv.Itoa(len(x.extraLines())) + ":" + strings.Join(x.lineProperties(uid, 1000, tr), ",")
	x.prepare([]*track{tr}, acmiObjectIDs([]*track{tr}))
	second := strconv.Itoa(len(x.extraLines())) + ":" + strings.Join(x.lineProperties(uid, 1000, tr), ",")
	if first != second {
		t.Fatalf("repeated prepare changed export:\nfirst: %s\nsecond: %s", first, second)
	}
}

func TestEmbeddedRadarModeCatalogProvenanceAndRWRSlot(t *testing.T) {
	if defaultRadarModeCatalog == nil {
		t.Fatal("embedded radar mode catalog did not load")
	}
	if got := defaultRadarModeCatalog.Source.Commit; got != "f7952e4291e9fd7b6fe38e84020dddef925c3457" {
		t.Fatalf("embedded commit = %q", got)
	}
	refs := defaultRadarModeCatalog.Models["f_16c_block_50"]
	if len(refs) != 4 {
		t.Fatalf("F-16C sensor slots = %v", refs)
	}
	if !strings.Contains(strings.ToLower(refs[2]), "alr_56") {
		t.Fatalf("F-16C slot 2 lost the RWR placeholder: %v", refs)
	}
	esm := defaultRadarModeCatalog.Sensors[strings.ToLower(refs[1])]
	if len(esm.Transceivers) != 1 || esm.Transceivers[0].VisibilityType != "radarIntercept" || esm.Transceivers[0].Active {
		t.Fatalf("F-16C ESM was not preserved as passive radar-intercept: %+v", esm)
	}
	radar := defaultRadarModeCatalog.Sensors[strings.ToLower(refs[0])]
	if radar.Name != "AN/APG-68" {
		t.Fatalf("F-16C radar = %+v", radar)
	}
}
