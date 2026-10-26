package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
)

// Radar-mode records index the static sensor blocks by their original key
// order. The generated table therefore contains the exact names and slots.
//
//go:embed data/radar_modes.json
var radarModeCatalogData []byte

// Display names are separate from the original pattern IDs used by the
// catalogue and provenance properties. They are embedded for standalone use.
//
//go:embed data/radar_mode_labels.json
var radarModeLabelsData []byte

var radarModeDisplayNames = func() map[string]string {
	var labels map[string]string
	if err := json.Unmarshal(radarModeLabelsData, &labels); err != nil {
		panic("invalid embedded radar mode display labels: " + err.Error())
	}
	return labels
}()

func radarModeDisplayName(pattern string) string {
	if label := radarModeDisplayNames[pattern]; label != "" {
		return label
	}
	return pattern
}

const radarModeCatalogSchema = 1

type radarModeSource struct {
	Repository       string `json:"repository"`
	Commit           string `json:"commit"`
	FlightmodelsTree string `json:"flightmodels_tree"`
	SensorsTree      string `json:"sensors_tree"`
	FlightmodelsPath string `json:"flightmodels_path"`
	SensorsPath      string `json:"sensors_path"`
	Generator        string `json:"generator"`
	Note             string `json:"note"`
}

type radarModeCatalog struct {
	Schema  int                        `json:"schema"`
	Source  radarModeSource            `json:"source"`
	Models  map[string][]string        `json:"models"`
	Sensors map[string]radarModeSensor `json:"sensors"`
}

type radarModeSensor struct {
	Path         string                 `json:"path"`
	Name         string                 `json:"name"`
	Type         string                 `json:"type"`
	Transceivers []radarModeTransceiver `json:"transceivers"`
	ScanPatterns []radarModeScanPattern `json:"scan_patterns"`
	Signals      []string               `json:"signals"`
}

type radarModeTransceiver struct {
	ID             string `json:"id"`
	VisibilityType string `json:"visibility_type"`
	Active         bool   `json:"active"`
}

type radarModeScanPattern struct {
	ID                     string   `json:"id"`
	Types                  []string `json:"types"`
	ScanWidthDeg           *float64 `json:"scan_width_deg,omitempty"`
	ScanBarHeightDeg       *float64 `json:"scan_bar_height_deg,omitempty"`
	HorizontalBeamwidthDeg *float64 `json:"horizontal_beamwidth_deg,omitempty"`
	VerticalBeamwidthDeg   *float64 `json:"vertical_beamwidth_deg,omitempty"`
}

type radarModeExportOptions struct {
	Table *radarModeCatalog
}

type radarModeSensorFrame struct {
	t       uint32
	sensors []packetfm.FMSensorData
}

type radarModeSnapshot struct {
	valid bool

	mode     string // off, scan, track or unknown
	nativeOn bool
	source   string

	slot       int
	sensorName string

	transceiver       string
	pattern           string
	signal            string
	azimuthDeg        float64
	elevationDeg      float64
	modeChangeTime    float64
	hasModeChange     bool
	beamHorizontal    float64
	beamVertical      float64
	hasBeamHorizontal bool
	hasBeamVertical   bool
}

// radarModeExport converts the live per-sensor FM state into Tacview's native
// Radar* properties. Static mode details remain WT_* text so no invented
// Tacview enum is emitted. prepare rebuilds all writer-visible state.
type radarModeExport struct {
	table *radarModeCatalog

	// Frames remain grouped by masked replay uid. prepare assigns them to the
	// unique sortie alive at each timestamp.
	frames map[uint64][]radarModeSensorFrame
	ids    map[trackKey]string
	tracks map[trackKey]*track
	byUID  map[uint64][]*track

	events map[trackKey]map[uint32][]string
	extra  []radarLockExtraLine

	prepared bool
}

