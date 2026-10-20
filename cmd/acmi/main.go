package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
	packetmovement "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/movement"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

var (
	inPath               = flag.String("in", "", "input .wrpl replay")
	outPath              = flag.String("out", "", "output .acmi (default: input name with .acmi)")
	lat0                 = flag.Float64("lat", 0, "reference latitude in degrees")
	lon0                 = flag.Float64("lon", 0, "reference longitude in degrees")
	alt0                 = flag.Float64("alt", 0, "reference altitude in meters")
	centerX              = flag.Float64("center-x", 0, "local X coordinate of the map origin")
	centerZ              = flag.Float64("center-z", 0, "local Z coordinate of the map origin")
	hashPath             = flag.String("ecshashes", "data/ecshashes.json", "ECS component hash map (empty to disable)")
	namesPath            = flag.String("names", "", "optional JSON map: air:<uid> / ground:<eid> / missile:<eid> -> display name")
	listPath             = flag.String("list", "", "optional CSV listing of tracks and current names")
	missileFlightLogPath = flag.String("missile-flight-log", "", "optional CSV of missile flight parameters and raw motion evidence")
	countermeasures      = flag.Bool("countermeasures", true, "export aircraft countermeasure releases as Misc+Decoy events and tracks (real sync, or an estimated short trajectory when no sync exists)")
	countermeasureLog    = flag.String("countermeasure-log", "", "optional CSV of every trigger-14 countermeasure construction")
)

type trackKey struct {
	air     bool
	missile bool
	decoy   bool
	id      uint64
}

// missileTrack is the parser-to-writer adapter. ID is the full generation EID,
// rather than an entity-array index, so recycled indices remain distinct.
type missileTrack struct {
	ID        uint64
	Name      string
	ParentUID uint64
	HasParent bool
	Samples   []acmiSample
	EndTime   uint32
	HasEnd    bool
	Source    *projectiles.Projectile
}

type acmiSample struct {
	t          uint32
	x          float64
	y          float64
	z          float64
	roll       float64
	pitch      float64
	yaw        float64
	projectile *projectiles.Sample
}

type track struct {
	key              trackKey
	entity           *packetecs2.Entity
	samples          []acmiSample
	name             string
	parent           *track
	end              uint32
	hasEnd           bool
	projectileSource *projectiles.Projectile
	missileModel     *missileModelInfo
	// estimated marks a countermeasure track reconstructed from a release
	// point and the parent aircraft velocity, not from WeaponSync telemetry.
	estimated         bool
	estimatedVelocity bool
}