func loadRadarModeCatalog(raw []byte) (*radarModeCatalog, error) {
	var catalog radarModeCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("radar mode table: %w", err)
	}
	if catalog.Schema != radarModeCatalogSchema {
		return nil, fmt.Errorf("radar mode table schema = %d, want %d", catalog.Schema, radarModeCatalogSchema)
	}
	if len(catalog.Models) == 0 || len(catalog.Sensors) == 0 {
		return nil, fmt.Errorf("radar mode table has no models or sensors")
	}

	models := make(map[string][]string, len(catalog.Models))
	for model, refs := range catalog.Models {
		models[strings.ToLower(strings.TrimSpace(model))] = append([]string(nil), refs...)
	}
	sensors := make(map[string]radarModeSensor, len(catalog.Sensors))
	for name, sensor := range catalog.Sensors {
		sensors[strings.ToLower(strings.TrimSpace(name))] = sensor
	}
	for model, refs := range models {
		for _, ref := range refs {
			if _, ok := sensors[strings.ToLower(ref)]; !ok {
				return nil, fmt.Errorf("radar mode table model %q references missing sensor %q", model, ref)
			}
		}
	}
	catalog.Models = models
	catalog.Sensors = sensors
	return &catalog, nil
}

var defaultRadarModeCatalog = func() *radarModeCatalog {
	catalog, err := loadRadarModeCatalog(radarModeCatalogData)
	if err != nil {
		panic(err)
	}
	return catalog
}()

// newRadarModeExport accepts an optional in-memory catalog so callers and tests
// do not need a global flag. The default is the embedded pinned Datamine table.
func newRadarModeExport(fm *packetfm.PacketFlightModelParser, options ...radarModeExportOptions) *radarModeExport {
	if fm == nil {
		return nil
	}
	catalog := defaultRadarModeCatalog
	if len(options) > 0 && options[0].Table != nil {
		catalog = options[0].Table
	}
	if catalog == nil {
		return nil
	}
	return &radarModeExport{table: catalog, frames: collectRadarModeFrames(fm)}
}

func collectRadarModeFrames(fm *packetfm.PacketFlightModelParser) map[uint64][]radarModeSensorFrame {
	byTime := map[uint64]map[uint32][]packetfm.FMSensorData{}
	for _, frame := range fm.Results {
		for _, entry := range frame.Entries {
			// Unk0&&Unk1 is a flags-only record with zero-filled telemetry, not an
			// explicit empty sensor list.
			if entry == nil || entry.Data == nil || (entry.Data.Unk0 && entry.Data.Unk1) {
				continue
			}
			states := byTime[entry.UID]
			if states == nil {
				states = map[uint32][]packetfm.FMSensorData{}
				byTime[entry.UID] = states
			}
			states[frame.CurrentTime] = append([]packetfm.FMSensorData(nil), entry.Data.SensorsData...)
		}
	}

	out := map[uint64][]radarModeSensorFrame{}
	for uid, states := range byTime {
		times := make([]uint32, 0, len(states))
		for t := range states {
			times = append(times, t)
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		frames := make([]radarModeSensorFrame, 0, len(times))
		for _, t := range times {
			frames = append(frames, radarModeSensorFrame{t: t, sensors: states[t]})
		}
		out[uid] = frames
	}
	return out
}

func (x *radarModeExport) prepare(tracks []*track, ids map[trackKey]string) {
	if x == nil {
		return
	}
	x.ids = map[trackKey]string{}
	x.tracks = map[trackKey]*track{}
	x.byUID = map[uint64][]*track{}
	for _, tr := range tracks {
		if !radarLockAirframe(tr) {
			continue
		}
		x.tracks[tr.key] = tr
		x.byUID[tr.key.id] = append(x.byUID[tr.key.id], tr)
		if id := ids[tr.key]; id != "" {
			x.ids[tr.key] = id
		}
	}
	for uid := range x.byUID {
		radarSortTracks(x.byUID[uid])
	}

	x.events = map[trackKey]map[uint32][]string{}
	x.extra = nil
	x.prepared = true

	uids := make([]uint64, 0, len(x.frames))
	for uid := range x.frames {
		if len(x.byUID[uid]) > 0 {
			uids = append(uids, uid)
		}
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	for _, uid := range uids {
		assigned := map[trackKey][]radarModeSensorFrame{}
		for _, frame := range x.frames[uid] {
			tr := radarTrackAt(x.byUID[uid], uid, frame.t)
			if tr == nil || x.ids[tr.key] == "" {
				continue
			}
			assigned[tr.key] = append(assigned[tr.key], frame)
		}
		for _, tr := range x.byUID[uid] {
			if frames := assigned[tr.key]; len(frames) > 0 {
				x.prepareSource(tr, frames)
			}
		}
	}
	sort.Slice(x.extra, func(i, j int) bool {
		if x.extra[i].t != x.extra[j].t {
			return x.extra[i].t < x.extra[j].t
		}
		return x.extra[i].line < x.extra[j].line
	})
}

func (x *radarModeExport) prepareSource(tr *track, frames []radarModeSensorFrame) {
	if tr == nil || len(frames) == 0 || x.ids[tr.key] == "" {
		return
	}
	model, ok := radarModeTrackModel(tr)
	if !ok {
		return
	}
	refs := x.table.Models[model]
	if len(refs) == 0 {
		return
	}

	previous := radarModeSnapshot{}
	for _, frame := range frames {
		if !radarModeTrackLiveAt(tr, frame.t) {
			if tr.hasEnd && frame.t >= tr.end {
				break
			}
			continue
		}
		current, present := x.selectRadarModeState(refs, frame.sensors, previous)
		if !present && previous.valid && len(frame.sensors) == 0 {
			// A complete FM sync with no sensor records cannot retain a previously
			// observed mode. Clear it as unknown rather than calling the radar off.
			current, present = radarModeUnknownSnapshot(-1, radarModeSensor{}, "fm-no-sensor-records"), true
		}
		if !present {
			continue
		}
		if radarModeSnapshotsEqual(previous, current) ||
			(!previous.valid && current.mode == "unknown" && current.source == "fm-unknown-sensor-slot") {
			continue
		}
		props := radarModeProperties(previous, current)
		if len(props) == 0 {
			continue
		}
		if radarModeTrackHasSample(tr, frame.t) {
			events := x.events[tr.key]
			if events == nil {
				events = map[uint32][]string{}
				x.events[tr.key] = events
			}
			events[frame.t] = props
		} else {
			x.extra = append(x.extra, radarLockExtraLine{
				t:    frame.t,
				line: x.ids[tr.key] + "," + strings.Join(props, ","),
			})
		}
		previous = current
	}
}

func radarModeTrackModel(tr *track) (string, bool) {
	if tr == nil || tr.entity == nil {
		return "", false
	}
	model, ok := packetecs2.GetObjectData[string](&tr.entity.Data, "unit__className")
	if !ok {
		return "", false
	}
	model = strings.ToLower(strings.TrimSpace(model))
	return model, model != ""
}

func radarModeTrackLiveAt(tr *track, t uint32) bool {
	if tr == nil || len(tr.samples) == 0 || t < tr.samples[0].t || t > tr.samples[len(tr.samples)-1].t {
		return false
	}
	return !tr.hasEnd || t < tr.end
}

func radarModeTrackHasSample(tr *track, t uint32) bool {
	if tr == nil {
		return false
	}
	i := sort.Search(len(tr.samples), func(i int) bool { return tr.samples[i].t >= t })
	return i < len(tr.samples) && tr.samples[i].t == t
}

func (x *radarModeExport) selectRadarModeState(refs []string, sensors []packetfm.FMSensorData, previous radarModeSnapshot) (radarModeSnapshot, bool) {
	var candidate radarModeSnapshot
	priority := 100
	present := false
	for _, sensorState := range sensors {
		slot := sensorState.SensorSlot()
		kind := sensorState.SensorKind()
		sensor, known := radarModeSensorAt(x.table, refs, slot)
		radarCapable := known && radarModeSensorHasActiveRadar(sensor)
		relevant := radarCapable || (previous.valid && slot == previous.slot)

		var state radarModeSnapshot
		var statePresent bool
		var statePriority int

		switch {
		case kind == 1 && !sensorState.FirstBool:
			if radarCapable {
				state, statePresent, statePriority = radarModeOffSnapshot(slot, sensor, "fm-kind1-off"), true, 2
			} else if relevant && known {
				state, statePresent, statePriority = radarModeUnknownSnapshot(slot, sensor, "fm-kind1-off-non-radar"), true, 4
			}

		case kind == 1 && (sensorState.Data1 == nil || !sensorState.Data1.HasState()):
			if radarCapable {
				state, statePresent, statePriority = radarModeUnknownSnapshot(slot, sensor, "fm-kind1-no-state"), true, 3
			}

		case kind == 1:
			transceiverIndex, patternIndex, signalIndex := packetfm.RadarModeIndices(sensorState.Data1.PackedIndices())
			transceiver, transceiverOK := radarModeTransceiverAt(sensor, transceiverIndex)
			pattern, patternOK := radarModePatternAt(sensor, patternIndex)
			if transceiverOK && radarModeTransceiverIsActiveRadar(transceiver) {
				if !patternOK {
					state, statePresent, statePriority = radarModeUnknownSnapshot(slot, sensor, "fm-kind1-invalid-pattern"), true, 3
				} else {
					state = radarModeActiveSnapshot(slot, sensor, transceiver, pattern)
					if signalIndex >= 0 && signalIndex < len(sensor.Signals) {
						state.signal = sensor.Signals[signalIndex]
					}
					state.azimuthDeg = radarModeAzimuthDegrees(sensorState.Data1.AzimuthRaw())
					state.elevationDeg = radarModeElevationDegrees(sensorState.Data1.ElevationRaw())
					changeTime := sensorState.Data1.ModeChangeTime()
					if radarModeFinite(changeTime) && changeTime >= 0 {
						state.modeChangeTime = changeTime
						state.hasModeChange = true
					}
					statePresent, statePriority = true, 0
				}
			} else if relevant || radarCapable {
				source := "fm-kind1-invalid-transceiver"
				if transceiverOK {
					source = "fm-kind1-non-radar-transceiver"
				}
				state, statePresent, statePriority = radarModeUnknownSnapshot(slot, sensor, source), true, 3
			}

		default:
			// Kind 2 is not intrinsically IRST. Without a transceiver index it
			// cannot select one of a mixed sensor's emitters, so only use it to
			// clear a mode previously held by the same sensor slot.
			if relevant && known {
				state, statePresent, statePriority = radarModeUnknownSnapshot(slot, sensor, fmt.Sprintf("fm-sensor-kind-%d", kind)), true, 4
			}
		}

		if statePresent && (!present || statePriority < priority || (statePriority == priority && state.slot < candidate.slot)) {
			candidate, priority, present = state, statePriority, true
		}
	}

	if !present && previous.valid && len(sensors) > 0 {
		// A replay may change to a slot absent from the pinned datamine table.
		// Do not equate that with off: clear the previous values as unknown.
		return radarModeUnknownSnapshot(-1, radarModeSensor{}, "fm-unknown-sensor-slot"), true
	}
	return candidate, present
}

func radarModeSensorAt(catalog *radarModeCatalog, refs []string, slot int) (radarModeSensor, bool) {
	if catalog == nil || slot < 0 || slot >= len(refs) {
		return radarModeSensor{}, false
	}
	sensor, ok := catalog.Sensors[strings.ToLower(refs[slot])]
	return sensor, ok
}

func radarModeSensorHasActiveRadar(sensor radarModeSensor) bool {
	for _, transceiver := range sensor.Transceivers {
		if radarModeTransceiverIsActiveRadar(transceiver) {
			return true
		}
	}
	return false
}

func radarModeTransceiverIsActiveRadar(t radarModeTransceiver) bool {
	return t.Active && strings.TrimSpace(t.VisibilityType) == ""
}

func radarModeTransceiverAt(sensor radarModeSensor, index int) (radarModeTransceiver, bool) {
	if index < 0 || index >= len(sensor.Transceivers) {
		return radarModeTransceiver{}, false
	}
	return sensor.Transceivers[index], true
}

func radarModePatternAt(sensor radarModeSensor, index int) (radarModeScanPattern, bool) {
	if index < 0 || index >= len(sensor.ScanPatterns) {
		return radarModeScanPattern{}, false
	}
	pattern := sensor.ScanPatterns[index]
	return pattern, strings.TrimSpace(pattern.ID) != "" && len(pattern.Types) > 0
}

func radarModeOffSnapshot(slot int, sensor radarModeSensor, source string) radarModeSnapshot {
	return radarModeSnapshot{valid: true, mode: "off", slot: slot, sensorName: sensor.Name, source: source}
}

func radarModeUnknownSnapshot(slot int, sensor radarModeSensor, source string) radarModeSnapshot {
	return radarModeSnapshot{valid: true, mode: "unknown", slot: slot, sensorName: sensor.Name, source: source}
}

func radarModeActiveSnapshot(slot int, sensor radarModeSensor, transceiver radarModeTransceiver, pattern radarModeScanPattern) radarModeSnapshot {
	mode := "scan"
	track := true
	for _, patternType := range pattern.Types {
		if !strings.EqualFold(strings.TrimSpace(patternType), "no") {
			track = false
			break
		}
	}
	if track {
		mode = "track"
	}
	snapshot := radarModeSnapshot{
		valid: true, mode: mode, nativeOn: true, source: "fm-kind1",
		slot: slot, sensorName: sensor.Name,
		transceiver: transceiver.ID, pattern: pattern.ID,
	}
	if pattern.HorizontalBeamwidthDeg != nil {
		snapshot.beamHorizontal = *pattern.HorizontalBeamwidthDeg
		snapshot.hasBeamHorizontal = radarModeFinite(snapshot.beamHorizontal) && snapshot.beamHorizontal >= 0
	}
	if pattern.VerticalBeamwidthDeg != nil {
		snapshot.beamVertical = *pattern.VerticalBeamwidthDeg
		snapshot.hasBeamVertical = radarModeFinite(snapshot.beamVertical) && snapshot.beamVertical >= 0
	}
	return snapshot
}

// Tracking samples of the supported runtime layout establish right-positive
// radar azimuth, matching Tacview. This is distinct from pre-launch IR gimbals.
func radarModeAzimuthDegrees(raw uint16) float64 {
	return radarModeAngleDegrees(raw)
}

// War Thunder body elevation is positive up, matching Tacview.
func radarModeElevationDegrees(raw uint16) float64 {
	return radarModeAngleDegrees(raw)
}

func radarModeAngleDegrees(raw uint16) float64 {
	return float64(int16(raw)) * math.Pi / 32767.0 * 180 / math.Pi
}

func radarModeFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func radarModeSnapshotsEqual(a, b radarModeSnapshot) bool {
	return a.valid == b.valid && a.mode == b.mode && a.nativeOn == b.nativeOn &&
		a.source == b.source && a.slot == b.slot && a.sensorName == b.sensorName &&
		a.transceiver == b.transceiver && a.pattern == b.pattern && a.signal == b.signal &&
		radarModeNumberEqual(a.azimuthDeg, b.azimuthDeg) &&
		radarModeNumberEqual(a.elevationDeg, b.elevationDeg) &&
		a.hasModeChange == b.hasModeChange && radarModeNumberEqual(a.modeChangeTime, b.modeChangeTime) &&
		a.hasBeamHorizontal == b.hasBeamHorizontal && radarModeNumberEqual(a.beamHorizontal, b.beamHorizontal) &&
		a.hasBeamVertical == b.hasBeamVertical && radarModeNumberEqual(a.beamVertical, b.beamVertical)
}

func radarModeNumberEqual(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) < 1e-9
}

func radarModeProperties(previous, current radarModeSnapshot) []string {
	if radarModeSnapshotsEqual(previous, current) {
		return nil
	}
	nativeMode := 0
	if current.nativeOn && (current.mode == "scan" || current.mode == "track") {
		nativeMode = 1
	}
	mode := current.mode
	if mode == "" {
		mode = "unknown"
	}
	props := []string{
		"RadarMode=" + strconv.Itoa(nativeMode),
		"WT_RadarMode=" + mode,
		"WT_RadarStateSource=" + escapeACMIValue(current.source),
		"WT_RadarSlot=" + strconv.Itoa(current.slot),
		"WT_RadarSensor=" + escapeACMIValue(current.sensorName),
		"WT_RadarTransceiver=" + escapeACMIValue(current.transceiver),
		"WT_RadarPattern=" + escapeACMIValue(current.pattern),
		"WT_RadarSignal=" + escapeACMIValue(current.signal),
	}
	// Label is a native ACMI text property, so precise game pattern names are
	// readable without pretending that Tacview defines TWS/STT mode numbers.
	label := "Unknown"
	if mode == "off" {
		label = "Off"
	} else if nativeMode == 1 {
		label = radarModeDisplayName(current.pattern)
	}
	props = append(props, "Label="+escapeACMIValue(label))

	azimuth, elevation := 0.0, 0.0
	if nativeMode == 1 {
		azimuth, elevation = current.azimuthDeg, current.elevationDeg
	}
	props = append(props,
		"RadarAzimuth="+radarModeFormat(azimuth, 2),
		"RadarElevation="+radarModeFormat(elevation, 2),
	)
	if current.hasModeChange {
		props = append(props, "WT_RadarModeChangeTime="+radarModeFormat(current.modeChangeTime, 3))
	} else if previous.hasModeChange {
		props = append(props, "WT_RadarModeChangeTime=")
	}
	if current.hasBeamHorizontal {
		props = append(props, "RadarHorizontalBeamwidth="+radarModeFormat(current.beamHorizontal, 2))
	} else if previous.hasBeamHorizontal {
		props = append(props, "RadarHorizontalBeamwidth=0")
	}
	if current.hasBeamVertical {
		props = append(props, "RadarVerticalBeamwidth="+radarModeFormat(current.beamVertical, 2))
	} else if previous.hasBeamVertical {
		props = append(props, "RadarVerticalBeamwidth=0")
	}
	return props
}

func radarModeFormat(value float64, decimals int) string {
	if !radarModeFinite(value) {
		value = 0
	}
	if math.Abs(value) < 0.5*math.Pow10(-decimals) {
		value = 0
	}
	return strconv.FormatFloat(value, 'f', decimals, 64)
}

// lineProperties returns properties only for an FM state transition at this
// exact track-sample timestamp. A later state is never borrowed by an earlier
// sample.
func (x *radarModeExport) lineProperties(uid uint64, t uint32, tr *track) []string {
	if x == nil || !x.prepared || tr == nil || tr.key.id != uid || !tr.key.air || !radarModeTrackLiveAt(tr, t) {
		return nil
	}
	events := x.events[tr.key]
	if events == nil {
		return nil
	}
	return events[t]
}

func (x *radarModeExport) extraLines() []radarLockExtraLine {
	if x == nil {
		return nil
	}
	return x.extra
}