func main() {
	flag.Parse()
	if *inPath == "" && flag.NArg() > 0 {
		*inPath = flag.Arg(0)
	}
	if *inPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *outPath == "" {
		ext := filepath.Ext(*inPath)
		*outPath = filepath.Base(strings.TrimSuffix(*inPath, ext)) + ".acmi"
	}

	f, err := os.Open(*inPath)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	replay, err := wrpl.OpenReplay(f, true, true, true)
	if err != nil {
		fatal(err)
	}
	defer replay.Close()

	chms, err := readHashes(*hashPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: ECS names disabled: %v\n", err)
	}
	var ecs *packetecs2.PacketECSParser
	var mgr *packetecs2.EntityManager
	var missileMetadata *projectiles.MetadataParser
	if chms != nil {
		ecs = packetecs2.NewPacketECSParser(*chms)
		ecs.ConstructionReservedByte = replay.Header.Version == 0x00018C1C
		mgr = &ecs.Mgr
		missileMetadata = projectiles.NewMetadataParser(*chms, replay.Header.Version == 0x00018C1C)
	}
	slot := &packetslot.PacketSlotParser{}
	fm := &packetfm.PacketFlightModelParser{KeepResults: true, ECS: mgr}
	paths := packetmovement.NewPositionRetainerParser()
	missilePositions := projectiles.NewPositionParser()
	missileNames := newMissileNameKillParser()
	parsers := []packet.PacketParser{}
	if ecs != nil {
		parsers = append(parsers, ecs)
	}
	parsers = append(parsers, slot, fm, paths)
	if missileMetadata != nil {
		parsers = append(parsers, missileMetadata)
	}
	parsers = append(parsers, missilePositions, missileNames)
	if _, err := packet.ParsePackets(packet.NewPacketStreamReader(replay.PacketStream), parsers); err != nil {
		fatal(err)
	}

	tracks := collectTracks(fm, paths, mgr, slot)
	tracks = appendMissileTracks(tracks, decodedMissileTracks(missileMetadata, missilePositions))
	applyMissileModelNames(tracks, missileNames)
	cmExport := prepareCountermeasures(missileMetadata, missilePositions, tracks, *countermeasures)
	tracks = cmExport.Tracks
	if *missileFlightLogPath != "" {
		if err := writeMissileFlightLog(*missileFlightLogPath, tracks); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote missile flight log %s\n", *missileFlightLogPath)
	}
	if *countermeasures && *countermeasureLog != "" {
		if err := writeCountermeasureLog(*countermeasureLog, cmExport.Records); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote countermeasure log %s\n", *countermeasureLog)
	}
	if len(tracks) == 0 && len(cmExport.Events) == 0 {
		fatal(fmt.Errorf("no tracks found in replay"))
	}
	names, err := readNames(*namesPath)
	if err != nil {
		fatal(err)
	}
	if *listPath != "" {
		if err := writeTrackList(*listPath, tracks, slot, names); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote track list %s\n", *listPath)
	}
	if err := writeACMIWithEvents(*outPath, replay.Header, tracks, slot, names, cmExport.Events); err != nil {
		fatal(err)
	}
	samples := 0
	aircraft, identified := 0, 0
	missiles, missileSamples := 0, 0
	missileModels := 0
	decoys, decoySamples := 0, 0
	estimatedDecoys, estimatedSamples := 0, 0
	for _, tr := range tracks {
		samples += len(tr.samples)
		if tr.key.missile {
			missiles++
			missileSamples += len(tr.samples)
			if tr.missileModel != nil && tr.missileModel.Model != "" {
				missileModels++
			}
		}
		if tr.key.decoy {
			decoys++
			decoySamples += len(tr.samples)
			if tr.estimated {
				estimatedDecoys++
				estimatedSamples += len(tr.samples)
			}
		}
		if tr.key.air {
			aircraft++
			if trackName(tr, slot, names) != fmt.Sprintf("air:%d", tr.key.id) {
				identified++
			}
		}
	}
	fmt.Printf("wrote %s: %d tracks, %d samples\n", *outPath, len(tracks), samples)
	fmt.Printf("aircraft names identified: %d/%d\n", identified, aircraft)
	fmt.Printf("missiles exported: %d (%d recorded positions)\n", missiles, missileSamples)
	fmt.Printf("missile models identified: %d/%d\n", missileModels, missiles)
	fmt.Printf("countermeasures exported: %d tracks (%d recorded positions), %d release events, %d construction records\n",
		decoys, decoySamples, len(cmExport.Events), cmExport.Constructions)
	fmt.Printf("countermeasure estimated trajectories: %d tracks (%d synthesized positions)\n",
		estimatedDecoys, estimatedSamples)
	if identified < aircraft {
		fmt.Fprintln(os.Stderr, "warning: some aircraft names are unresolved; use -list to inspect them and -names to supply overrides")
	}
}

func readHashes(path string) (*packetecs2.ComponentHashMaps, error) {
	if path == "" {
		return nil, fmt.Errorf("disabled")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if exe, exeErr := os.Executable(); exeErr == nil {
			alt := filepath.Join(filepath.Dir(exe), path)
			data, err = os.ReadFile(alt)
		}
	}
	if err != nil {
		return nil, err
	}
	return packetecs2.ReadComponentHashMaps(bytes.NewReader(data))
}

func collectTracks(fm *packetfm.PacketFlightModelParser, paths *packetmovement.PositionRetainerParser, mgr *packetecs2.EntityManager, slot *packetslot.PacketSlotParser) []*track {
	byKey := map[trackKey]*track{}
	get := func(k trackKey) *track {
		tr := byKey[k]
		if tr == nil {
			tr = &track{key: k}
			byKey[k] = tr
		}
		return tr
	}
	if fm != nil {
		for _, frame := range fm.Results {
			for _, entry := range frame.Entries {
				if entry == nil || entry.Data == nil {
					continue
				}
				tr := get(trackKey{air: true, id: entry.UID})
				if tr.entity == nil {
					tr.entity = entry.ResolvedEntity
					if tr.entity == nil && mgr != nil {
						tr.entity = mgr.Uid_lookup[int32(entry.UID)]
					}
				}
				heading, attitude, bank := packetfm.UnpackEuler(entry.Data.EulerBytes)
				tr.samples = append(tr.samples, acmiSample{
					t:     frame.CurrentTime,
					x:     float64(entry.Data.PosX),
					y:     float64(entry.Data.PosY),
					z:     float64(entry.Data.PosZ),
					roll:  aircraftRollToACMI(float64(bank) * 180 / math.Pi),
					pitch: float64(attitude) * 180 / math.Pi,
					yaw:   aircraftHeadingToACMI(float64(heading) * 180 / math.Pi),
				})
			}
		}
	}
	if paths != nil {
		for eid, path := range paths.Paths {
			tr := get(trackKey{air: false, id: eid})
			if tr.entity == nil && mgr != nil {
				tr.entity = mgr.Entities[groundEntityIndex(eid)]
			}
			for i, p := range path {
				yaw := 0.0
				if i+1 < len(path) {
					x := path[i+1].X - p.X
					z := path[i+1].Z - p.Z
					if x != 0 || z != 0 {
						yaw = math.Atan2(x, z) * 180 / math.Pi
					}
				}
				tr.samples = append(tr.samples, acmiSample{t: p.Time, x: p.X, y: p.Y, z: p.Z, yaw: yaw})
			}
		}
	}

	tracks := make([]*track, 0, len(byKey))
	for _, tr := range byKey {
		if len(tr.samples) == 0 {
			continue
		}
		sort.Slice(tr.samples, func(i, j int) bool { return tr.samples[i].t < tr.samples[j].t })
		tracks = append(tracks, tr)
	}
	sort.Slice(tracks, func(i, j int) bool {
		ki, kj := trackKindOrder(tracks[i].key), trackKindOrder(tracks[j].key)
		if ki != kj {
			return ki < kj
		}
		return tracks[i].key.id < tracks[j].key.id
	})
	_ = slot
	return tracks
}

// appendMissileTracks adapts decoded missile records into writer tracks. Parent
// links are resolved only against an actual aircraft-source UID. Missile samples
// after an explicit end are discarded, and tracks without recorded samples are
// omitted. With no explicit end, the final sample is the recording cutoff used
// by the ACMI writer for object deletion.
func appendMissileTracks(tracks []*track, inputs []missileTrack) []*track {
	out := append([]*track(nil), tracks...)
	existing := make(map[trackKey]bool, len(tracks))
	aircraft := make(map[uint64]*track)
	for _, tr := range tracks {
		if tr == nil {
			continue
		}
		existing[tr.key] = true
		if tr.key.air && !tr.key.missile {
			if _, ok := aircraft[tr.key.id]; !ok {
				aircraft[tr.key.id] = tr
			}
		}
	}

	added := make([]*track, 0, len(inputs))
	for _, in := range inputs {
		samples := append([]acmiSample(nil), in.Samples...)
		sort.SliceStable(samples, func(i, j int) bool { return samples[i].t < samples[j].t })
		if in.HasEnd {
			end := sort.Search(len(samples), func(i int) bool { return samples[i].t > in.EndTime })
			samples = samples[:end]
		}
		if len(samples) == 0 {
			continue
		}
		key := trackKey{missile: true, id: in.ID}
		if existing[key] {
			continue
		}
		tr := &track{
			key:              key,
			name:             in.Name,
			samples:          samples,
			end:              in.EndTime,
			hasEnd:           in.HasEnd,
			projectileSource: in.Source,
		}
		if in.HasParent {
			tr.parent = aircraft[in.ParentUID]
		}
		added = append(added, tr)
		existing[key] = true
	}
	sort.Slice(added, func(i, j int) bool { return added[i].key.id < added[j].key.id })
	return append(out, added...)
}

func trackKindOrder(key trackKey) int {
	if key.decoy {
		return 3
	}
	if key.missile {
		return 2
	}
	if key.air {
		return 1
	}
	return 0
}

func groundEntityIndex(key uint64) uint32 {
	eid := ((uint64(uint64(key)&0xff) << uint64(0x16)) | (uint64(key) >> uint64(0x8))) & 0x7FF
	return uint32(packetecs2.EntityID(uint32(eid)).Index())
}

func writeACMI(path string, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string) error {
	return writeACMIWithEvents(path, header, tracks, slot, names, nil)
}

func writeACMIWithEvents(path string, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string, events []acmiEvent) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	events = append([]acmiEvent(nil), events...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		if events[i].parentID != events[j].parentID {
			return events[i].parentID < events[j].parentID
		}
		return events[i].text < events[j].text
	})
	first := acmiReferenceBase(tracks, events)
	ref := time.Now().UTC()
	if header.StartTime > 0 {
		ref = time.Unix(int64(header.StartTime), 0).UTC().Add(time.Duration(first) * time.Millisecond)
	}

	fmt.Fprintln(w, "FileType=text/acmi/tacview")
	fmt.Fprintln(w, "FileVersion=2.1")
	fmt.Fprintf(w, "0,ReferenceTime=%s\n", ref.Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(w, "0,ReferenceLongitude=%.7f\n", *lon0)
	fmt.Fprintf(w, "0,ReferenceLatitude=%.7f\n", *lat0)
	fmt.Fprintf(w, "0,ReferenceAltitude=%.1f\n", *alt0)
	fmt.Fprintln(w, "0,DataSource=War Thunder replay via wrpl-inspector")

	ids := map[trackKey]string{}
	for i, tr := range tracks {
		if tr == nil {
			continue
		}
		ids[tr.key] = fmt.Sprintf("%X", i+1)
	}
	properties := map[trackKey][]string{}
	for _, tr := range tracks {
		if tr == nil {
			continue
		}
		id := ids[tr.key]
		props := acmiTrackProperties(tr, slot, names, ids)
		properties[tr.key] = props
		if !tr.key.missile && !tr.key.decoy {
			fmt.Fprintf(w, "%s,%s\n", id, strings.Join(props, ","))
		}
	}

	updates := map[uint32][]string{}
	deletions := map[uint32][]string{}
	timesSeen := map[uint32]bool{}
	times := make([]uint32, 0)
	for _, tr := range tracks {
		if tr == nil {
			continue
		}
		id := ids[tr.key]
		if tr.key.missile {
			end, ok := missileTrackEnd(tr)
			if !ok || tr.samples[0].t > end {
				continue
			}
			flight := enrichRecordedMissileFlight(tr.samples)
			for i, s := range tr.samples {
				if s.t > end {
					break
				}
				position := acmiMissilePositionValue(s)
				flightProps := missileFlightPropertiesWithINS(flight[i], s)
				line := fmt.Sprintf("%s,T=%s,%s", id, position, strings.Join(flightProps, ","))
				if i == 0 {
					props := append([]string(nil), properties[tr.key]...)
					props = append(props, "T="+position)
					props = append(props, flightProps...)
					line = id + "," + strings.Join(props, ",")
				}
				if !timesSeen[s.t] {
					timesSeen[s.t] = true
					times = append(times, s.t)
				}
				updates[s.t] = append(updates[s.t], line)
			}
			if !timesSeen[end] {
				timesSeen[end] = true
				times = append(times, end)
			}
			deletions[end] = append(deletions[end], "-"+id)
			continue
		}
		if tr.key.decoy {
			end, ok := decoyTrackEnd(tr)
			if !ok || tr.samples[0].t > end {
				continue
			}
			for i, s := range tr.samples {
				if s.t > end {
					break
				}
				position := acmiPositionValue(s)
				line := fmt.Sprintf("%s,T=%s", id, position)
				if i == 0 {
					props := append([]string(nil), properties[tr.key]...)
					props = append(props, "T="+position)
					line = id + "," + strings.Join(props, ",")
				}
				if !timesSeen[s.t] {
					timesSeen[s.t] = true
					times = append(times, s.t)
				}
				updates[s.t] = append(updates[s.t], line)
			}
			if !timesSeen[end] {
				timesSeen[end] = true
				times = append(times, end)
			}
			deletions[end] = append(deletions[end], "-"+id)
			continue
		}
		for _, s := range tr.samples {
			if !timesSeen[s.t] {
				timesSeen[s.t] = true
				times = append(times, s.t)
			}
			updates[s.t] = append(updates[s.t], fmt.Sprintf("%s,T=%s", id, acmiPositionValue(s)))
		}
	}
	for _, event := range events {
		if !timesSeen[event.t] {
			timesSeen[event.t] = true
			times = append(times, event.t)
		}
		updates[event.t] = append(updates[event.t], acmiEventLine(event, ids))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	for _, t := range times {
		fmt.Fprintf(w, "#%.3f\n", float64(int64(t)-int64(first))/1000)
		for _, line := range updates[t] {
			fmt.Fprintln(w, line)
		}
		for _, line := range deletions[t] {
			fmt.Fprintln(w, line)
		}
	}
	return nil
}

// acmiReferenceBase preserves the legacy aircraft and missile origin. Decoy
// or event times earlier than that origin are written as negative offsets.
// Decoys only define the origin when a replay has no legacy track at all.
func acmiReferenceBase(tracks []*track, events []acmiEvent) uint32 {
	first := uint32(math.MaxUint32)
	for _, tr := range tracks {
		if tr == nil || tr.key.decoy {
			continue
		}
		end, endpointOK := missileTrackEnd(tr)
		for _, s := range tr.samples {
			if tr.key.missile && (!endpointOK || s.t > end) {
				continue
			}
			if s.t < first {
				first = s.t
			}
		}
	}
	if first != math.MaxUint32 {
		return first
	}
	for _, tr := range tracks {
		if tr == nil || !tr.key.decoy {
			continue
		}
		end, ok := decoyTrackEnd(tr)
		if !ok || tr.samples[0].t > end {
			continue
		}
		for _, s := range tr.samples {
			if s.t > end {
				break
			}
			if s.t < first {
				first = s.t
			}
		}
	}
	for _, event := range events {
		if event.t < first {
			first = event.t
		}
	}
	if first == math.MaxUint32 {
		return 0
	}
	return first
}

func acmiTrackProperties(tr *track, slot *packetslot.PacketSlotParser, names map[string]string, ids map[trackKey]string) []string {
	coalition := trackCoalition(tr, slot)
	color := coalition
	if color == "Neutral" {
		color = "White"
	}
	typ := "Ground+Vehicle"
	if tr.key.air {
		typ = "Air+FixedWing"
	}
	if tr.key.missile {
		typ = "Weapon+Missile"
	}
	if tr.key.decoy {
		typ = countermeasureType(tr.projectileSource)
	}
	name := trackName(tr, slot, names)
	props := []string{
		"Name=" + escapeACMIValue(name),
		"CallSign=" + escapeACMIValue(name),
	}
	if pilot := trackPilot(tr, slot); pilot != "" {
		props = append(props, "Pilot="+escapeACMIValue(pilot))
	}
	if tr.parent != nil {
		if parentID := ids[tr.parent.key]; parentID != "" {
			props = append(props, "Parent="+parentID)
		}
	}
	props = append(props,
		"Type="+typ,
		"Coalition="+coalition,
		"Color="+color,
	)
	if tr.estimated {
		props = append(props, countermeasureEstimatedProperties(tr)...)
	}
	if tr.key.missile {
		props = append(props, missileModelProperties(tr)...)
	}
	return props
}

func acmiPositionValue(s acmiSample) string {
	lat, lon := localToLatLon(s.x, s.z, *centerX, *centerZ, *lat0, *lon0)
	return fmt.Sprintf("%.7f|%.7f|%.2f|%.2f|%.2f|%.2f", lon, lat, *alt0+s.y, s.roll, s.pitch, s.yaw)
}

// aircraftRollToACMI converts War Thunder's packed FM bank to Tacview roll.
// The FM bank sign is opposite Tacview's convention, where positive roll is
// right wing down. Magnitude and the zero crossing are preserved.
func aircraftRollToACMI(bankDeg float64) float64 {
	return -bankDeg
}

// aircraftHeadingToACMI converts the packed aircraft heading axis to Tacview's
// clockwise heading. Ground headings are already derived clockwise from north.
func aircraftHeadingToACMI(heading float64) float64 {
	heading = math.Mod(heading+90, 360)
	if heading < 0 {
		heading += 360
	}
	return heading
}

func missileTrackEnd(tr *track) (uint32, bool) {
	if tr == nil || !tr.key.missile || len(tr.samples) == 0 {
		return 0, false
	}
	if tr.hasEnd {
		return tr.end, true
	}
	// A replay ending is also the cutoff for a missile whose source does not
	// provide an explicit destruction timestamp. No synthetic sample is added.
	return tr.samples[len(tr.samples)-1].t, true
}

func localToLatLon(x, z, cx, cz, lat, lon float64) (float64, float64) {
	const metersPerDegree = 111320.0
	dx := x - cx
	dz := z - cz
	lat += dz / metersPerDegree
	cosLat := math.Cos(lat * math.Pi / 180)
	if math.Abs(cosLat) < 1e-6 {
		cosLat = 1
	}
	lon += dx / (metersPerDegree * cosLat)
	return lat, lon
}

func readNames(path string) (map[string]string, error) {
	if path == "" {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("parsing names JSON: %w", err)
	}
	return names, nil
}

func writeTrackList(path string, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	qw := csv.NewWriter(f)
	defer qw.Flush()
	if err := qw.Write([]string{"object_id", "kind", "player", "team", "current_name", "start_sec", "end_sec", "samples", "class_name", "parent_source"}); err != nil {
		return err
	}
	for _, tr := range tracks {
		if tr == nil || len(tr.samples) == 0 {
			continue
		}
		kind := trackListKind(tr)
		player := ""
		if tr.entity != nil {
			if p := entityPlayer(tr.entity, slot); p != nil {
				player = p.Uid.Name
			}
		}
		start := tr.samples[0].t
		end := tr.samples[len(tr.samples)-1].t
		sampleCount := len(tr.samples)
		if tr.key.missile {
			var ok bool
			end, ok = missileTrackEnd(tr)
			if !ok || start > end {
				continue
			}
			sampleCount = sort.Search(len(tr.samples), func(i int) bool { return tr.samples[i].t > end })
		}
		if tr.key.decoy {
			var ok bool
			end, ok = decoyTrackEnd(tr)
			if !ok || start > end {
				continue
			}
			sampleCount = sort.Search(len(tr.samples), func(i int) bool { return tr.samples[i].t > end })
		}
		className := ""
		if tr.entity != nil {
			className, _ = packetecs2.GetObjectData[string](&tr.entity.Data, "unit__className")
		}
		parentSource := ""
		if (tr.key.missile || tr.key.decoy) && tr.parent != nil {
			parentSource = fmt.Sprintf("air:%d", tr.parent.key.id)
		}
		if err := qw.Write([]string{
			fmt.Sprintf("%s:%d", kind, tr.key.id),
			kind,
			player,
			trackCoalition(tr, slot),
			trackName(tr, slot, names),
			strconv.FormatFloat(float64(start)/1000, 'f', 3, 64),
			strconv.FormatFloat(float64(end)/1000, 'f', 3, 64),
			strconv.Itoa(sampleCount),
			className,
			parentSource,
		}); err != nil {
			return err
		}
	}
	qw.Flush()
	return qw.Error()
}

func trackName(tr *track, slot *packetslot.PacketSlotParser, names map[string]string) string {
	if name := manualTrackName(tr, names); name != "" {
		return name
	}
	if tr.key.decoy {
		if tr.estimated {
			return countermeasureEstimatedDisplayName
		}
		return countermeasureDisplayName
	}
	if tr.key.missile && strings.TrimSpace(tr.name) != "" {
		return tr.name
	}
	if tr.entity != nil {
		if model, ok := packetecs2.GetObjectData[string](&tr.entity.Data, "unit__className"); ok {
			if model = strings.TrimSpace(model); model != "" {
				return displayModelName(model)
			}
		}
	}
	if tr.key.missile {
		return fmt.Sprintf("missile:%d", tr.key.id)
	}
	if tr.key.air {
		return fmt.Sprintf("air:%d", tr.key.id)
	}
	return fmt.Sprintf("ground:%d", tr.key.id)
}

func manualTrackName(tr *track, names map[string]string) string {
	if len(names) > 0 {
		if tr.key.decoy {
			return strings.TrimSpace(names[fmt.Sprintf("countermeasure:%d", tr.key.id)])
		}
		if tr.key.missile {
			return strings.TrimSpace(names[fmt.Sprintf("missile:%d", tr.key.id)])
		}
		base := "ground"
		if tr.key.air {
			base = "air"
		}
		for _, key := range []string{fmt.Sprintf("%s:%d", base, tr.key.id), strconv.FormatUint(tr.key.id, 10), fmt.Sprintf("%X", tr.key.id)} {
			if v := strings.TrimSpace(names[key]); v != "" {
				return v
			}
		}
	}
	return ""
}

func trackPilot(tr *track, slot *packetslot.PacketSlotParser) string {
	if tr.entity == nil {
		return ""
	}
	player := entityPlayer(tr.entity, slot)
	if player == nil {
		return ""
	}
	return strings.TrimSpace(player.Uid.Name)
}

func trackCoalition(tr *track, slot *packetslot.PacketSlotParser) string {
	if tr.key.decoy {
		if tr.parent != nil {
			return trackCoalition(tr.parent, slot)
		}
		return "Neutral"
	}
	if tr.key.missile {
		if tr.parent != nil {
			return trackCoalition(tr.parent, slot)
		}
		return "Neutral"
	}
	if tr.entity != nil {
		if p := entityPlayer(tr.entity, slot); p != nil {
			switch p.Team {
			case 1:
				return "Blue"
			case 2:
				return "Red"
			}
		}
	}
	return "Neutral"
}

func trackListKind(tr *track) string {
	if tr.key.decoy {
		return "countermeasure"
	}
	if tr.key.missile {
		return "missile"
	}
	if tr.key.air {
		return "air"
	}
	return "ground"
}

func entityPlayer(e *packetecs2.Entity, slot *packetslot.PacketSlotParser) *packetslot.Player {
	if e == nil || slot == nil {
		return nil
	}
	pid, ok := packetecs2.GetObjectData[int32](&e.Data, "unit__playerId")
	if !ok || pid < 0 || int(pid) >= len(slot.Players) {
		return nil
	}
	return slot.Players[pid]
}

func escapeACMIValue(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
